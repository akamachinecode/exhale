//go:build desktop

package window

import "os/exec"

// detach is not needed on Windows: its installer reopens Exhale.
func detach(*exec.Cmd) {}
