//go:build !windows

package update

import (
	"errors"
	"os"
	"syscall"
)

// execSelf runs the new binary at exe in this process's place: same
// process, same arguments, so launchd, systemd or the terminal that started
// Exhale keep following it.
func execSelf(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ()) //nolint:gosec // the release we just checked, at our own path
}

func startSetup(string) error { return errors.New("update: no installer on this system") }
