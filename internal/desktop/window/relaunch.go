//go:build desktop

package window

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/akamachinecode/exhale/internal/desktop"
)

// checkProgramEvery is how often the window looks at its program on disk.
const checkProgramEvery = 5 * time.Second

// followUpdates reopens the window and its icon when their program on disk
// is replaced: the server installed a new version (ADR 0033), or a package
// manager did. Without it the old window would stay until the next login.
// Windows needs none of this: its installer closes Exhale and opens the new
// one.
func (b *bridge) followUpdates(ctx context.Context) {
	if runtime.GOOS == "windows" || b.smoke != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	start, err := os.Stat(exe)
	if err != nil {
		return
	}
	tick := time.NewTicker(checkProgramEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now, err := os.Stat(exe)
		if err != nil || os.SameFile(start, now) {
			continue // being replaced, or unchanged
		}
		b.log.Info("window: a new version is installed; reopening", "program", exe)
		if err := relaunch(exe, b.visible()); err != nil {
			b.log.Warn("window: could not reopen with the new version", "err", err)
			return
		}
		b.app.Quit()
		return
	}
}

// visible reports whether the window is on screen, so it comes back the
// same way.
func (b *bridge) visible() bool { return b.win != nil && b.win.IsVisible() }

// relaunch starts exe again with this process's arguments once this
// process has exited: the single-instance lock would otherwise hand the
// new process over to this one.
func relaunch(exe string, visible bool) error {
	args := slices.DeleteFunc(slices.Clone(os.Args[1:]), func(a string) bool { return a == desktop.BackgroundFlag })
	if !visible {
		args = append(args, desktop.BackgroundFlag)
	}
	script := `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; shift; exec "$@"`
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, "sh", strconv.Itoa(os.Getpid()), exe}, args...)...) //nolint:gosec // our own program and arguments
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
