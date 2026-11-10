// Package awake keeps the computer Exhale runs on from going to sleep, so
// that reminders go out on time and chat buttons keep answering (ADR 0028).
//
// Each operating system has its own way, none of them needs cgo:
//
//   - macOS runs /usr/bin/caffeinate tied to Exhale's process id;
//   - Windows calls SetThreadExecutionState from a thread kept for it;
//   - Linux runs systemd-inhibit with a block lock on idle and sleep.
//
// Elsewhere, and inside a container, it is not available.
package awake

import "errors"

// ErrUnsupported is returned by Available on an operating system Exhale
// cannot keep awake.
var ErrUnsupported = errors.New("keeping the computer awake is not supported on this operating system")

// ErrContainer is returned by Available when Exhale runs in a container:
// the machine that sleeps is the host, out of reach.
var ErrContainer = errors.New("running in a container, which cannot keep the host awake")

// Keeper holds the machine awake. Its methods are safe for one caller at a
// time (the controller in internal/app serializes them).
type Keeper interface {
	// Available reports why the machine cannot be kept awake here: nil when
	// it can, ErrContainer, ErrUnsupported or another error (a missing
	// tool).
	Available() error
	// Hold keeps the system awake, and the display too with display, until
	// Release. Calling it while holding replaces the previous hold.
	Hold(display bool) error
	// Release lets the machine sleep again. It does nothing when nothing is
	// held.
	Release()
	// Lost reports why a hold ended on its own (the helper process exited),
	// or nil while it is still in place or nothing is held.
	Lost() error
}

// New returns the keeper of this operating system.
func New() Keeper { return newKeeper() }
