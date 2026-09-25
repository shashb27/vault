package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "@") {
		rest := line[1:]
		if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
			to, line = rest[:i], rest[i:]
		} else {
			to, line = rest, ""
		}
	}
	note = strings.TrimSpace(wsRe.ReplaceAllString(line, " "))
	if r := []rune(note); len(r) > noteMaxRunes {
		note = string(r[:noteMaxRunes])
	}
	return to, note
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
	for _, m := range v.users() {
		out = append(out, m.User+"@"+m.Host)
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
	tokens := func(m member) []string { return []string{m.User, m.Host, m.User + "@" + m.Host} }
	for _, m := range members {
		for _, t := range tokens(m) {
			if strings.EqualFold(t, tok) {
				return t, nil
			}
		}
	}
	low := strings.ToLower(tok)
	hitMember, hitToken, hits := -1, "", 0
	for i, m := range members {
		first := ""
		for _, t := range tokens(m) {
			if strings.HasPrefix(strings.ToLower(t), low) {
				if first == "" {
					first = t
				}
			}
		}
		if first != "" {
			hits++
			hitMember, hitToken = i, first
		}
	}
	if hits == 1 && hitMember >= 0 {
		return hitToken, nil
	}
	return "", v.memberList()
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
		if noteHasSecret(o.Note) {
			return o, fail("that note looks like it contains a secret — not stored. Everyone in the vault can read markers.")
		}
	}
	return o, nil
}
