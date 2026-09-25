package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// cleanText drops control characters and escape sequences' lead bytes: notes are
// printed on every member's terminal and quoted into Claude's prompt, and markers
// are as editable as transcripts.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r == 0x1b || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar:
			return -1
		}
		return r
	}, s)
}

const toMaxRunes = 64

func capRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// A handoff marker (.vault/handoff/<sid>.done) says a session was exited cleanly.
// Since 0.4 it may also carry "for whom, what next": To and Note. Both optional,
// so old and new clients keep sharing a vault (schema 1 unchanged).

const noteMaxRunes = 280

type Marker struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	ReleasedAt string `json:"released_at"`
	To         string `json:"to,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (v *Vault) markerPath(sid string) string { return filepath.Join(v.Dir, "handoff", sid+".done") }

// readMarker returns nil when the marker is absent or not a JSON object.
// Callers that only need "was it handed off" keep using fileExists.
func (v *Vault) readMarker(sid string) *Marker { return readMarkerFile(v.markerPath(sid)) }

func readMarkerFile(path string) *Marker {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m Marker
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	m.User, m.Host = capRunes(cleanText(m.User), toMaxRunes), capRunes(cleanText(m.Host), toMaxRunes)
	m.To = capRunes(cleanText(m.To), toMaxRunes)
	m.Note = capRunes(strings.TrimSpace(wsRe.ReplaceAllString(cleanText(m.Note), " ")), noteMaxRunes)
	return &m
}

// writeMarker writes atomically (temp + rename). The temp name is a dotfile, so
// OneDrive's blocklist (.lock, ~$*, desktop.ini) and the conflict sweep ignore it.
func (v *Vault) writeMarker(sid string, m Marker) error {
	dir := filepath.Join(v.Dir, "handoff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.%d", sid, os.Getpid()))
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, v.markerPath(sid))
}

// parseNote splits "@sam check the totals" into (sam, "check the totals").
// Whitespace is collapsed, the note is capped at noteMaxRunes. Never errors.
func parseNote(line string) (to, note string) {
	line = strings.TrimSpace(cleanText(line))
	if strings.HasPrefix(line, "@") {
		rest := line[1:]
		if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
			to, line = rest[:i], rest[i:]
		} else {
			to, line = rest, ""
		}
	}
	note = capRunes(strings.TrimSpace(wsRe.ReplaceAllString(line, " ")), noteMaxRunes)
	return capRunes(to, toMaxRunes), note
}

func noteHasSecret(note string) bool {
	for _, p := range secretPatterns {
		if p.Re.MatchString(note) {
			return true
		}
	}
	return false
}

// memberList returns "user@host" per users.json row, in file order.
func (v *Vault) memberList() []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range v.users() {
		t := m.User + "@" + m.Host
		if !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	return out
}

// matchMember resolves an @token to a member token as spelled in users.json:
// case-insensitive exact match on user, host or user@host; else a prefix that
// points at exactly ONE member (hits grouped by member, so "ali" → alice even
// though it also prefixes "alice@alice-mac"). Otherwise ("", member list).
func (v *Vault) matchMember(tok string) (string, []string) {
	if tok == "" {
		return "", nil
	}
	members := v.users()
	// exact: host or login@host are specific; a bare login is only specific if that
	// login joined from ONE machine (two "Administrator" Windows boxes would both match)
	for _, m := range members {
		if strings.EqualFold(m.Host, tok) || strings.EqualFold(m.User+"@"+m.Host, tok) {
			return tok2spelled(m, tok), nil
		}
	}
	for _, m := range members {
		if strings.EqualFold(m.User, tok) {
			if v.loginHosts(m.User) == 1 {
				return m.User, nil
			}
			return "", v.memberList()
		}
	}
	// prefix: every hit is attributed to the login that owns it; unique owner → resolve
	low := strings.ToLower(tok)
	owner, token := "", ""
	ambiguous := false
	for _, m := range members {
		hit := ""
		switch {
		case strings.HasPrefix(strings.ToLower(m.User), low):
			hit = m.User
		case strings.HasPrefix(strings.ToLower(m.Host), low):
			hit = m.Host
		case strings.HasPrefix(strings.ToLower(m.User+"@"+m.Host), low):
			hit = m.User + "@" + m.Host
		}
		if hit == "" {
			continue
		}
		if owner != "" && !strings.EqualFold(owner, m.User) {
			ambiguous = true
		}
		if owner == "" || strings.EqualFold(hit, m.User) { // prefer the login token over a host token
			owner, token = m.User, hit
		}
	}
	if owner != "" && !ambiguous {
		if strings.EqualFold(token, owner) && v.loginHosts(owner) > 1 && !strings.EqualFold(token, tok) {
			// resolved to a bare login that is on several machines: fine for "Waiting for you"
			// (it shows on each of their machines), which is what addressing a person means
			return owner, nil
		}
		return token, nil
	}
	return "", v.memberList()
}

func tok2spelled(m member, tok string) string {
	if strings.EqualFold(m.Host, tok) {
		return m.Host
	}
	return m.User + "@" + m.Host
}

// loginHosts: how many distinct machines this login joined from.
func (v *Vault) loginHosts(login string) int {
	seen := map[string]bool{}
	for _, m := range v.users() {
		if strings.EqualFold(m.User, login) {
			seen[strings.ToLower(m.Host)] = true
		}
	}
	return len(seen)
}

// addressedToMe: a marker's To names this machine's login, host, or login@host.
func (v *Vault) addressedToMe(to string) bool {
	if to == "" {
		return false
	}
	return strings.EqualFold(to, v.self.User) || strings.EqualFold(to, v.self.Host) ||
		strings.EqualFold(to, v.self.User+"@"+v.self.Host)
}

func toOrAnyone(to string) string {
	if to == "" {
		return "the next person"
	}
	return to
}

// selfTestArgs: the join self-test must not run the vault's shared project
// settings (hooks, env, apiKeyHelper) or project MCP servers on a newcomer's
// first Claude call. --setting-sources user skips project/local settings;
// --strict-mcp-config with no --mcp-config connects no project servers.
// (--bare is not usable: it never reads OAuth/keychain, so subscription users
// would fail the self-test.) Verified on Claude Code 2.1.280, 2026-09-24.
func selfTestArgs(nonce string) []string {
	return []string{"-p", "--setting-sources", "user", "--strict-mcp-config", "vault join self-test " + nonce + " — reply OK"}
}

// argsAreInteractive: a Claude launch that is not a -p/--print run.
func argsAreInteractive(args []string) bool {
	for _, a := range args {
		if a == "-p" || a == "--print" || strings.HasPrefix(a, "-p=") || strings.HasPrefix(a, "--print=") {
			return false
		}
	}
	return true
}

// shouldAskNote: ask for a handoff note at exit only in an interactive run,
// with a terminal, unless the user opted out with VAULT_NO_NOTE.
func shouldAskNote(args []string) bool {
	return argsAreInteractive(args) && stdinIsTerminal() && os.Getenv("VAULT_NO_NOTE") == ""
}

// takeNoteFlags pulls --for <tok> / --note "<text>" out of args so they are
// consumed by vault and never passed to Claude. Everything else stays in order.
func takeNoteFlags(args []string) (launchOpts, []string, error) {
	var o launchOpts
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--for":
			if i+1 >= len(args) {
				return o, nil, fail("--for needs a login, host or login@host (see 'vault members')")
			}
			o.To = args[i+1]
			i++
		case strings.HasPrefix(a, "--for="):
			o.To = strings.TrimPrefix(a, "--for=")
		case a == "--note":
			if i+1 >= len(args) {
				return o, nil, fail("--note needs text")
			}
			o.Note = args[i+1]
			i++
		case strings.HasPrefix(a, "--note="):
			o.Note = strings.TrimPrefix(a, "--note=")
		default:
			rest = append(rest, a)
		}
	}
	return o, rest, nil
}

// resolveNoteFlags validates --for against the members and normalises --note.
func (v *Vault) resolveNoteFlags(o launchOpts) (launchOpts, error) {
	if o.To != "" {
		canon, cands := v.matchMember(o.To)
		if canon == "" {
			return o, fail("no member matches '%s'. Members: %s", o.To, strings.Join(cands, ", "))
		}
		o.To = canon
	}
	if o.Note != "" {
		_, o.Note = parseNote(strings.TrimPrefix(o.Note, "@")) // --note never carries an @who
	}
	if noteHasSecret(o.To + " " + o.Note) {
		return o, fail("that note looks like it contains a secret — not stored. Everyone in the vault can read markers.")
	}
	return o, nil
}
