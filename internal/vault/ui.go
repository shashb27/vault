package vault

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

var (
	colorOn                bool
	cB, cD, cG, cY, cR, cN string
)

func initUI() {
	colorOn = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""
	if colorOn {
		enableColor()
		cB, cD, cG, cY, cR, cN = "\x1b[1m", "\x1b[2m", "\x1b[32m", "\x1b[33m", "\x1b[31m", "\x1b[0m"
	}
}

func bold(s string) string { return cB + s + cN }
func dim(s string) string  { return cD + s + cN }
func grn(s string) string  { return cG + s + cN }
func yel(s string) string  { return cY + s + cN }
func red(s string) string  { return cR + s + cN }

func out(format string, a ...any)  { fmt.Fprintf(os.Stdout, format+"\n", a...) }
func ok(format string, a ...any)   { out(grn("✓")+" "+format, a...) }
func info(format string, a ...any) { out("  "+format, a...) }
func hint(format string, a ...any) { out(dim("  → next:")+" "+format, a...) }
func warn(format string, a ...any) { fmt.Fprintf(os.Stderr, yel("!")+" "+format+"\n", a...) }
func errln(format string, a ...any) {
	fmt.Fprintf(os.Stderr, red("✗")+" "+format+"\n", a...)
}

// userError is a message for the human, printed with ✗ and exit 1.
type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }
func fail(format string, a ...any) error {
	return &userError{fmt.Sprintf(format, a...)}
}

func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func readLine(prompt string) string {
	fmt.Fprint(os.Stdout, prompt)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s)
}

func pad(s string, w int) string {
	if n := visibleLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func lpad(s string, w int) string {
	if n := visibleLen(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

func visibleLen(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case esc:
			if r == 'm' {
				esc = false
			}
		case r == '\x1b':
			esc = true
		default:
			n++
		}
	}
	return n
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w])
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// printTable renders the session list the way v0.2 did.
func printTable(rows []*Session, numbered bool, limit int) {
	if len(rows) == 0 {
		out(dim("  (no sessions in this vault yet)"))
		return
	}
	shown := rows
	if limit > 0 && len(rows) > limit {
		shown = rows[:limit]
	}
	wName, wState, wOwn, wLast := 4, 5, 10, 7
	for _, s := range shown {
		lbl := s.Name
		if lbl == "" {
			lbl = "(unnamed) " + s.ID[:8]
		}
		wName = maxInt(wName, len([]rune(lbl)))
		wState = maxInt(wState, len(s.State))
		wOwn = maxInt(wOwn, len(s.Owner))
		wLast = maxInt(wLast, len(s.LastBy))
	}
	if wName > 32 {
		wName = 32
	}
	hdr := ""
	if numbered {
		hdr = "   # "
	}
	hdr += pad("NAME", wName) + "  " + pad("STATE", wState) + "  " + pad("STARTED-BY", wOwn) + "  " + pad("LAST-BY", wLast) + "  " + pad("LAST-ACTIVE", 11) + "  " + lpad("SIZE", 6) + "  ID"
	out(dim(hdr))
	var big []string
	for i, s := range shown {
		var name string
		if s.Name != "" {
			name = pad(truncate(s.Name, wName), wName)
		} else {
			name = dim(pad(truncate("(unnamed) "+s.ID[:8], wName), wName))
		}
		st := pad(s.State, wState)
		switch {
		case strings.HasPrefix(s.State, "in use"), s.State == "syncing":
			st = yel(st)
		case s.State == "handed off":
			st = grn(st)
		default:
			st = dim(st)
		}
		sz := lpad(humanSize(s.Size), 6)
		if s.Size > bigSessionBytes() {
			sz = yel(sz)
			big = append(big, s.Label())
		}
		line := ""
		if numbered {
			line = fmt.Sprintf("  %2d ", i+1)
		}
		line += name + "  " + st + "  " + pad(s.Owner, wOwn) + "  " + pad(s.LastBy, wLast) + "  " + pad(relTime(s.Mtime), 11) + "  " + sz + "  " + dim(s.ID[:8])
		out("%s", line)
		if s.Name == "" && s.FirstPrompt != "" {
			ind := "   "
			if numbered {
				ind = "        "
			}
			out(dim(fmt.Sprintf("%s\"%s\"", ind, s.FirstPrompt)))
		}
	}
	if limit > 0 && len(rows) > limit {
		out(dim(fmt.Sprintf("  … %d more — run `vault sessions` for all", len(rows)-limit)))
	}
	if len(big) > 0 {
		out(yel(fmt.Sprintf("  ! large session(s): %s — slow to sync and heavy to resume. Run /compact inside Claude before handing off.", strings.Join(big, ", "))))
	}
}
