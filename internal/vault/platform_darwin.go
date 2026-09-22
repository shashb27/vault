//go:build darwin

package vault

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const platformName = "macOS"
const caseInsensitiveFS = true // default APFS is case-insensitive; Claude compares the string it was given
const claudeBinary = "claude"

// SyncState is what the cloud provider tells us about a path (best effort).
type SyncState struct {
	Known       bool
	Uploading   bool
	Downloaded  bool
	Paused      bool
	Pinned      bool
	CloudOnly   bool
	UploadKnown bool // whether "Uploading" is a real signal on this platform
	Raw         string
}

func isCloudPath(p string) bool {
	return strings.HasPrefix(realpath(p), filepath.Join(homeDir(), "Library", "CloudStorage")+"/")
}

// syncState uses fileproviderctl, an undocumented Apple debug tool. Offline it
// reports false-green.
func syncState(p string) SyncState {
	st := SyncState{UploadKnown: true}
	if !isCloudPath(p) {
		return st
	}
	out, err := exec.Command("fileproviderctl", "evaluate", p).CombinedOutput()
	if err != nil && len(out) == 0 {
		return st
	}
	var keep []string
	for _, ln := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "isUploading = 1"):
			st.Uploading = true
		case strings.HasPrefix(t, "isMostRecentVersionDownloaded = 1"), strings.HasPrefix(t, "isDownloaded = 1"):
			st.Downloaded = true
		case strings.HasPrefix(t, "isSyncPaused = 1"):
			st.Paused = true
		case strings.HasPrefix(t, "isKeepDownloaded = 1"):
			st.Pinned = true
		}
		if strings.Contains(t, "isUploaded") || strings.Contains(t, "isUploading") || strings.Contains(t, "Downloaded") ||
			strings.Contains(t, "isSyncPaused") || strings.Contains(t, "isKeepDownloaded") {
			keep = append(keep, t)
			st.Known = true
		}
	}
	st.Raw = strings.Join(keep, "\n")
	return st
}

func cloudRunning() (running bool, known bool) {
	err := exec.Command("pgrep", "-x", "OneDrive").Run()
	return err == nil, true
}

var errManualPin = errors.New("macOS has no scriptable pin; use Finder → Always Keep on This Device")

func pinFolder(string) error { return errManualPin }

func makeLink(target, link string) error { return os.Symlink(target, link) }

// readLink returns the link target and whether the path is a link at all.
func readLink(link string) (string, bool) {
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	t, err := os.Readlink(link)
	if err != nil {
		return "", true
	}
	return t, true
}

func enableColor() {}

func cloudSetupHint(vaultDir string) string {
	return "in Finder, right-click the '" + filepath.Base(vaultDir) + "' folder here and choose Always Keep on This Device (Cmd+Shift+. shows hidden folders)"
}

func oneDriveHint() string { return "~/Library/CloudStorage/OneDrive-<YourOrg>/" }
