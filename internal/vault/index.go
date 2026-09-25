package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is one row of the vault's session list.
type Session struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	LastBy      string `json:"last_by"`
	FirstPrompt string `json:"first_prompt"`
	Size        int64  `json:"size"`
	Mtime       int64  `json:"mtime"`
	Torn        bool   `json:"torn"`
	Handoff     bool   `json:"handoff"`
	State       string `json:"state"`
	HandoffBy   string `json:"handoff_by,omitempty"`
	HandoffTo   string `json:"handoff_to,omitempty"`
	HandoffNote string `json:"handoff_note,omitempty"`
	HandoffAt   string `json:"handoff_at,omitempty"`
}

type indexEntry struct {
	Owner       string `json:"owner"`
	LastBy      string `json:"last_by"`
	Name        string `json:"name"`
	FirstPrompt string `json:"first_prompt"`
	Size        int64  `json:"size"`
	Mtime       int64  `json:"mtime"`
}

type indexFile struct {
	Sessions map[string]indexEntry `json:"sessions"`
}

func (s *Session) Label() string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID[:8]
}

// buildIndex lists sessions in dir, opening only transcripts whose size/mtime
// changed since the cached entry (opening a cloud-only file downloads it all).
func (v *Vault) buildIndex(dir string) []*Session {
	idxPath := filepath.Join(v.Dir, "index.json")
	if dir != v.SessionsDir {
		idxPath = filepath.Join(dir, "index.json")
	}
	var idx indexFile
	if b, err := os.ReadFile(idxPath); err == nil {
		json.Unmarshal(b, &idx)
	}
	if idx.Sessions == nil {
		idx.Sessions = map[string]indexEntry{}
	}
	newIdx := indexFile{Sessions: map[string]indexEntry{}}
	var rows []*Session
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Strings(matches)
	for _, path := range matches {
		sid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !uuidRe.MatchString(sid) {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		mt := st.ModTime().Unix()
		e, ok := idx.Sessions[sid]
		if !ok || e.Size != st.Size() || e.Mtime != mt {
			r := scanTranscript(path)
			e = indexEntry{Owner: v.userFor(r.FirstCwd), LastBy: v.userFor(r.LastCwd), Name: r.Title,
				FirstPrompt: r.FirstPrompt, Size: st.Size(), Mtime: mt}
		}
		newIdx.Sessions[sid] = e
		markerPath := v.markerPath(sid)
		if dir != v.SessionsDir { // archive/: markers travel next to the transcript
			markerPath = filepath.Join(dir, sid+".done")
		}
		row := &Session{ID: sid, Name: e.Name, Owner: e.Owner, LastBy: e.LastBy, FirstPrompt: e.FirstPrompt,
			Size: e.Size, Mtime: e.Mtime, Torn: !tailValid(path), Handoff: fileExists(markerPath)}
		if row.Handoff {
			if m := readMarkerFile(markerPath); m != nil {
				row.HandoffBy, row.HandoffTo, row.HandoffNote, row.HandoffAt = m.User, m.To, m.Note, m.ReleasedAt
			}
		}
		rows = append(rows, row)
	}
	if b, err := json.MarshalIndent(newIdx, "", " "); err == nil {
		os.WriteFile(idxPath, b, 0o644)
	}
	leases := v.activeLeases()
	for _, s := range rows {
		switch l, held := leases[s.ID]; {
		case held && l.User == v.self.User && l.Host == v.self.Host:
			s.State = "in use (you)"
		case held:
			s.State = "in use by " + l.User
		case s.Torn:
			s.State = "syncing"
		case s.Handoff:
			s.State = "handed off"
		default:
			s.State = "unknown"
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Mtime > rows[j].Mtime })
	return rows
}

func (v *Vault) sessions() []*Session { return v.buildIndex(v.SessionsDir) }

func (v *Vault) sessionByID(id string) *Session {
	for _, s := range v.sessions() {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (v *Vault) nameTaken(name string) (string, bool) {
	for _, s := range v.sessions() {
		if s.Name != "" && strings.EqualFold(s.Name, name) {
			return s.ID, true
		}
	}
	return "", false
}

// resolve turns a name, case-insensitive name prefix, id prefix (>=4 chars)
// or full id into a session id.
func resolve(arg string, rows []*Session) (string, error) {
	if uuidRe.MatchString(arg) {
		for _, s := range rows {
			if strings.EqualFold(s.ID, arg) {
				return s.ID, nil
			}
		}
		return "", fmt.Errorf("no session with id %s in this vault (not synced yet?)", arg)
	}
	var exact, ci, idpre, npre []*Session
	for _, s := range rows {
		switch {
		case s.Name == arg:
			exact = append(exact, s)
		case strings.EqualFold(s.Name, arg):
			ci = append(ci, s)
		}
		if len(arg) >= 4 && strings.HasPrefix(s.ID, strings.ToLower(arg)) {
			idpre = append(idpre, s)
		}
		if s.Name != "" && strings.HasPrefix(strings.ToLower(s.Name), strings.ToLower(arg)) {
			npre = append(npre, s)
		}
	}
	if len(exact) == 1 {
		return exact[0].ID, nil
	}
	if len(exact) > 1 {
		var lines []string
		for _, s := range exact {
			lines = append(lines, fmt.Sprintf("%s  %s  %s", s.ID, s.Owner, relTime(s.Mtime)))
		}
		return "", fmt.Errorf("several sessions share the name '%s':\n  %s\nuse the ID instead, and `vault rename` one of them.", arg, strings.Join(lines, "\n  "))
	}
	if len(ci) == 1 {
		return ci[0].ID, nil
	}
	if len(idpre) == 1 {
		return idpre[0].ID, nil
	}
	if len(idpre) > 1 {
		return "", fmt.Errorf("'%s' matches %d session ids — give more characters", arg, len(idpre))
	}
	if len(npre) == 1 {
		return npre[0].ID, nil
	}
	if len(npre) > 1 {
		var names []string
		for _, s := range npre {
			names = append(names, s.Name)
		}
		return "", fmt.Errorf("'%s' matches several names: %s", arg, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("no session named '%s' in this vault. Run `vault sessions` to see what exists.", arg)
}

func relTime(ts int64) string {
	s := time.Now().Unix() - ts
	switch {
	case s < 60:
		return "just now"
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh ago", s/3600)
	case s < 7*86400:
		return fmt.Sprintf("%dd ago", s/86400)
	}
	return time.Unix(ts, 0).Format("2006-01-02")
}
