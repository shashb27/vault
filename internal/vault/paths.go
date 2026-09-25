package vault

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Version is set at build time with -ldflags "-X .../vault.Version=x.y.z".
var Version = "0.4.0-dev"

const (
	vaultDirName   = ".vault"
	leaseTTLMin    = 60
	uploadWaitSecs = 45
	gateWaitSecs   = 90  // how long `resume` waits for sync before asking
	idleOKSecs     = 900 // marker-less session idle this long counts as finished
	repoSlug       = "shashb27/vault"
)

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return "."
}

func claudeDir() string    { return filepath.Join(homeDir(), ".claude") }
func projectsDir() string  { return filepath.Join(claudeDir(), "projects") }
func stateDir() string     { return filepath.Join(claudeDir(), "vault-local-state") } // per-user, never synced
func backupDir() string    { return filepath.Join(claudeDir(), "vault-backups") }     // per-user, never synced
func registryPath() string { return filepath.Join(stateDir(), "vaults.list") }
func privateScratch(vaultRoot string) string {
	return filepath.Join(claudeDir(), "vault-private", filepath.Base(vaultRoot))
}

// EncodePath replicates Claude Code's project-directory encoding (verified
// against 2.1.170 and 2.1.278 on macOS): every UTF-16 code unit outside
// [A-Za-z0-9] becomes '-'; results over 200 units are truncated to 200 plus
// '-' plus base36(abs(java String.hashCode of the full path)).
func EncodePath(p string) string {
	units := utf16.Encode([]rune(p))
	var b strings.Builder
	for _, u := range units {
		if (u >= '0' && u <= '9') || (u >= 'A' && u <= 'Z') || (u >= 'a' && u <= 'z') {
			b.WriteRune(rune(u))
		} else {
			b.WriteByte('-')
		}
	}
	enc := b.String()
	if len(enc) > 200 {
		var h int32
		for _, u := range units {
			h = h*31 + int32(u)
		}
		if h < 0 {
			h = -h
		}
		enc = enc[:200] + "-" + base36(uint64(h))
	}
	return enc
}

func base36(n uint64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{digits[n%36]}, out...)
		n /= 36
	}
	return string(out)
}

// realpath resolves symlinks the way Claude does before encoding the cwd.
func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		if abs, err := filepath.Abs(r); err == nil {
			return abs
		}
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if caseInsensitiveFS {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func underPath(child, parent string) bool {
	child, parent = filepath.Clean(child), filepath.Clean(parent)
	if samePath(child, parent) {
		return true
	}
	prefix := parent + string(filepath.Separator)
	if caseInsensitiveFS {
		return strings.HasPrefix(strings.ToLower(child), strings.ToLower(prefix))
	}
	return strings.HasPrefix(child, prefix)
}

// underPathAny is underPath but also tolerant of the other OS's separator, for
// cwd values written by a teammate on a different platform.
func underPathAny(child, parent string) bool {
	if underPath(child, parent) {
		return true
	}
	n := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "\\", "/")) }
	c, p := n(child), n(parent)
	return c == p || strings.HasPrefix(c, p+"/")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%dK", n/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1048576)
	}
}
