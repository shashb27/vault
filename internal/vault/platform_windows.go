//go:build windows

package vault

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const platformName = "Windows"
const caseInsensitiveFS = true
const claudeBinary = "claude" // exec.LookPath resolves claude.exe / claude.cmd via PATHEXT

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

// OneDrive attribute bits (winnt.h)
const (
	attrReparsePoint       = 0x400
	attrOffline            = 0x1000
	attrPinned             = 0x80000
	attrUnpinned           = 0x100000
	attrRecallOnDataAccess = 0x400000
)

// cloudRoots: Windows sets these for every signed-in OneDrive account.
func cloudRoots() []string {
	var roots []string
	for _, k := range []string{"OneDriveCommercial", "OneDrive", "OneDriveConsumer"} {
		if v := os.Getenv(k); v != "" {
			roots = append(roots, filepath.Clean(v))
		}
	}
	return roots
}

func isCloudPath(p string) bool {
	rp := realpath(p)
	for _, r := range cloudRoots() {
		if underPath(rp, r) {
			return true
		}
	}
	return false
}

func fileAttributes(p string) (uint32, error) {
	u, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return 0, err
	}
	return syscall.GetFileAttributes(u)
}

// syncState reads OneDrive's file attributes. There is no "uploading" signal
// on Windows, so UploadKnown is false and callers fall back to a short grace.
func syncState(p string) SyncState {
	st := SyncState{UploadKnown: false}
	if !isCloudPath(p) {
		return st
	}
	a, err := fileAttributes(p)
	if err != nil {
		return st
	}
	st.Known = true
	st.CloudOnly = a&attrRecallOnDataAccess != 0 || a&attrOffline != 0
	st.Downloaded = !st.CloudOnly
	st.Pinned = a&attrPinned != 0
	var parts []string
	if st.CloudOnly {
		parts = append(parts, "cloud-only")
	} else {
		parts = append(parts, "downloaded")
	}
	if st.Pinned {
		parts = append(parts, "pinned")
	}
	if a&attrUnpinned != 0 {
		parts = append(parts, "unpinned")
	}
	st.Raw = strings.Join(parts, ", ")
	return st
}

func cloudRunning() (bool, bool) {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq OneDrive.exe", "/NH").Output()
	if err != nil {
		return false, false
	}
	return strings.Contains(strings.ToLower(string(out)), "onedrive.exe"), true
}

// pinFolder marks the folder "Always keep on this device" (attrib +P, recursive).
func pinFolder(p string) error {
	return exec.Command("attrib", "+P", p, "/S", "/D").Run()
}

// makeLink creates a directory junction: works without admin or developer mode,
// and Claude Code writes through it like a normal directory.
func makeLink(target, link string) error {
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}

// readLink handles both junctions and symlinks.
func readLink(link string) (string, bool) {
	a, err := fileAttributes(link)
	if err != nil || a&attrReparsePoint == 0 {
		return "", false
	}
	t, err := os.Readlink(link)
	if err != nil {
		return "", true
	}
	t = strings.TrimPrefix(t, `\\?\`)
	t = strings.TrimPrefix(t, `\??\`)
	return t, true
}

// enableColor turns on ANSI escape processing in the console (Windows 10+).
func enableColor() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	getMode := k32.NewProc("GetConsoleMode")
	setMode := k32.NewProc("SetConsoleMode")
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	r, _, _ := getMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return
	}
	const enableVT = 0x0004
	setMode.Call(uintptr(h), uintptr(mode|enableVT))
}

func cloudSetupHint(vaultDir string) string {
	return "vault pins '" + filepath.Base(vaultDir) + "' with attrib +P so OneDrive keeps it on this device"
}

func oneDriveHint() string {
	if r := cloudRoots(); len(r) > 0 {
		return r[0]
	}
	return `C:\Users\<you>\OneDrive - <Your Org>\`
}
