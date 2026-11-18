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

// How the window waits for its server before it reopens: it asks every
// serverPollEvery; for a newer version until serverNewTimeout, then for any
// answer until serverBackTimeout, when it reopens anyway.
var (
	serverPollEvery   = time.Second
	serverNewTimeout  = 2 * time.Minute
	serverBackTimeout = 5 * time.Minute
	// startWait bounds the wait of a window that starts with no server.
	startWait = 60 * time.Second
)

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
	before := b.serverVersion(ctx)
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
		b.log.Info("window: a new version is installed; waiting for the server", "program", exe)
		b.waitForServer(ctx, before)
		b.log.Info("window: reopening with the new version", "program", exe)
		if err := relaunch(exe, b.visible()); err != nil {
			b.log.Warn("window: could not reopen with the new version", "err", err)
			return
		}
		b.app.Quit()
		return
	}
}

// waitForServer waits until the server answers with a version other than
// before. A package replaces this program before it restarts the server, so
// a window reopened at once finds no server and shows only its dark
// background, for good: the page never loaded, and nothing loads it again.
// When the server's version stays the same (only the window changed), the
// window reopens once the server answers, after serverNewTimeout.
func (b *bridge) waitForServer(ctx context.Context, before string) {
	start := time.Now()
	for {
		v := b.serverVersion(ctx)
		waited := time.Since(start)
		switch {
		case v != "" && v != before,
			v != "" && waited >= serverNewTimeout,
			waited >= serverBackTimeout:
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(serverPollEvery):
		}
	}
}

// waitForAnswer waits up to d for the server to answer: the window opens
// on a page that loads. An older window reopens this one as soon as its
// program changes, before the package has restarted the server.
func (b *bridge) waitForAnswer(ctx context.Context, d time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	for b.serverVersion(ctx) == "" {
		select {
		case <-ctx.Done():
			b.log.Warn("window: the server does not answer; opening anyway", "url", b.base.String())
			return
		case <-time.After(serverPollEvery):
		}
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
	// The new window logs where this one does (the journal, under the
	// launcher entry).
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
