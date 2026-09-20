//go:build windows

package vault

import (
	"os"
	"syscall"
)

// FindProcess succeeds on Windows only if the process exists; confirm with a
// handle query so a stale pid isn't mistaken for a live one.
func signalAlive(p *os.Process) bool {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(p.Pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
