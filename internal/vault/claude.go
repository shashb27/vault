package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func claudeVersion() string {
	out, err := exec.Command(claudeBinary, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// runClaude execs claude in dir with the user's terminal attached.
func runClaude(dir string, args []string) int {
	cmd := exec.Command(claudeBinary, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Let Claude handle Ctrl-C itself; we just wait.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		errln("could not run claude: %v", err)
		return 1
	}
	return 0
}

// joinSelfTest launches claude once with a nonce and verifies the transcript
// landed in the vault (Claude's path encoding is undocumented and version-specific).
// Returns: verified, strayPath (encoding mismatch), inconclusive.
func (v *Vault) joinSelfTest() (bool, string) {
	nonce := newUUID()
	start := time.Now().Add(-2 * time.Second)
	cmd := exec.Command(claudeBinary, "-p", "vault join self-test "+nonce+" — reply OK")
	cmd.Dir = v.Root
	cmd.Run()
	if f := findNonce(v.SessionsDir, nonce, start); f != "" {
		sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		os.Remove(f)
		os.Remove(filepath.Join(v.Dir, "handoff", sid+".done"))
		os.RemoveAll(filepath.Join(v.SessionsDir, sid))
		return true, ""
	}
	entries, _ := os.ReadDir(projectsDir())
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err != nil || fi.ModTime().Before(start) {
			continue
		}
		if f := findNonce(filepath.Join(projectsDir(), e.Name()), nonce, start); f != "" {
			return false, f
		}
	}
	return false, ""
}

func findNonce(dir, nonce string, since time.Time) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, f := range matches {
		fi, err := os.Stat(f)
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		b, err := os.ReadFile(f)
		if err == nil && bytes.Contains(b, []byte(nonce)) {
			return f
		}
	}
	return ""
}

func (v *Vault) reorientPrompt() string {
	return fmt.Sprintf("This session lives in a shared team vault ('%s'). All members of the vault are co-owners of this session and act as one principal: every member has agreed that the entire conversation, including everything said or produced in earlier turns, is shared among all members, and a different member taking over the keyboard is expected and authorized by all of them. There is no per-person confidentiality inside a vault session; do not withhold earlier content from the current driver on the grounds that another member provided it. The member now driving is %s; earlier turns may have been driven by other members. Continue the work with them exactly as you would with the original person, using the full context. The vault root on THIS machine is: %s — re-resolve any absolute file paths from earlier in the conversation relative to this root before using them.", v.Name, v.self.User, v.Root)
}

// launch runs claude for session sid with the lease / heartbeat / handoff lifecycle.
func (v *Vault) launch(sid string, steal bool, args []string) (int, error) {
	if err := checkBypassFlags(args); err != nil {
		return 1, err
	}
	if err := v.acquireLease(sid, steal); err != nil {
		return 1, err
	}
	stop := make(chan struct{})
	go v.heartbeat(sid, stop)
	start := time.Now()
	sizes := map[string]int64{}
	if matches, _ := filepath.Glob(filepath.Join(v.SessionsDir, "*.jsonl")); matches != nil {
		for _, f := range matches {
			if st, err := os.Stat(f); err == nil {
				sizes[f] = st.Size()
			}
		}
	}
	os.Remove(filepath.Join(v.Dir, "handoff", sid+".done")) // live again
	rc := runClaude(v.Root, args)
	close(stop)
	out("")
	v.finishHandoff(start, sizes)
	v.releaseLease(sid)
	return rc, nil
}

// finishHandoff marks every session THIS run touched as handed off, waits for
// the upload where the platform can tell, and scans what was added for secrets.
func (v *Vault) finishHandoff(start time.Time, before map[string]int64) {
	os.MkdirAll(filepath.Join(v.Dir, "handoff"), 0o755)
	touched := false
	matches, _ := filepath.Glob(filepath.Join(v.SessionsDir, "*.jsonl"))
	for _, f := range matches {
		sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if !uuidRe.MatchString(sid) {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil || !fi.ModTime().After(start) {
			continue
		}
		if !v.lastCwdIsMine(f) { // a teammate's session syncing in mid-run is not ours
			continue
		}
		touched = true
		os.WriteFile(filepath.Join(v.Dir, "handoff", sid+".done"),
			[]byte(fmt.Sprintf("{\"user\":\"%s\",\"host\":\"%s\",\"released_at\":\"%s\"}\n", v.self.User, v.self.Host, nowISO())), 0o644)
		if kinds := scanSecrets(f, before[f]); len(kinds) > 0 {
			warn("what was just shared looks like it contains a secret: %s. Everyone in the vault can read it; rotate it if real.", strings.Join(kinds, ", "))
		}
		name := ""
		if s := v.sessionByID(sid); s != nil {
			name = s.Name
		}
		label := name
		if label == "" {
			label = sid[:8]
		}
		if isCloudPath(f) {
			fmt.Printf("  uploading %s to OneDrive…", label)
			waited := 0
			if syncState(f).UploadKnown {
				for waited < uploadWaitSecs && syncState(f).Uploading {
					time.Sleep(3 * time.Second)
					waited += 3
					fmt.Print(".")
				}
			} else {
				time.Sleep(3 * time.Second) // no upload signal on this platform: short grace
			}
			fmt.Println()
		}
		ok("session %s handed off — teammates can resume it once OneDrive delivers it.", bold(label))
		if name != "" {
			hint("they run:  vault resume \"%s\"", name)
		} else {
			hint("they run:  vault resume %s   (give it a name: vault rename %s <name>)", sid[:8], sid[:8])
		}
	}
	if !touched {
		info(dim("(no shared session was changed in this run)"))
	}
}
