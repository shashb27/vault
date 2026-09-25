package vault

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type identity struct{ User, Host string }

// Vault is a folder that owns Claude Code session state.
type Vault struct {
	Root        string // realpath of the vault folder (what Claude encodes)
	Dir         string // <root>/.vault
	SessionsDir string // <root>/.vault/sessions  (the shared Claude project dir)
	Enc         string // Claude's encoding of Root
	LinkPath    string // ~/.claude/projects/<Enc>
	Name        string
	self        identity
}

type config struct {
	VaultID   string `json:"vault_id"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	Schema    int    `json:"schema"`
}

type member struct {
	User      string `json:"user"`
	Host      string `json:"host"`
	VaultPath string `json:"vault_path"`
	JoinedAt  string `json:"joined_at"`
}

type usersFile struct {
	Users []member `json:"users"`
}

func selfIdentity() identity {
	id := identity{}
	if u := os.Getenv("VAULT_TEST_USER"); u != "" { // tests/sim.sh only
		id.User = u
	} else if u, err := user.Current(); err == nil {
		id.User = u.Username
		if i := strings.LastIndexAny(id.User, `\`); i >= 0 { // Windows DOMAIN\user
			id.User = id.User[i+1:]
		}
	} else {
		id.User = os.Getenv("USER")
	}
	if h := os.Getenv("VAULT_TEST_HOST"); h != "" {
		id.Host = h
	} else if h, err := os.Hostname(); err == nil {
		id.Host = strings.SplitN(h, ".", 2)[0]
	} else {
		id.Host = "?"
	}
	return id
}

// findVault walks up from cwd to the nearest folder containing .vault/sessions.
func findVault() (*Vault, bool) {
	d, err := os.Getwd()
	if err != nil {
		return nil, false
	}
	for {
		if dirExists(filepath.Join(d, vaultDirName, "sessions")) {
			return loadVault(d), true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil, false
		}
		d = parent
	}
}

func loadVault(root string) *Vault {
	root = realpath(root)
	v := &Vault{Root: root, Dir: filepath.Join(root, vaultDirName), self: selfIdentity()}
	v.SessionsDir = filepath.Join(v.Dir, "sessions")
	v.Enc = EncodePath(root)
	v.LinkPath = filepath.Join(projectsDir(), v.Enc)
	v.Name = v.config().Name
	if v.Name == "" {
		v.Name = filepath.Base(root)
	}
	return v
}

func (v *Vault) config() config {
	var c config
	if b, err := os.ReadFile(filepath.Join(v.Dir, "config.json")); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func (v *Vault) users() []member {
	var u usersFile
	if b, err := os.ReadFile(filepath.Join(v.Dir, "users.json")); err == nil {
		json.Unmarshal(b, &u)
	}
	return u.Users
}

func (v *Vault) writeUsers(us []member) error {
	b, _ := json.MarshalIndent(usersFile{Users: us}, "", " ")
	return os.WriteFile(filepath.Join(v.Dir, "users.json"), b, 0o644)
}

func (v *Vault) addUser() error {
	us := v.users()
	for _, m := range us {
		if m.User == v.self.User && m.Host == v.self.Host && samePath(m.VaultPath, v.Root) {
			return nil
		}
	}
	us = append(us, member{User: v.self.User, Host: v.self.Host, VaultPath: v.Root, JoinedAt: nowISO()})
	return v.writeUsers(us)
}

func (v *Vault) removeUser() error {
	var keep []member
	for _, m := range v.users() {
		if !(m.User == v.self.User && m.Host == v.self.Host) {
			keep = append(keep, m)
		}
	}
	if keep == nil {
		keep = []member{}
	}
	return v.writeUsers(keep)
}

// userFor attributes a transcript cwd to a member (best guess, not identity).
func (v *Vault) userFor(cwd string) string {
	if cwd == "" {
		return "?"
	}
	for _, m := range v.users() {
		if m.VaultPath != "" && underPathAny(cwd, m.VaultPath) {
			return m.User
		}
	}
	n := strings.ReplaceAll(cwd, "\\", "/")
	for _, prefix := range []string{"/Users/", "/home/"} {
		if strings.HasPrefix(n, prefix) {
			if parts := strings.Split(strings.TrimPrefix(n, prefix), "/"); parts[0] != "" {
				return parts[0]
			}
		}
	}
	if m := regexp.MustCompile(`^[A-Za-z]:/Users/([^/]+)`).FindStringSubmatch(n); m != nil {
		return m[1]
	}
	return "?"
}

func (v *Vault) isJoined() bool {
	t, isLink := readLink(v.LinkPath)
	return isLink && samePath(t, v.SessionsDir)
}

func (v *Vault) lastCwdIsMine(path string) bool {
	c := lastCwd(path)
	return c != "" && underPathAny(c, v.Root)
}

func (v *Vault) isMine(l *Lease) bool { return l.User == v.self.User && l.Host == v.self.Host }

// ---- registry of joined vaults on this machine ----

func registryAdd(root string) {
	os.MkdirAll(stateDir(), 0o755)
	for _, r := range registryList() {
		if samePath(r, root) {
			return
		}
	}
	f, err := os.OpenFile(registryPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, root)
		f.Close()
	}
}

func registryRemove(root string) {
	var keep []string
	for _, r := range registryList() {
		if !samePath(r, root) {
			keep = append(keep, r)
		}
	}
	os.WriteFile(registryPath(), []byte(strings.Join(keep, "\n")+"\n"), 0o644)
}

func registryList() []string {
	f, err := os.Open(registryPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ---- preflight checks ----

func (v *Vault) checkCloud() {
	if !isCloudPath(v.Root) {
		return
	}
	if running, known := cloudRunning(); known && !running {
		warn("OneDrive is not running — nothing you do here will reach your teammates until it starts.")
	}
	if syncState(v.Dir).Paused {
		warn("OneDrive sync is PAUSED — resume it from the menu-bar icon or nothing will propagate.")
	}
}

// quarantineConflicts moves OneDrive conflict copies (basename no longer a pure
// UUID) out of the sessions dir so Claude's own picker cannot open them.
func (v *Vault) quarantineConflicts() {
	matches, _ := filepath.Glob(filepath.Join(v.SessionsDir, "*.jsonl"))
	for _, f := range matches {
		base := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if uuidRe.MatchString(base) {
			continue
		}
		os.MkdirAll(filepath.Join(v.Dir, "conflicts"), 0o755)
		dest := filepath.Join(v.Dir, "conflicts", filepath.Base(f))
		if fileExists(dest) {
			dest = filepath.Join(v.Dir, "conflicts", fmt.Sprintf("%s.%d.jsonl", base, time.Now().Unix()))
		}
		if err := os.Rename(f, dest); err != nil {
			continue
		}
		warn("two people were in one session at the same time. OneDrive kept both copies;")
		warn("the extra one is now in .vault/conflicts/ — its turns are NOT in the main session.")
		fmt.Fprintln(os.Stderr, dim("  → next:")+" vault conflicts        to see and read what was lost")
	}
	// Marker conflict copies (<uuid>-<Machine>.done): a lost note at most, filed silently.
	dones, _ := filepath.Glob(filepath.Join(v.Dir, "handoff", "*.done"))
	for _, f := range dones {
		base := strings.TrimSuffix(filepath.Base(f), ".done")
		if uuidRe.MatchString(base) || !conflictNameRe.MatchString(base) {
			continue
		}
		os.MkdirAll(filepath.Join(v.Dir, "conflicts"), 0o755)
		dest := filepath.Join(v.Dir, "conflicts", filepath.Base(f))
		if fileExists(dest) {
			dest = filepath.Join(v.Dir, "conflicts", fmt.Sprintf("%s.%d.done", base, time.Now().Unix()))
		}
		os.Rename(f, dest)
	}
}

func (v *Vault) conflictCount() int {
	m, _ := filepath.Glob(filepath.Join(v.Dir, "conflicts", "*.jsonl"))
	return len(m)
}

func md5File(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "absent"
	}
	h := md5.Sum(b)
	return hex.EncodeToString(h[:])
}

// checkDrift warns when shared instruction/permission files changed since this
// user's last run: they are a deliberate shared surface AND an injection channel.
func (v *Vault) checkDrift() {
	os.MkdirAll(stateDir(), 0o755)
	statePath := filepath.Join(stateDir(), v.Enc+".drift")
	tracked := []string{filepath.Join(v.Root, "CLAUDE.md"), filepath.Join(v.Root, ".claude", "settings.json"), filepath.Join(v.Root, ".claude", "settings.local.json")}
	old := map[string]string{}
	firstRun := true
	if b, err := os.ReadFile(statePath); err == nil {
		firstRun = false
		for _, ln := range strings.Split(string(b), "\n") {
			if i := strings.LastIndex(ln, " "); i > 0 {
				old[ln[:i]] = ln[i+1:]
			}
		}
	}
	var changed []string
	var sb strings.Builder
	for _, f := range tracked {
		h := md5File(f)
		fmt.Fprintf(&sb, "%s %s\n", f, h)
		if firstRun {
			if h != "absent" {
				changed = append(changed, f)
			}
		} else if prev, ok := old[f]; ok && prev != h {
			changed = append(changed, f)
		}
	}
	os.WriteFile(statePath, []byte(sb.String()), 0o644)
	if len(changed) == 0 {
		return
	}
	if firstRun {
		warn("this vault already has shared instruction/permission files you have never reviewed:")
	} else {
		warn("shared instruction/permission files changed since your last run (a teammate edited them):")
	}
	for _, f := range changed {
		fmt.Fprintf(os.Stderr, "    %s\n", strings.TrimPrefix(f, v.Root+string(filepath.Separator)))
	}
	warn("they steer Claude and grant permissions on YOUR machine — skim them before working. Never 'always allow' in a vault.")
}

var memConflictRe = regexp.MustCompile(`^(.+?)(?: \(\d+\)|-[A-Za-z0-9][A-Za-z0-9 ’']*)$`)

func (v *Vault) checkMemoryConflicts() {
	mem := filepath.Join(v.SessionsDir, "memory")
	matches, _ := filepath.Glob(filepath.Join(mem, "*.md"))
	for _, p := range matches {
		stem := strings.TrimSuffix(filepath.Base(p), ".md")
		if m := memConflictRe.FindStringSubmatch(stem); m != nil && fileExists(filepath.Join(mem, m[1]+".md")) {
			warn("shared memory has a conflict copy: %s duplicates %s.md — two people's Claude wrote memory at once. Merge by hand in .vault/sessions/memory/.", filepath.Base(p), m[1])
		}
	}
}

var bypassRe = regexp.MustCompile(`"defaultMode"\s*:\s*"bypassPermissions"`)

// checkBypassSettings refuses to launch when the vault's shared settings turn
// off permission prompts: any member could then run anything on your machine.
func (v *Vault) checkBypassSettings() error {
	for _, f := range []string{filepath.Join(v.Root, ".claude", "settings.json"), filepath.Join(v.Root, ".claude", "settings.local.json")} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if bypassRe.Match(b) {
			return fail("refusing to start: %s sets defaultMode to bypassPermissions.\n  In a shared vault that means any member could make Claude run anything on your machine.\n  Remove that line (and ask who added it) before working here.", strings.TrimPrefix(f, v.Root+string(filepath.Separator)))
		}
	}
	return nil
}

func checkBypassFlags(args []string) error {
	prev := ""
	for _, a := range args {
		if a == "--dangerously-skip-permissions" || (prev == "--permission-mode" && a == "bypassPermissions") || a == "--permission-mode=bypassPermissions" {
			return fail("'%s' is not allowed inside a vault: the shared CLAUDE.md and settings here are editable by every member,\n  so bypassing permissions would let any member run anything on your machine. Use 'vault private' for that.", a)
		}
		prev = a
	}
	return nil
}

func (v *Vault) preflight() error {
	v.checkCloud()
	v.quarantineConflicts()
	v.checkDrift()
	v.checkMemoryConflicts()
	return v.checkBypassSettings()
}

// writeMembersMemory keeps a shared memory file listing members, so Claude
// knows any of them may be driving and that context is shared by agreement.
func (v *Vault) writeMembersMemory() {
	mem := filepath.Join(v.SessionsDir, "memory")
	os.MkdirAll(mem, 0o755)
	seen := map[string]bool{}
	var names []string
	for _, m := range v.users() {
		if !seen[m.User] {
			seen[m.User] = true
			names = append(names, m.User)
		}
	}
	body := "---\nname: vault-members\ndescription: Who shares this vault; any of them may be the person typing in a resumed session\nmetadata:\n  type: project\n---\n\n" +
		"This folder is a shared team vault. Members: " + strings.Join(names, ", ") + ".\n" +
		"Any member may be driving a session, and the whole conversation is shared among all members by agreement, " +
		"so continue with whoever is driving using the full context. The wrapper states who is driving when a session is resumed.\n"
	os.WriteFile(filepath.Join(mem, "vault-members.md"), []byte(body), 0o644)
	idx := filepath.Join(mem, "MEMORY.md")
	cur := "# Memory Index\n"
	if b, err := os.ReadFile(idx); err == nil {
		cur = string(b)
	}
	if !strings.Contains(cur, "vault-members.md") {
		os.WriteFile(idx, []byte(strings.TrimRight(cur, "\n")+"\n- [Vault members](vault-members.md) — who shares this vault; any may be driving\n"), 0o644)
	}
}

func bigSessionBytes() int64 {
	if s := os.Getenv("VAULT_BIG_SESSION_BYTES"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	return 5_000_000
}
