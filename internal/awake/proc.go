//go:build darwin || linux

package awake

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// startupGrace is how long a helper must stay up for its start to count:
// a tool that refuses (no permission, no session) exits at once.
const startupGrace = 500 * time.Millisecond

// proc is a helper process that holds the machine awake while it runs.
type proc struct {
	name     string
	cmd      *exec.Cmd
	stderr   bytes.Buffer
	done     chan struct{}
	waitErr  error
	released bool
}

// startProc starts cmd and waits startupGrace to see that it stays up.
func startProc(name string, cmd *exec.Cmd) (*proc, error) {
	p := &proc{name: name, cmd: cmd, done: make(chan struct{})}
	cmd.Stderr = &p.stderr
	started := make(chan error, 1)
	go func() {
		// On Linux the child gets SIGTERM when the thread that started it
		// ends (Pdeathsig), not the process: keep this thread for as long
		// as the child lives. Returning while locked ends the thread.
		runtime.LockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	if err := <-started; err != nil {
		return nil, fmt.Errorf("%s could not start: %w", name, err)
	}
	select {
	case <-p.done:
		return nil, p.exitError()
	case <-time.After(startupGrace):
		return p, nil
	}
}

// exitError describes why the helper exited; only call it after done.
func (p *proc) exitError() error {
	msg := strings.TrimSpace(p.stderr.String())
	if msg == "" && p.waitErr != nil {
		msg = p.waitErr.Error()
	}
	if msg == "" {
		msg = "it exited"
	}
	return fmt.Errorf("%s: %s", p.name, msg)
}

// lost reports why the helper exited on its own, or nil while it runs.
func (p *proc) lost() error {
	if p == nil || p.released {
		return nil
	}
	select {
	case <-p.done:
		return p.exitError()
	default:
		return nil
	}
}

// stop ends the helper: SIGTERM, then SIGKILL if it does not exit soon.
func (p *proc) stop() {
	if p == nil || p.released {
		return
	}
	p.released = true
	select {
	case <-p.done:
		return
	default:
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// procKeeper is a Keeper whose hold is a helper process.
type procKeeper struct {
	available func() error
	command   func(display bool) (name string, cmd *exec.Cmd)
	p         *proc
}

func (k *procKeeper) Available() error { return k.available() }

func (k *procKeeper) Hold(display bool) error {
	k.Release()
	if err := k.available(); err != nil {
		return err
	}
	p, err := startProc(k.command(display))
	if err != nil {
		return err
	}
	k.p = p
	return nil
}

func (k *procKeeper) Release() {
	k.p.stop()
	k.p = nil
}

func (k *procKeeper) Lost() error { return k.p.lost() }
