//go:build linux

package vault

import (
	"errors"
	"os"
)

const platformName = "Linux"
const caseInsensitiveFS = false
const claudeBinary = "claude"

type SyncState struct {
	Known       bool
	Uploading   bool
	Downloaded  bool
	Paused      bool
	Pinned      bool
	CloudOnly   bool
	UploadKnown bool
	Raw         string
}

// No first-party OneDrive client on Linux; sync tools vary, so we can't tell.
func isCloudPath(string) bool            { return false }
func syncState(string) SyncState         { return SyncState{} }
func cloudRunning() (bool, bool)         { return false, false }
func pinFolder(string) error             { return errors.New("no OneDrive client on Linux to pin with") }
func makeLink(target, link string) error { return os.Symlink(target, link) }
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
func cloudSetupHint(string) string {
	return "make sure whatever syncs this folder keeps it fully downloaded"
}

func oneDriveHint() string {
	return "wherever your sync client puts them (no first-party OneDrive on Linux)"
}
