package vault

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

// Everything that parses Claude Code's transcript format lives in this file.
// The format is internal to Claude Code and may change between releases.

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$`)
var wsRe = regexp.MustCompile(`\s+`)

type record struct {
	Type             string          `json:"type"`
	Cwd              string          `json:"cwd"`
	CustomTitle      string          `json:"customTitle"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          json.RawMessage `json:"message"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

// textOf returns the first text of a message: a plain string, or the first
// {"type":"text"} block of a content list.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m message
	if json.Unmarshal(raw, &m) != nil || len(m.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" {
				return b.Text
			}
		}
	}
	return ""
}

type scanResult struct {
	FirstCwd, LastCwd, Title, FirstPrompt string
}

// scanTranscript reads the whole file once (only called when size/mtime changed).
func scanTranscript(path string) scanResult {
	var r scanResult
	f, err := os.Open(path)
	if err != nil {
		return r
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var d record
		if json.Unmarshal(sc.Bytes(), &d) != nil {
			continue
		}
		if d.Cwd != "" {
			if r.FirstCwd == "" {
				r.FirstCwd = d.Cwd
			}
			r.LastCwd = d.Cwd
		}
		switch d.Type {
		case "custom-title":
			if d.CustomTitle != "" {
				r.Title = d.CustomTitle
			}
		case "user":
			if r.FirstPrompt == "" && !d.IsMeta {
				t := strings.TrimSpace(textOf(d.Message))
				if t != "" && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "Caveat:") {
					t = wsRe.ReplaceAllString(t, " ")
					if len(t) > 60 {
						t = t[:60]
					}
					r.FirstPrompt = t
				}
			}
		}
	}
	return r
}

// tailValid: OneDrive can deliver a snapshot cut mid-record. The last
// non-empty line must parse as JSON.
func tailValid(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	off := st.Size() - 65536
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) == 0 {
			continue
		}
		return json.Valid(lines[i])
	}
	return false
}

// lastCwd returns the cwd of the most recent record that has one.
func lastCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - 262144
	if off < 0 {
		off = 0
	}
	f.Seek(off, io.SeekStart)
	data, _ := io.ReadAll(f)
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var d record
		if json.Unmarshal(lines[i], &d) == nil && d.Cwd != "" {
			return d.Cwd
		}
	}
	return ""
}

// appendTitle renames a session: Claude honours the LAST custom-title record.
func appendTitle(path, sid, name string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": name, "sessionId": sid})
	_, err = f.Write(append(b, '\n'))
	return err
}

// Turn is one human/assistant exchange rendered as text.
type Turn struct {
	Who  string // "user", "assistant", "compact"
	Text string
}

func turns(path string, lastN int) []Turn {
	var out []Turn
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var d record
		if json.Unmarshal(sc.Bytes(), &d) != nil || (d.Type != "user" && d.Type != "assistant") || d.IsMeta {
			continue
		}
		t := strings.TrimSpace(textOf(d.Message))
		if d.IsCompactSummary {
			out = append(out, Turn{"compact", t})
			continue
		}
		if t == "" || strings.HasPrefix(t, "<") {
			continue
		}
		out = append(out, Turn{d.Type, t})
	}
	if lastN > 0 && len(out) > lastN {
		out = out[len(out)-lastN:]
	}
	return out
}

// Secrets that people most often paste into terminals. Warn-only.
var secretPatterns = []struct {
	Name string
	Re   *regexp.Regexp
}{
	{"AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"OpenAI-style key", regexp.MustCompile(`\bsk-[A-Za-z0-9]{32,}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(ghp|gho|ghs|ghr)_[A-Za-z0-9]{36}\b|\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"Slack token", regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9-]{10,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"Azure/Microsoft client secret (pattern)", regexp.MustCompile(`\b[A-Za-z0-9~._-]{3}8Q~[A-Za-z0-9~._-]{30,}`)},
}

// scanSecrets checks bytes [from, end) of a transcript and returns the kinds found.
func scanSecrets(path string, from int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if from > 0 {
		f.Seek(from, io.SeekStart)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	var found []string
	for _, p := range secretPatterns {
		if p.Re.Match(data) {
			found = append(found, p.Name)
		}
	}
	return found
}
