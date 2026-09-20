package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Main is the CLI entry point. Returns the process exit code.
func Main(args []string) int {
	initUI()
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	var err error
	rc := 0
	switch cmd {
	case "":
		err = cmdHome()
	case "init":
		err = cmdInit(args)
	case "join":
		err = cmdJoin()
	case "new", "start":
		rc, err = cmdNew(args)
	case "resume", "open":
		rc, err = cmdResume(args)
	case "sessions", "ls", "list":
		err = cmdSessions(args)
	case "rename":
		err = cmdRename(args)
	case "show", "log":
		err = cmdShow(args)
	case "status":
		err = cmdStatus(args)
	case "conflicts":
		err = cmdConflicts(args)
	case "doctor":
		rc, err = cmdDoctor()
	case "private":
		rc, err = cmdPrivate(args)
	case "leave":
		err = cmdLeave(args)
	case "members":
		err = cmdMembers()
	case "archive":
		err = cmdArchive(args)
	case "restore", "unarchive":
		err = cmdRestore(args)
	case "update":
		err = cmdUpdate(args)
	case "encode": // for the Windows checklist: what Claude should call this path
		p, _ := os.Getwd()
		if len(args) > 0 {
			p = args[0]
		}
		fmt.Print(EncodePath(realpath(p)))
	case "version", "--version", "-v":
		exe, _ := os.Executable()
		out("vault %s (%s/%s, %s)", Version, runtime.GOOS, runtime.GOARCH, exe)
	case "claude": // v0.1 compatibility
		if len(args) > 0 && args[0] == "--private" {
			rc, err = cmdPrivate(args[1:])
		} else {
			rc, err = cmdNew(args)
		}
	case "help", "--help", "-h":
		printHelp()
	default:
		errln("unknown command '%s'.", cmd)
		fmt.Fprintln(os.Stderr)
		printHelp()
		return 1
	}
	if err != nil {
		errln("%s", err.Error())
		return 1
	}
	return rc
}

func requireVault() (*Vault, error) {
	if v, ok := findVault(); ok {
		return v, nil
	}
	var sb strings.Builder
	sb.WriteString("You are not inside a vault folder.")
	if reg := registryList(); len(reg) > 0 {
		sb.WriteString("\n  Vaults you have joined on this machine:")
		for _, r := range reg {
			if dirExists(filepath.Join(r, vaultDirName)) {
				sb.WriteString("\n    cd \"" + r + "\"")
			}
		}
	} else {
		sb.WriteString("\n  To make a shared folder a vault:   cd <shared OneDrive folder> && vault init")
		sb.WriteString("\n  To join one a teammate made:       cd <that folder> && vault join")
	}
	return nil, errors.New(sb.String())
}

func requireJoined(v *Vault) error {
	if v.isJoined() {
		return nil
	}
	if t, isLink := readLink(v.LinkPath); isLink {
		return fail("your join is broken: %s points to '%s' instead of this vault. Run 'vault leave --force' then 'vault join'.", v.LinkPath, t)
	}
	return fail("You have not joined this vault yet (%s).\n  → next: vault join", bold(v.Name))
}

// ---------- home / help ----------

func cmdHome() error {
	v, ok := findVault()
	if !ok {
		out("%s %s — shared Claude Code sessions in a shared folder", bold("vault"), Version)
		out("")
		out("You are not inside a vault folder.")
		if reg := registryList(); len(reg) > 0 {
			out("Vaults you have joined on this machine:")
			for _, r := range reg {
				if dirExists(filepath.Join(r, vaultDirName)) {
					out("  cd \"%s\"", r)
				}
			}
			out("")
			hint("cd into one of them, then run 'vault' again")
		} else {
			out("")
			out("  Create one:   cd <a shared OneDrive folder>   &&  vault init")
			out("  Join one:     cd <the folder a teammate made>  &&  vault join")
			out("")
			out(dim("  'vault help' lists every command. 'vault doctor' checks your setup."))
		}
		return nil
	}
	out("%s  %s", bold(v.Name), dim(v.Root))
	if !v.isJoined() {
		out("  This folder is a vault with %d member(s), but you have not joined it.", len(v.users()))
		hint("vault join")
		return nil
	}
	v.checkCloud()
	v.quarantineConflicts()
	v.checkDrift()
	v.checkMemoryConflicts()
	rows := v.sessions()
	out("  joined as %s · %d members · %d session(s)", v.self.User, len(v.users()), len(rows))
	out("")
	if len(rows) == 0 {
		out("No sessions yet.")
		hint("vault new <name>        e.g.  vault new planning-review")
		return nil
	}
	printTable(rows, false, 8)
	out("")
	hint("vault resume <name>     continue one   ·   vault new <name>     start a new one   ·   vault help")
	return nil
}

func printHelp() {
	fmt.Printf(`%s %s — shared Claude Code sessions in a shared folder
%s

%s
  vault                      where am I, what's here, what next
  vault new <name>           start a shared session with a name teammates can find
  vault resume [<name>]      continue a session (waits for sync, checks nobody is in it)
  vault sessions             list sessions: name, state, who started, who last touched
  vault show <name> [N]      read the last N turns before you jump in
  vault rename <old> <new>   give a session a better name

%s
  vault init [name]          make the current folder a vault, and join it
  vault join                 join the vault in the current folder
  vault doctor               check Claude, OneDrive, join, pins — with fixes
  vault members              who has joined this vault
  vault leave [--force]      disconnect this machine from the vault

%s
  vault status [<name>]      health, who's in what, sync state of one session
  vault conflicts [show <#>] sessions two people were in at once; read the lost turns
  vault archive <name>       move a finished session out of the list (restore brings it back)
  vault private              a Claude session here that is NOT shared
  vault update               install the latest release
  vault version

%s  resume --steal (take over someone's session)  ·  resume --now (skip the sync wait)
       new/resume pass any other flags straight to claude (bypass-permissions flags are refused)

%s
`, bold("vault"), Version, dim("https://github.com/"+repoSlug), bold("Everyday"), bold("Setup (once)"), bold("When something's off"), bold("Flags"),
		dim("The conversation is shared; the workbench is not — /rewind, background tasks, prompt\nhistory and your personal permissions stay on your own machine. Trust model: docs/safety.md"))
}

// ---------- init / join / leave ----------

func cmdInit(args []string) error {
	if v, found := findVault(); found {
		joined := v.isJoined()
		ok("this folder is already a vault (%s).", bold(v.Name))
		if joined {
			hint("vault              to see what's here")
		} else {
			hint("vault join")
		}
		return nil
	}
	cwd, _ := os.Getwd()
	root := realpath(cwd)
	name := filepath.Base(root)
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	if !isCloudPath(root) {
		warn("this folder does not look like a synced OneDrive folder on this machine.")
		warn("a vault only shares if the folder syncs to your teammates. Continuing anyway (fine for testing).")
	}
	vd := filepath.Join(root, vaultDirName)
	for _, d := range []string{"sessions/memory", "handoff", "conflicts", "leases"} {
		os.MkdirAll(filepath.Join(vd, filepath.FromSlash(d)), 0o755)
	}
	self := selfIdentity()
	c := config{VaultID: newUUID(), Name: name, CreatedBy: self.User + "@" + self.Host, CreatedAt: nowISO(), Schema: 1}
	b, _ := json.Marshal(c)
	os.WriteFile(filepath.Join(vd, "config.json"), append(b, '\n'), 0o644)
	os.WriteFile(filepath.Join(vd, "users.json"), []byte("{\"users\":[]}\n"), 0o644)
	os.WriteFile(filepath.Join(vd, "index.json"), []byte("{\"sessions\":{}}\n"), 0o644)
	os.WriteFile(filepath.Join(vd, "sessions", "memory", "MEMORY.md"), []byte(`# Memory Index

- NOTE: this is SHARED VAULT MEMORY. Everything here is visible to and writable
  by every member of this vault, and is injected into every member's sessions.
  Do not store personal facts here.
`), 0o644)
	ok("created vault %s at %s", bold(name), root)
	out("")
	return cmdJoin()
}

func cmdJoin() error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	os.MkdirAll(projectsDir(), 0o755)
	os.MkdirAll(stateDir(), 0o755)
	if t, isLink := readLink(v.LinkPath); isLink {
		if samePath(t, v.SessionsDir) {
			registryAdd(v.Root)
			ok("already joined %s.", bold(v.Name))
			hint("vault              to see the sessions here")
			return nil
		}
		return fail("something else is linked at %s (%s). Run 'vault leave --force' there first.", v.LinkPath, t)
	}
	if dirExists(v.LinkPath) {
		os.MkdirAll(backupDir(), 0o755)
		backup := filepath.Join(backupDir(), fmt.Sprintf("%s.%d", v.Enc, time.Now().Unix()))
		if err := os.Rename(v.LinkPath, backup); err != nil {
			return fail("could not move your existing sessions aside: %v", err)
		}
		info("you had private Claude sessions for this folder from before joining — they were moved aside")
		info("(not shared, not deleted): %s", backup)
	}
	if err := makeLink(v.SessionsDir, v.LinkPath); err != nil {
		return fail("could not link %s → %s: %v", v.LinkPath, v.SessionsDir, err)
	}
	v.addUser()
	v.writeMembersMemory()
	registryAdd(v.Root)

	fmt.Print("  checking that Claude really uses the shared folder (one silent Claude call)…")
	verified, stray := v.joinSelfTest()
	fmt.Println()
	switch {
	case verified:
		ok("joined %s as %s@%s (verified: Claude writes into the vault)", bold(v.Name), v.self.User, v.self.Host)
	case stray != "":
		os.Remove(v.LinkPath)
		v.removeUser()
		registryRemove(v.Root)
		return fail("join FAILED: this Claude version stores sessions somewhere the vault does not expect\n  (%s). Nothing was joined. Please report this at https://github.com/%s/issues\n  with your 'claude --version' and the output of 'vault encode'.", stray, repoSlug)
	default:
		warn("could not verify (is Claude logged in? run 'claude' once by itself). Joined, but unproven —")
		warn("after your first 'vault new' check that the session shows up in 'vault sessions'.")
		ok("joined %s as %s@%s", bold(v.Name), v.self.User, v.self.Host)
	}

	out("")
	out("  %s", bold("Before you start — the three things to know"))
	out("  1. Everything Claude reads or prints in a vault session is shared with every member.")
	out("     Don't cat secrets or pull personal mail/calendar into a vault session.")
	out("  2. CLAUDE.md, .claude/settings and Claude's memory in this folder are shared and editable")
	out("     by every member — they steer Claude on YOUR machine. Never click 'don't ask again' here.")
	out("  3. One person per session at a time. Exit Claude when you're done so it hands off cleanly.")
	out("  %s", dim("Full trust model: docs/safety.md in the vault repo."))
	if isCloudPath(v.Root) {
		out("")
		if !syncState(v.Dir).Pinned {
			if err := pinFolder(v.Dir); err == nil {
				ok("pinned %s so OneDrive keeps it on this device", vaultDirName)
			} else {
				warn("one manual step: %s.", cloudSetupHint(v.Dir))
			}
		}
		filepath.Walk(v.SessionsDir, func(p string, fi os.FileInfo, err error) error { // materialize what exists today
			if err == nil && !fi.IsDir() {
				if f, e := os.Open(p); e == nil {
					buf := make([]byte, 65536)
					for {
						if _, e := f.Read(buf); e != nil {
							break
						}
					}
					f.Close()
				}
			}
			return nil
		})
	}
	out("")
	if n := len(v.sessions()); n == 0 {
		hint("vault new <name>        start the first shared session, e.g.  vault new planning-review")
	} else {
		hint("vault                   see the %d session(s) already here, then 'vault resume <name>'", n)
	}
	return nil
}

func cmdLeave(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	force := len(args) > 0 && args[0] == "--force"
	if _, isLink := readLink(v.LinkPath); !isLink {
		return fail("you are not joined to this vault.")
	}
	if !force {
		for _, l := range v.activeLeases() {
			if v.isMine(l) {
				return fail("you are still in a session here — exit Claude first, or 'vault leave --force'.")
			}
		}
	}
	if err := os.Remove(v.LinkPath); err != nil {
		return fail("could not remove %s: %v", v.LinkPath, err)
	}
	if backups, _ := filepath.Glob(filepath.Join(backupDir(), v.Enc+".*")); len(backups) > 0 {
		sort.Strings(backups)
		latest := backups[len(backups)-1]
		if os.Rename(latest, v.LinkPath) == nil {
			info("restored your pre-join private sessions from %s", latest)
		}
	}
	v.removeUser()
	v.writeMembersMemory()
	registryRemove(v.Root)
	ok("left %s. Claude in this folder is private again.", bold(v.Name))
	warn("leaving does not un-share: sessions you ran while joined stay in the vault, on teammates' machines and in OneDrive history.")
	return nil
}

func cmdMembers() error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	out("%s  %s", bold("Members of "+v.Name), dim("(whoever can open this folder can join; membership is the folder's sharing)"))
	for _, m := range v.users() {
		you := ""
		if m.User == v.self.User && m.Host == v.self.Host {
			you = dim("  (you)")
		}
		out("  %s@%s  joined %s%s", m.User, m.Host, strings.SplitN(m.JoinedAt, "T", 2)[0], you)
	}
	return nil
}

// ---------- new / resume / private ----------

func cmdNew(args []string) (int, error) {
	v, err := requireVault()
	if err != nil {
		return 1, err
	}
	if err := requireJoined(v); err != nil {
		return 1, err
	}
	name, steal := "", false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--steal":
			steal = true
		case a == "--private":
			return cmdPrivate(args[i+1:])
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		case name == "":
			name = a
		default:
			rest = append(rest, a)
		}
	}
	if name == "" {
		out("Every shared session needs a name so teammates can find it (e.g. %s, %s).", bold("planning-review"), bold("q4-budget"))
		name = readLine("  name for this session: ")
		if name == "" {
			return 1, fail("no name given.")
		}
	}
	if !nameRe.MatchString(name) {
		return 1, fail("'%s' is not a valid name: use letters, digits, '-', '_', '.', spaces (max 64 chars).", name)
	}
	if id, taken := v.nameTaken(name); taken {
		return 1, fail("a session named '%s' already exists in this vault (%s).\n  Continue it instead:   vault resume \"%s\"\n  or pick another name:  vault new \"%s-2\"", bold(name), id[:8], name, name)
	}
	if err := v.preflight(); err != nil {
		return 1, err
	}
	sid := newUUID()
	out("%s starting shared session %s in vault %s", grn("▶"), bold(name), bold(v.Name))
	out("  %s", dim("exit Claude (Ctrl+D or /exit) when you're done — that hands the session to the team."))
	out("")
	return v.launch(sid, steal, append([]string{"--session-id", sid, "--name", name}, rest...))
}

func cmdResume(args []string) (int, error) {
	v, err := requireVault()
	if err != nil {
		return 1, err
	}
	if err := requireJoined(v); err != nil {
		return 1, err
	}
	target, userASP := "", ""
	steal, force := false, false
	waitSecs := gateWaitSecs
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--steal":
			steal, force = true, true
		case a == "--force" || a == "--now":
			force, waitSecs = true, 0
		case a == "--wait" && i+1 < len(args):
			waitSecs, _ = strconv.Atoi(args[i+1])
			i++
		case a == "--append-system-prompt" && i+1 < len(args):
			userASP = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		case target == "":
			target = a
		default:
			rest = append(rest, a)
		}
	}
	if err := v.preflight(); err != nil {
		return 1, err
	}
	rows := v.sessions()
	var sid string
	if target == "" {
		if len(rows) == 0 {
			out("No sessions in this vault yet.")
			hint("vault new <name>")
			return 0, nil
		}
		out("%s", bold("Sessions in "+v.Name))
		printTable(rows, true, 0)
		out("")
		pick := readLine("  resume which? (number or name, Enter to cancel): ")
		if pick == "" {
			return 0, nil
		}
		if n, err := strconv.Atoi(pick); err == nil {
			if n < 1 || n > len(rows) {
				return 1, fail("no session #%d", n)
			}
			sid = rows[n-1].ID
		} else if sid, err = resolve(pick, rows); err != nil {
			return 1, err
		}
	} else if sid, err = resolve(target, rows); err != nil {
		return 1, err
	}
	f := filepath.Join(v.SessionsDir, sid+".jsonl")
	s := v.sessionByID(sid)
	label := s.Label()
	if isCloudPath(f) {
		os.ReadFile(f) // materialize
	}

	// ---- sync gate: wait for a complete transcript + a clean handoff, then go ----
	mine := v.lastCwdIsMine(f)
	waited, shown := 0, false
	var torn, handed bool
	for {
		torn = !tailValid(f)
		handed = fileExists(filepath.Join(v.Dir, "handoff", sid+".done"))
		if !torn && (handed || mine || force) {
			break
		}
		if waited >= waitSecs {
			break
		}
		if !shown {
			if torn {
				fmt.Printf("  waiting for OneDrive to finish delivering %s", label)
			} else {
				fmt.Printf("  waiting for the previous person to exit %s", label)
			}
			shown = true
		}
		fmt.Print(".")
		time.Sleep(5 * time.Second)
		waited += 5
	}
	if shown {
		fmt.Println()
	}
	if torn {
		return 1, fail("the transcript for '%s' is still incomplete on this machine (OneDrive mid-sync).\n  Try again in a minute:   vault resume \"%s\"\n  Check progress with:     vault status \"%s\"", label, label, label)
	}
	if !handed && !mine && !force {
		// No marker usually means Claude was started without the wrapper. If nobody
		// holds a lease and the transcript has been idle a while, treat it as finished.
		if fi, err := os.Stat(f); err == nil {
			idle := time.Since(fi.ModTime())
			if _, held := v.activeLeases()[sid]; !held && idle > idleOKSecs*time.Second {
				info(dim(fmt.Sprintf("no clean-exit marker (started outside vault?) but idle for %dm — treating it as finished.", int(idle.Minutes()))))
				handed = true
			}
		}
	}
	if !handed && !mine && !force {
		lastBy := s.LastBy
		if lastBy == "" || lastBy == "?" {
			lastBy = "someone"
		}
		warn("'%s' was last touched by %s and has no clean-exit marker yet.", label, lastBy)
		warn("either they are still in it, or their exit hasn't synced. If you both type, the session forks.")
		if !stdinIsTerminal() {
			return 1, fail("refusing without a terminal; pass --force to override.")
		}
		yn := readLine("  continue anyway? [y/N] ")
		if !strings.EqualFold(yn, "y") {
			out("  ok — try again in a minute, or ask %s.", lastBy)
			return 1, nil
		}
	}
	reorient := v.reorientPrompt()
	if userASP != "" {
		reorient += "\n\n" + userASP
	}
	out("%s resuming %s — the whole conversation so far is in context.", grn("▶"), bold(label))
	out("  %s", dim("exit Claude (Ctrl+D or /exit) when you're done to hand it back."))
	out("")
	return v.launch(sid, steal, append([]string{"--resume", sid, "--append-system-prompt", reorient}, rest...))
}

func cmdPrivate(args []string) (int, error) {
	v, err := requireVault()
	if err != nil {
		return 1, err
	}
	scratch := privateScratch(v.Root)
	os.MkdirAll(scratch, 0o755)
	out("%s private session — %s with the vault (runs from a local scratch folder)", grn("▶"), bold("not shared"))
	return runClaude(scratch, args), nil
}

// ---------- sessions / rename / show ----------

func cmdSessions(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "--json" {
		rows := v.sessions()
		if rows == nil {
			rows = []*Session{}
		}
		b, _ := json.MarshalIndent(rows, "", " ")
		fmt.Println(string(b))
		return nil
	}
	v.quarantineConflicts()
	out("%s  %s", bold("Sessions in "+v.Name), dim(fmt.Sprintf("(%d members)", len(v.users()))))
	printTable(v.sessions(), false, 0)
	out("")
	out(dim("  STARTED-BY / LAST-BY come from transcript contents — a best guess, not a verified identity."))
	if n := v.conflictCount(); n > 0 {
		warn("%d conflict cop(ies) exist — turns in them are missing from the sessions above. See 'vault conflicts'.", n)
	}
	hint("vault resume <name>     continue one   ·   vault new <name>     start another")
	return nil
}

func cmdRename(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	if err := requireJoined(v); err != nil {
		return err
	}
	if len(args) < 2 {
		return fail("usage: vault rename <name-or-id> <new-name>")
	}
	from, to := args[0], args[1]
	if !nameRe.MatchString(to) {
		return fail("'%s' is not a valid name: letters, digits, '-', '_', '.', spaces (max 64).", to)
	}
	sid, err := resolve(from, v.sessions())
	if err != nil {
		return err
	}
	if id, taken := v.nameTaken(to); taken && id != sid {
		return fail("another session is already named '%s' (%s).", to, id[:8])
	}
	if l := readLease(v.leasePath(sid)); l != nil && !l.Expired {
		return fail("%s@%s is in that session right now — rename it after they exit (or use /rename inside Claude).", l.User, l.Host)
	}
	f := filepath.Join(v.SessionsDir, sid+".jsonl")
	if !tailValid(f) {
		return fail("that transcript is still syncing — try again in a minute.")
	}
	old := v.sessionByID(sid).Label()
	if err := appendTitle(f, sid, to); err != nil {
		return fail("could not rename: %v", err)
	}
	ok("renamed %s → %s", old, bold(to))
	hint("vault resume \"%s\"", to)
	return nil
}

func cmdShow(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fail("usage: vault show <name> [turns]   — read the last turns before you resume")
	}
	n := 10
	if len(args) > 1 {
		n, _ = strconv.Atoi(args[1])
	}
	sid, err := resolve(args[0], v.sessions())
	if err != nil {
		return err
	}
	s := v.sessionByID(sid)
	f := filepath.Join(v.SessionsDir, sid+".jsonl")
	if isCloudPath(f) {
		os.ReadFile(f)
	}
	out("%s  %s", bold(s.Label()), dim(fmt.Sprintf("last %d turns · started by %s", n, s.Owner)))
	out("")
	printTurns(turns(f, n), 3000)
	hint("vault resume \"%s\"", s.Label())
	return nil
}

func printTurns(ts []Turn, max int) {
	for _, t := range ts {
		tag := bold("claude>")
		switch t.Who {
		case "user":
			tag = grn("human>")
		case "compact":
			tag = dim("[compacted summary]")
		}
		fmt.Printf("%s %s\n\n", tag, truncate(t.Text, max))
	}
}

// ---------- status / conflicts / doctor ----------

func cmdStatus(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	out("%s      %s", bold("Vault"), v.Name)
	out("%s     %s", bold("Folder"), v.Root)
	var ms []string
	for _, m := range v.users() {
		ms = append(ms, fmt.Sprintf("%s@%s (joined %s)", m.User, m.Host, strings.SplitN(m.JoinedAt, "T", 2)[0]))
	}
	out("%s    %s", bold("Members"), strings.Join(ms, " · "))
	if v.isJoined() {
		out("%s        joined as %s@%s", bold("You"), v.self.User, v.self.Host)
	} else {
		out("%s        %s — run 'vault join'", bold("You"), red("not joined"))
	}
	if isCloudPath(v.Root) {
		od := "running"
		if running, known := cloudRunning(); known && !running {
			od = red("NOT RUNNING")
		}
		st := syncState(v.Dir)
		if st.Paused {
			od += ", " + red("sync PAUSED")
		}
		if st.Pinned {
			od += ", pinned"
		} else {
			od += ", " + yel("not pinned") + " (" + cloudSetupHint(v.Dir) + ")"
		}
		out("%s   %s", bold("OneDrive"), od)
	} else {
		out("%s   n/a — not a synced OneDrive folder on this machine (local-only vault)", bold("OneDrive"))
	}
	leases := v.activeLeases()
	if len(leases) == 0 {
		out("%s     nobody right now", bold("In use"))
	} else {
		var parts []string
		for id, l := range leases {
			lbl := id[:min(8, len(id))]
			if s := v.sessionByID(id); s != nil {
				lbl = s.Label()
			}
			parts = append(parts, fmt.Sprintf("%s by %s@%s (%dm)", lbl, l.User, l.Host, l.AgeSecs/60))
		}
		out("%s     %s", bold("In use"), strings.Join(parts, "  "))
	}
	v.quarantineConflicts()
	if n := v.conflictCount(); n == 0 {
		out("%s  none", bold("Conflicts"))
	} else {
		out("%s  %s — see 'vault conflicts'", bold("Conflicts"), yel(fmt.Sprintf("%d quarantined", n)))
	}
	v.checkDrift()
	v.checkMemoryConflicts()
	if len(args) == 0 {
		return nil
	}
	sid, err := resolve(args[0], v.sessions())
	if err != nil {
		return err
	}
	s := v.sessionByID(sid)
	f := filepath.Join(v.SessionsDir, sid+".jsonl")
	fi, _ := os.Stat(f)
	out("")
	name := s.Name
	if name == "" {
		name = "(unnamed)"
	}
	out("%s    %s  %s", bold("Session"), name, dim(sid))
	out("  present    yes (%d bytes, modified %s)", fi.Size(), fi.ModTime().Format("2006-01-02 15:04:05"))
	complete := tailValid(f)
	if complete {
		out("  transcript %s", grn("complete"))
	} else {
		out("  transcript %s", yel("incomplete — OneDrive still delivering it"))
	}
	handed := fileExists(filepath.Join(v.Dir, "handoff", sid+".done"))
	if handed {
		b, _ := os.ReadFile(filepath.Join(v.Dir, "handoff", sid+".done"))
		out("  handoff    %s (%s)", grn("clean"), strings.TrimSpace(string(b)))
	} else {
		out("  handoff    %s — last person may still be in it", yel("no clean-exit marker"))
	}
	if st := syncState(f); st.Known {
		for _, ln := range strings.Split(st.Raw, "\n") {
			out("  onedrive   %s", ln)
		}
	}
	out("")
	if complete && handed {
		hint("vault resume \"%s\"     — safe to continue", s.Label())
	} else {
		hint("vault resume \"%s\"     — it will wait for sync and ask before proceeding", s.Label())
	}
	return nil
}

var conflictNameRe = regexpMust(`^([0-9a-fA-F-]{36})-?(.*)$`)

func cmdConflicts(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	v.quarantineConflicts()
	files, _ := filepath.Glob(filepath.Join(v.Dir, "conflicts", "*.jsonl"))
	sort.Strings(files)
	if len(args) >= 2 && args[0] == "show" {
		n, _ := strconv.Atoi(args[1])
		if n < 1 || n > len(files) {
			return fail("no such conflict number")
		}
		out(dim("# " + filepath.Base(files[n-1])))
		printTurns(turns(files[n-1], 0), 2000)
		return nil
	}
	out("%s", bold("Conflict copies in "+v.Name))
	out(dim("  Each is a branch of a session that two people were in at the same time. Its turns are"))
	out(dim("  NOT in the main session. Read one with 'vault conflicts show <#>' and paste anything"))
	out(dim("  important back into a live session. Prevention: one person per session; exit when done."))
	if len(files) == 0 {
		out(dim("  (no conflict copies)"))
		return nil
	}
	names := map[string]string{}
	for _, s := range v.sessions() {
		names[s.ID] = s.Name
	}
	for i, p := range files {
		base := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		sid, machine := base, ""
		if m := conflictNameRe.FindStringSubmatch(base); m != nil {
			sid, machine = m[1], m[2]
		}
		if machine == "" {
			machine = "?"
		}
		nm := names[strings.ToLower(sid)]
		if nm == "" {
			nm = sid[:min(8, len(sid))]
		}
		out("  %2d  %s from %s %s lines  %s", i+1, pad(bold(nm), 24), pad(machine, 28), lpad(strconv.Itoa(countLines(p)), 4), dim(filepath.Base(p)))
	}
	return nil
}

func countLines(p string) int {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func cmdDoctor() (int, error) {
	fails := 0
	chk := func(good bool, label, fix string) {
		if good {
			ok("%s", label)
			return
		}
		out("%s %s", red("✗"), label)
		if fix != "" {
			out("    %s %s", dim("fix:"), fix)
		}
		fails++
	}
	exe, _ := os.Executable()
	out("%s  %s", bold("vault doctor"), dim(fmt.Sprintf("(vault %s, %s/%s, %s)", Version, runtime.GOOS, runtime.GOARCH, exe)))
	chk(true, platformName, "")
	cv := claudeVersion()
	chk(cv != "", "Claude Code installed "+dim(cv), "install Claude Code and run 'claude' once to log in")
	_, err := exec.LookPath("gh")
	chk(err == nil, "GitHub CLI (gh) available — needed by 'vault update'", "https://cli.github.com then 'gh auth login'")
	v, inVault := findVault()
	if inVault {
		chk(true, "inside vault "+bold(v.Name)+" "+dim(v.Root), "")
		if v.isJoined() {
			chk(true, "joined — Claude sessions started here go to the shared folder", "")
		} else {
			chk(false, "not joined", "vault join")
		}
		if isCloudPath(v.Root) {
			if running, known := cloudRunning(); known {
				chk(running, "OneDrive running", "open OneDrive")
			}
			st := syncState(v.Dir)
			chk(!st.Paused, "OneDrive sync not paused", "resume sync from the OneDrive icon")
			chk(st.Pinned, "vault folder pinned (always keep on this device)", cloudSetupHint(v.Dir))
		} else {
			warn("not a synced OneDrive folder on this machine — sessions will not reach anyone else")
		}
		n := v.conflictCount()
		chk(n == 0, fmt.Sprintf("no conflict copies (%d)", n), "vault conflicts")
		if leases := v.activeLeases(); len(leases) > 0 {
			var parts []string
			for id, l := range leases {
				parts = append(parts, id[:min(8, len(id))]+" by "+l.User)
			}
			info("%s %s", yel("in use:"), strings.Join(parts, " "))
		}
		v.checkDrift()
		v.checkMemoryConflicts()
		if err := v.checkBypassSettings(); err != nil {
			chk(false, "shared settings do not bypass permissions", err.Error())
		} else {
			chk(true, "shared settings do not bypass permissions", "")
		}
	} else {
		chk(false, "inside a vault folder", "cd into a shared OneDrive folder, then 'vault init' or 'vault join'")
	}
	out("")
	if fails == 0 {
		ok("%s", bold("all good"))
		return 0, nil
	}
	warn("%d problem(s) above", fails)
	return 1, nil
}

// ---------- archive / restore ----------

func (v *Vault) archiveDir() string { return filepath.Join(v.Dir, "archive") }

func cmdArchive(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "list" {
		rows := v.buildIndex(v.archiveDir())
		out("%s", bold("Archived sessions in "+v.Name))
		printTable(rows, false, 0)
		hint("vault restore <name>    to bring one back   ·   vault archive <name>   to archive another")
		return nil
	}
	if err := requireJoined(v); err != nil {
		return err
	}
	sid, err := resolve(args[0], v.sessions())
	if err != nil {
		return err
	}
	if l, held := v.activeLeases()[sid]; held {
		return fail("%s@%s is in that session right now — archive it after they exit.", l.User, l.Host)
	}
	s := v.sessionByID(sid)
	os.MkdirAll(v.archiveDir(), 0o755)
	moves := [][2]string{
		{filepath.Join(v.SessionsDir, sid+".jsonl"), filepath.Join(v.archiveDir(), sid+".jsonl")},
		{filepath.Join(v.SessionsDir, sid), filepath.Join(v.archiveDir(), sid)},
		{filepath.Join(v.Dir, "handoff", sid+".done"), filepath.Join(v.archiveDir(), sid+".done")},
	}
	for _, m := range moves {
		if _, err := os.Stat(m[0]); err == nil {
			if err := os.Rename(m[0], m[1]); err != nil {
				return fail("could not move %s: %v", m[0], err)
			}
		}
	}
	ok("archived %s — out of the list, nothing deleted (.vault/archive/).", bold(s.Label()))
	hint("vault restore \"%s\"    if you need it back", s.Label())
	return nil
}

func cmdRestore(args []string) error {
	v, err := requireVault()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fail("usage: vault restore <name-or-id>")
	}
	rows := v.buildIndex(v.archiveDir())
	sid, err := resolve(args[0], rows)
	if err != nil {
		return err
	}
	var s *Session
	for _, r := range rows {
		if r.ID == sid {
			s = r
		}
	}
	if s.Name != "" {
		if id, taken := v.nameTaken(s.Name); taken && id != sid {
			return fail("a live session is already named '%s' — rename it first.", s.Name)
		}
	}
	moves := [][2]string{
		{filepath.Join(v.archiveDir(), sid+".jsonl"), filepath.Join(v.SessionsDir, sid+".jsonl")},
		{filepath.Join(v.archiveDir(), sid), filepath.Join(v.SessionsDir, sid)},
		{filepath.Join(v.archiveDir(), sid+".done"), filepath.Join(v.Dir, "handoff", sid+".done")},
	}
	for _, m := range moves {
		if _, err := os.Stat(m[0]); err == nil {
			if err := os.Rename(m[0], m[1]); err != nil {
				return fail("could not move %s: %v", m[0], err)
			}
		}
	}
	ok("restored %s", bold(s.Label()))
	hint("vault resume \"%s\"", s.Label())
	return nil
}

// ---------- update ----------

func assetName() string {
	n := "vault-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

func cmdUpdate(args []string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fail("'vault update' needs the GitHub CLI (gh) logged in, because the repo is private. Install from https://cli.github.com and run 'gh auth login'.")
	}
	exe, err := os.Executable()
	if err != nil {
		return fail("cannot find my own executable: %v", err)
	}
	exe = realpath(exe)
	tmp, err := os.MkdirTemp("", "vault-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tag := ""
	if len(args) > 0 && args[0] != "" {
		tag = args[0] // explicit tag
	} else { // newest release INCLUDING pre-releases (gh's "latest" skips betas)
		outb, err := exec.Command("gh", "release", "list", "-R", repoSlug, "--limit", "1", "--json", "tagName", "--jq", ".[0].tagName").Output()
		tag = strings.TrimSpace(string(outb))
		if err != nil || tag == "" {
			return fail("could not find a release at https://github.com/%s (is gh logged in, do you have access?)", repoSlug)
		}
	}
	if tag == "v"+Version || tag == Version {
		ok("already on %s", tag)
		return nil
	}
	ghArgs := []string{"release", "download", tag, "-R", repoSlug, "-p", assetName(), "-D", tmp, "--clobber"}
	fmt.Printf("  downloading %s…", tag)
	if outb, err := exec.Command("gh", ghArgs...).CombinedOutput(); err != nil {
		fmt.Println()
		return fail("download failed: %s", strings.TrimSpace(string(outb)))
	}
	fmt.Println()
	fresh := filepath.Join(tmp, assetName())
	os.Chmod(fresh, 0o755)
	newVer, _ := exec.Command(fresh, "version").Output()
	if runtime.GOOS == "windows" { // a running exe can't be overwritten, but it can be renamed
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return fail("could not move the running binary aside: %v", err)
		}
	}
	if err := copyFile(fresh, exe); err != nil {
		return fail("could not replace %s: %v", exe, err)
	}
	nv := strings.TrimSpace(string(newVer))
	if f := strings.Fields(nv); len(f) >= 2 {
		nv = f[1]
	}
	ok("updated %s → %s", Version, nv)
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
