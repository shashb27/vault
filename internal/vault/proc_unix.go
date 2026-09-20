//go:build !windows

package vault

import (
	"os"
	"syscall"
)

func signalAlive(p *os.Process) bool { return p.Signal(syscall.Signal(0)) == nil }
