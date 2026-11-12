//go:build desktop

package window

import (
	"os/exec"
	"strings"
)

func titlebarDoubleClick() titlebarAction {
	out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleActionOnDoubleClick").Output()
	if err != nil { // not set: the system default
		return titlebarZoom
	}
	return macTitlebarAction(strings.TrimSpace(string(out)))
}
