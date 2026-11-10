package awake

import (
	"os"
	"os/exec"
	"strconv"
)

const caffeinate = "/usr/bin/caffeinate"

// newKeeper runs caffeinate: -i keeps the system from idle sleep, -d the
// display too, and -w ties it to Exhale's process so it exits with it.
func newKeeper() Keeper {
	return &procKeeper{
		available: func() error {
			_, err := os.Stat(caffeinate)
			return err
		},
		command: func(display bool) (string, *exec.Cmd) {
			args := []string{"-i"}
			if display {
				args = append(args, "-d")
			}
			args = append(args, "-w", strconv.Itoa(os.Getpid()))
			return "caffeinate", exec.Command(caffeinate, args...)
		},
	}
}
