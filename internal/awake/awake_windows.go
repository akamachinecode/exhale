package awake

import (
	"fmt"
	"runtime"
	"syscall"
)

// Flags of SetThreadExecutionState (winbase.h).
const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// winKeeper calls SetThreadExecutionState. The state belongs to the thread
// that sets it, so a goroutine locked to its thread holds it until Release
// and clears it from that same thread. Windows drops it when Exhale exits.
type winKeeper struct {
	stop, done chan struct{}
}

func newKeeper() Keeper { return &winKeeper{} }

func (k *winKeeper) Available() error { return setThreadExecutionState.Find() }

func (k *winKeeper) Hold(display bool) error {
	k.Release()
	if err := k.Available(); err != nil {
		return err
	}
	flags := uintptr(esContinuous | esSystemRequired)
	if display {
		flags |= esDisplayRequired
	}
	started := make(chan error, 1)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		if r, _, err := setThreadExecutionState.Call(flags); r == 0 {
			started <- fmt.Errorf("SetThreadExecutionState: %w", err)
			return // the locked thread ends with this goroutine
		}
		started <- nil
		<-stop
		_, _, _ = setThreadExecutionState.Call(esContinuous)
		runtime.UnlockOSThread()
	}()
	if err := <-started; err != nil {
		<-done
		return err
	}
	k.stop, k.done = stop, done
	return nil
}

func (k *winKeeper) Release() {
	if k.stop == nil {
		return
	}
	close(k.stop)
	<-k.done
	k.stop, k.done = nil, nil
}

func (k *winKeeper) Lost() error { return nil }
