package awake

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// inContainer reports whether this process runs in a Docker or Podman
// container.
func inContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// errNoSystemd is reported when systemd-inhibit is not installed.
var errNoSystemd = errors.New("systemd-inhibit was not found: this Linux does not run systemd")

// newKeeper takes a systemd block lock on idle and sleep. The helper gets
// SIGTERM when Exhale exits (Pdeathsig), which drops the lock.
func newKeeper() Keeper {
	return &procKeeper{
		available: func() error {
			if inContainer() {
				return ErrContainer
			}
			if _, err := exec.LookPath("systemd-inhibit"); err != nil {
				return errNoSystemd
			}
			return nil
		},
		// The display belongs to the desktop (its screen saver), not to
		// logind: display makes no difference here.
		command: func(bool) (string, *exec.Cmd) {
			cmd := exec.Command("systemd-inhibit", "--what=idle:sleep", "--mode=block", "--who=Exhale",
				"--why=Sending reminders", "sleep", "infinity")
			cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
			return "systemd-inhibit", cmd
		},
	}
}
