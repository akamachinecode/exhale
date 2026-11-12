//go:build desktop && !windows

package window

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a session of its own, so it outlives this process.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
