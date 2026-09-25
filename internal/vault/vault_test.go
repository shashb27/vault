package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Encoding cases verified against the real Claude Code encoder (macOS) plus the
// Windows shapes the join self-test will confirm on a real machine.
func TestEncodePath(t *testing.T) {
	cases := map[string]string{
		"/tmp/my vault_dir": "-tmp-my-vault-dir",
		"/tmp/café_vault":   "-tmp-caf--vault", // é is one UTF-16 unit
		"/a/b.c-d":          "-a-b-c-d",
		"/Users/alice.smith/Library/CloudStorage/OneDrive-ExampleCo/Team-vault": "-Users-alice-smith-Library-CloudStorage-OneDrive-ExampleCo-Team-vault",
		`C:\Users\Administrator\OneDrive - Example Co\playground`:               "C--Users-Administrator-OneDrive---Example-Co-playground",
	}
	for in, want := range cases {
		if got := EncodePath(in); got != want {
			t.Errorf("EncodePath(%q) = %q, want %q", in, got, want)
		}
	}
	// >200 units: truncated to 200 + "-" + base36 hash, deterministic
	long := "/" + strings.Repeat("a", 250)
	got := EncodePath(long)
	if len(got) <= 201 || got[200] != '-' || got[:200] != "-"+strings.Repeat("a", 199) {
		t.Errorf("long path encoding wrong: %q", got)
	}
	if EncodePath(long) != got {
		t.Error("long path encoding not deterministic")
	}
}

func TestBase36(t *testing.T) {
	if base36(0) != "0" || base36(35) != "z" || base36(36) != "10" {
		t.Error("base36 wrong")
	}
}

func mk(id, name, owner string, mtime int64) *Session {
	return &Session{ID: id, Name: name, Owner: owner, Mtime: mtime}
}

func TestResolve(t *testing.T) {
	rows := []*Session{
		mk("7b63838e-25e4-4780-89ef-b8d0cc7a8f4f", "falcon", "alice", 10),
		mk("0fdfe845-1111-2222-3333-444444444444", "", "alice", 9),
		mk("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "Kestrel", "bob", 8),
		mk("aaaaaaaa-0000-cccc-dddd-eeeeeeeeeeee", "kestrel-2", "bob", 7),
	}
	good := map[string]string{
		"falcon":                               "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f",
		"FALCON":                               "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f",
		"fal":                                  "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f",
		"0fdfe845":                             "0fdfe845-1111-2222-3333-444444444444",
		"kestrel":                              "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", // exact case-insensitive beats prefix
		"kestrel-2":                            "aaaaaaaa-0000-cccc-dddd-eeeeeeeeeeee",
		"7b63838e-25e4-4780-89ef-b8d0cc7a8f4f": "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f",
	}
	for in, want := range good {
		got, err := resolve(in, rows)
		if err != nil || got != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"aaaaaaaa", "nope", "kes"} { // ambiguous id prefix, missing, ambiguous name prefix
		if _, err := resolve(bad, rows); err == nil {
			t.Errorf("resolve(%q) should fail", bad)
		}
	}
	// duplicate exact names are reported, not silently picked
	dup := append(rows, mk("bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee", "falcon", "carol", 1))
	if _, err := resolve("falcon", dup); err == nil || !strings.Contains(err.Error(), "several sessions share") {
		t.Errorf("duplicate names should be an error, got %v", err)
	}
}

func TestTranscriptParsing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"custom-title","customTitle":"first-name","sessionId":"x"}`,
		`{"type":"user","cwd":"/Users/alice/vault","message":{"role":"user","content":"<local-command>ignored</local-command>"}}`,
		`{"type":"user","cwd":"/Users/alice/vault","message":{"role":"user","content":[{"type":"text","text":"Our release   train is FALCON-42"}]}}`,
		`{"type":"assistant","cwd":"/Users/alice/vault","message":{"role":"assistant","content":[{"type":"text","text":"OK"}]}}`,
		`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"summary of earlier"}}`,
		`{"type":"custom-title","customTitle":"renamed","sessionId":"x"}`,
		`{"type":"user","cwd":"/Users/bob/vault","message":{"role":"user","content":"what train?"}}`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	r := scanTranscript(p)
	if r.Title != "renamed" {
		t.Errorf("title: last custom-title should win, got %q", r.Title)
	}
	if r.FirstCwd != "/Users/alice/vault" || r.LastCwd != "/Users/bob/vault" {
		t.Errorf("cwd first/last wrong: %q %q", r.FirstCwd, r.LastCwd)
	}
	if r.FirstPrompt != "Our release train is FALCON-42" {
		t.Errorf("first prompt should skip <…> and squash spaces, got %q", r.FirstPrompt)
	}
	if !tailValid(p) {
		t.Error("complete file should be tail-valid")
	}
	ts := turns(p, 0)
	if len(ts) != 4 || ts[2].Who != "compact" || ts[0].Text != "Our release   train is FALCON-42" {
		t.Errorf("turns wrong: %+v", ts)
	}
	if got := turns(p, 1); len(got) != 1 || got[0].Text != "what train?" {
		t.Errorf("last-n turns wrong: %+v", got)
	}
	if lastCwd(p) != "/Users/bob/vault" {
		t.Error("lastCwd wrong")
	}
	// torn tail
	b, _ := os.ReadFile(p)
	os.WriteFile(p, b[:len(b)-10], 0o644)
	if tailValid(p) {
		t.Error("truncated file must not be tail-valid")
	}
	// rename appends a record Claude will honour
	os.WriteFile(p, b, 0o644)
	if err := appendTitle(p, "x", "final"); err != nil || scanTranscript(p).Title != "final" {
		t.Errorf("appendTitle failed: %v", err)
	}
}

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	body := "hello AKIAABCDEFGHIJKLMNOP world\n-----BEGIN RSA PRIVATE KEY-----\nghp_" + strings.Repeat("a", 36) + "\n"
	os.WriteFile(p, []byte(body), 0o644)
	got := scanSecrets(p, 0)
	if len(got) != 3 {
		t.Errorf("expected 3 kinds, got %v", got)
	}
	if got := scanSecrets(p, int64(len(body))); len(got) != 0 {
		t.Errorf("nothing appended → nothing found, got %v", got)
	}
	os.WriteFile(p, []byte("just a normal transcript with sk-ant-short"), 0o644)
	if got := scanSecrets(p, 0); len(got) != 0 {
		t.Errorf("false positive: %v", got)
	}
}

func TestLease(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "l.json")
	fresh := `{"user":"carol","host":"c","pid":1,"acquired_at":"` + time.Now().UTC().Format(time.RFC3339) + `","ttl_minutes":60}`
	os.WriteFile(p, []byte(fresh), 0o644)
	if l := readLease(p); l == nil || l.Expired || l.User != "carol" {
		t.Errorf("fresh lease misread: %+v", l)
	}
	old := `{"user":"carol","host":"c","pid":1,"acquired_at":"` + time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339) + `","ttl_minutes":60}`
	os.WriteFile(p, []byte(old), 0o644)
	if l := readLease(p); l == nil || !l.Expired {
		t.Errorf("2h-old lease should be expired: %+v", l)
	}
	os.WriteFile(p, []byte("not json"), 0o644)
	if readLease(p) != nil {
		t.Error("garbage lease should read as nil")
	}
}

func TestConflictAndMemoryNames(t *testing.T) {
	m := conflictNameRe.FindStringSubmatch("98c2c061-24a5-4738-8895-5f3168711679-Shash’s MacBook Pro-2")
	if m == nil || m[1] != "98c2c061-24a5-4738-8895-5f3168711679" || m[2] != "Shash’s MacBook Pro-2" {
		t.Errorf("conflict name parse wrong: %v", m)
	}
	for stem, want := range map[string]string{"MEMORY-Bob’s MacBook Pro": "MEMORY", "MEMORY (1)": "MEMORY", "vault-members": ""} {
		mm := memConflictRe.FindStringSubmatch(stem)
		got := ""
		if mm != nil {
			got = mm[1]
		}
		if want == "" && stem == "vault-members" {
			// "vault-members" matches the regexp shape but has no "vault.md" sibling; caller checks existence
			continue
		}
		if got != want {
			t.Errorf("memory conflict stem %q → %q, want %q", stem, got, want)
		}
	}
}

func TestUserForAndPaths(t *testing.T) {
	v := &Vault{}
	if v.userFor("/Users/bob.jones/Library/CloudStorage/x") != "bob.jones" {
		t.Error("mac home attribution")
	}
	if v.userFor(`C:\Users\Administrator\OneDrive - Example Co\v`) != "Administrator" {
		t.Error("windows home attribution")
	}
	if v.userFor("") != "?" {
		t.Error("empty cwd")
	}
	if !underPathAny(`C:\Users\a\OneDrive\v\sub`, `C:/Users/a/OneDrive/v`) {
		t.Error("underPathAny should tolerate mixed separators")
	}
	if underPathAny("/Users/a/vault2", "/Users/a/vault") {
		t.Error("underPathAny must not match sibling with common prefix")
	}
	if humanSize(512) != "512B" || humanSize(2048) != "2K" || humanSize(3*1048576) != "3.0M" {
		t.Error("humanSize")
	}
	if !nameRe.MatchString("planning-review 2") || nameRe.MatchString("-bad") || nameRe.MatchString("a/b") {
		t.Error("name validation")
	}
}

func TestBypassFlags(t *testing.T) {
	if checkBypassFlags([]string{"--resume", "x", "--dangerously-skip-permissions"}) == nil {
		t.Error("should refuse --dangerously-skip-permissions")
	}
	if checkBypassFlags([]string{"--permission-mode", "bypassPermissions"}) == nil {
		t.Error("should refuse --permission-mode bypassPermissions")
	}
	if checkBypassFlags([]string{"--permission-mode", "plan", "-p", "hi"}) != nil {
		t.Error("plan mode is fine")
	}
}

// ---------- 0.4 handoff notes ----------

func tempVault(t *testing.T, users string) *Vault {
	t.Helper()
	dir := t.TempDir()
	v := &Vault{Root: dir, Dir: filepath.Join(dir, ".vault"), SessionsDir: filepath.Join(dir, ".vault", "sessions"), self: identity{"Administrator", "SHASH-PC"}}
	os.MkdirAll(v.SessionsDir, 0o755)
	os.MkdirAll(filepath.Join(v.Dir, "handoff"), 0o755)
	os.WriteFile(filepath.Join(v.Dir, "users.json"), []byte(users), 0o644)
	return v
}

const fixtureUsers = `{"users":[{"user":"alice","host":"alice-mac","vault_path":"/Users/alice/v"},{"user":"Administrator","host":"SHASH-PC","vault_path":"C:\\Users\\Administrator\\v"},{"user":"shashvath.bhaskar","host":"shash-mbp","vault_path":"/Users/s/v"}]}`

func TestMarker(t *testing.T) {
	v := tempVault(t, fixtureUsers)
	sid := "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f"
	if err := v.writeMarker(sid, Marker{User: "alex", Host: "alex-mac", ReleasedAt: "2026-09-24T00:00:00Z", To: "sam", Note: "check totals"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(v.markerPath(sid))
	if strings.Count(string(b), "\n") != 1 || !strings.HasSuffix(string(b), "\n") {
		t.Errorf("marker must be one line ending in newline: %q", b)
	}
	m := v.readMarker(sid)
	if m == nil || m.To != "sam" || m.Note != "check totals" || m.User != "alex" {
		t.Errorf("round trip failed: %+v", m)
	}
	os.WriteFile(v.markerPath(sid), []byte(`{"user":"a","host":"h","released_at":"2026-09-24T00:00:00Z"}`+"\n"), 0o644)
	if m := v.readMarker(sid); m == nil || m.To != "" || m.Note != "" || m.User != "a" {
		t.Errorf("legacy marker: %+v", m)
	}
	os.WriteFile(v.markerPath(sid), []byte("garbage"), 0o644)
	if v.readMarker(sid) != nil || !fileExists(v.markerPath(sid)) {
		t.Error("garbage marker should read nil while the file exists")
	}
	out, _ := json.Marshal(Marker{User: "a", Host: "h", ReleasedAt: "x"})
	if strings.Contains(string(out), "\"to\"") || strings.Contains(string(out), "\"note\"") {
		t.Errorf("omitempty: %s", out)
	}
}

func TestParseNote(t *testing.T) {
	cases := []struct{ in, to, note string }{
		{"@sam check the totals", "sam", "check the totals"},
		{"plain", "", "plain"},
		{"@sam", "sam", ""},
		{"@", "", ""},
		{"a\n\nb   c", "", "a b c"},
		{"   spaced   ", "", "spaced"},
	}
	for _, c := range cases {
		to, note := parseNote(c.in)
		if to != c.to || note != c.note {
			t.Errorf("parseNote(%q) = (%q,%q), want (%q,%q)", c.in, to, note, c.to, c.note)
		}
	}
	_, long := parseNote(strings.Repeat("é", 400))
	if len([]rune(long)) != 280 {
		t.Errorf("cap should be 280 runes, got %d", len([]rune(long)))
	}
}

func TestNoteSecret(t *testing.T) {
	if !noteHasSecret("rotate AKIAABCDEFGHIJKLMNOP") || noteHasSecret("check section 3") {
		t.Error("noteHasSecret")
	}
}

func TestMatchMember(t *testing.T) {
	v := tempVault(t, fixtureUsers)
	all := []string{"alice@alice-mac", "Administrator@SHASH-PC", "shashvath.bhaskar@shash-mbp"}
	cases := map[string]string{"BOB": "", "shash": "", "shashv": "shashvath.bhaskar", "ali": "alice", "shash-pc": "SHASH-PC",
		"administrator": "Administrator", "alice@alice-mac": "alice@alice-mac", "": ""}
	for in, want := range cases {
		got, cands := v.matchMember(in)
		if got != want {
			t.Errorf("matchMember(%q) = %q, want %q", in, got, want)
		}
		if want == "" && in != "" && strings.Join(cands, ",") != strings.Join(all, ",") {
			t.Errorf("matchMember(%q) candidates = %v", in, cands)
		}
	}
}

func TestAddressedToMe(t *testing.T) {
	v := tempVault(t, fixtureUsers)
	for in, want := range map[string]bool{"administrator": true, "shash-pc": true, "Administrator@shash-pc": true, "shashvath.bhaskar": false, "": false} {
		if v.addressedToMe(in) != want {
			t.Errorf("addressedToMe(%q) != %v", in, want)
		}
	}
}

func TestSessionJSON(t *testing.T) {
	b, _ := json.Marshal(&Session{ID: "x", Handoff: true, HandoffTo: "sam", HandoffNote: "n"})
	s := string(b)
	if !strings.Contains(s, `"handoff_to"`) || !strings.Contains(s, `"handoff_note"`) || !strings.Contains(s, `"handoff"`) || !strings.Contains(s, `"state"`) {
		t.Errorf("json: %s", s)
	}
	b, _ = json.Marshal(&Session{ID: "x"})
	if strings.Contains(string(b), "handoff_to") || strings.Contains(string(b), "handoff_note") {
		t.Errorf("omitempty: %s", b)
	}
}

func TestBuildIndexReadsMarker(t *testing.T) {
	v := tempVault(t, fixtureUsers)
	sid := "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f"
	os.WriteFile(filepath.Join(v.SessionsDir, sid+".jsonl"), []byte(`{"type":"user","cwd":"/Users/alice/v","message":{"role":"user","content":"hi"}}`+"\n"), 0o644)
	v.writeMarker(sid, Marker{User: "alex", Host: "h", ReleasedAt: "2026-09-24T00:00:00Z", To: "sam", Note: "n"})
	rows := v.sessions()
	if len(rows) != 1 || !rows[0].Handoff || rows[0].HandoffTo != "sam" || rows[0].HandoffBy != "alex" || rows[0].State != "handed off" {
		t.Errorf("rows: %+v", rows)
	}
}

func TestArgsAreInteractive(t *testing.T) {
	if argsAreInteractive([]string{"-p", "x"}) || argsAreInteractive([]string{"--print"}) || !argsAreInteractive([]string{"--resume", "id"}) {
		t.Error("argsAreInteractive")
	}
}

func TestReorientPromptNote(t *testing.T) {
	v := tempVault(t, fixtureUsers)
	v.Name = "team"
	base := v.reorientPrompt(nil)
	if base != v.reorientBase() {
		t.Error("nil note must leave the prompt unchanged")
	}
	with := v.reorientPrompt(&Marker{User: "alex", Note: "check totals"})
	if !strings.Contains(with, `left this handoff note for the member now driving: "check totals"`) || strings.Contains(with, "request") {
		t.Errorf("note sentence wrong: %s", with)
	}
}

func TestSelfTestArgs(t *testing.T) {
	a := selfTestArgs("N")
	want := []string{"-p", "--setting-sources", "user", "--strict-mcp-config", "vault join self-test N — reply OK"}
	if strings.Join(a, "|") != strings.Join(want, "|") {
		t.Errorf("selfTestArgs = %v", a)
	}
}

func TestTakeNoteFlags(t *testing.T) {
	o, rest, err := takeNoteFlags([]string{"--note", "x", "heron"})
	if err != nil || o.Note != "x" || strings.Join(rest, ",") != "heron" {
		t.Errorf("1: %+v %v %v", o, rest, err)
	}
	o, rest, _ = takeNoteFlags([]string{"heron", "--for", "bob", "-p", "hi"})
	if o.To != "bob" || strings.Join(rest, ",") != "heron,-p,hi" {
		t.Errorf("2: %+v %v", o, rest)
	}
	o, rest, _ = takeNoteFlags([]string{"--for=bob-mac", "--note=y z", "heron", "--now"})
	if o.To != "bob-mac" || o.Note != "y z" || strings.Join(rest, ",") != "heron,--now" {
		t.Errorf("3: %+v %v", o, rest)
	}
	if _, _, err := takeNoteFlags([]string{"heron", "--for"}); err == nil {
		t.Error("dangling --for should error")
	}
	o, rest, _ = takeNoteFlags([]string{"-p", "hi"})
	if o.To != "" || o.Note != "" || strings.Join(rest, ",") != "-p,hi" {
		t.Errorf("5: %+v %v", o, rest)
	}
}

func TestMarkerConflictSweep(t *testing.T) {
	v := tempVault(t, fixtureUsers)
	sid := "7b63838e-25e4-4780-89ef-b8d0cc7a8f4f"
	h := filepath.Join(v.Dir, "handoff")
	for _, n := range []string{sid + ".done", sid + "-Bob’s MacBook Pro.done", "." + sid + ".123"} {
		os.WriteFile(filepath.Join(h, n), []byte("{}"), 0o644)
	}
	v.quarantineConflicts()
	if !fileExists(filepath.Join(h, sid+".done")) || !fileExists(filepath.Join(h, "."+sid+".123")) {
		t.Error("legit marker or temp file was moved")
	}
	if fileExists(filepath.Join(h, sid+"-Bob’s MacBook Pro.done")) || !fileExists(filepath.Join(v.Dir, "conflicts", sid+"-Bob’s MacBook Pro.done")) {
		t.Error("conflict marker not moved")
	}
	if v.conflictCount() != 0 {
		t.Error("conflictCount must count transcripts only")
	}
}
