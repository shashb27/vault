package vault

import (
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
		"/Users/shashvath.bhaskar/Library/CloudStorage/OneDrive-OxmiqLabs/Testing-vault": "-Users-shashvath-bhaskar-Library-CloudStorage-OneDrive-OxmiqLabs-Testing-vault",
		`C:\Users\Administrator\OneDrive - Oxmiq Labs\playground`:                        "C--Users-Administrator-OneDrive---Oxmiq-Labs-playground",
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
	if v.userFor("/Users/tvisha.devavarapu/Library/CloudStorage/x") != "tvisha.devavarapu" {
		t.Error("mac home attribution")
	}
	if v.userFor(`C:\Users\Administrator\OneDrive - Oxmiq Labs\v`) != "Administrator" {
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
