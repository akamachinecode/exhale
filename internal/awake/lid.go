package awake

import (
	"context"
	"errors"
)

// Keeping the computer awake with its lid closed (ADR 0032). The ordinary
// hold (Keeper) only stops idle sleep: every system still sleeps when the
// lid closes. Each one has another switch for that, with its own cost:
//
//   - macOS: `pmset disablesleep 1`, which needs root. A sudoers rule set up
//     once (Setup, with the administrator's password) allows exactly that
//     command and its undo, nothing else.
//   - Linux: a systemd-inhibit lock on handle-lid-switch, which dies with
//     Exhale like the idle lock does.
//   - Windows: the power plan's "when I close the lid" set to do nothing,
//     put back as it was on release.
//
// The macOS and Windows switches outlive Exhale, so a marker file is
// written before they are turned on and removed once they are turned off:
// Recover, at the next start, puts back what a crash left behind.

// ErrNeedsSetup is returned by Lid.Available on a Mac whose administrator
// has not allowed Exhale to turn lid sleep off yet (Lid.Setup).
var ErrNeedsSetup = errors.New("keeping a closed laptop awake needs the administrator's permission once")

// Lid keeps the computer awake with its lid closed. Its methods are safe
// for one caller at a time (the controller in internal/app serializes them).
type Lid interface {
	// Available reports why the lid cannot be held: nil when it can,
	// ErrNeedsSetup, ErrUnsupported, ErrContainer or another error.
	Available() error
	// Setup asks for the permission Available lacks (macOS: the
	// administrator's password, in a dialog on this computer).
	Setup(ctx context.Context) error
	// Hold keeps the computer awake when the lid closes, until Release.
	Hold() error
	// Release lets the lid put the computer to sleep again and, when the
	// lid is already closed, puts it to sleep now: the system does not look
	// at the lid again by itself, so a closed laptop would stay awake in a
	// bag until its battery runs out.
	Release() error
	// Recover undoes a hold that a previous run left in place (a crash, a
	// forced quit). It does nothing when there is none.
	Recover() error
}

// NewLid returns the lid switch of this operating system. dir is where its
// marker is kept: Exhale's data directory.
func NewLid(dir string) Lid { return newLid(dir) }

// Power is the computer's power source, as the system reports it.
type Power struct {
	// Known is false when the system did not say (Read failed).
	Known bool `json:"known"`
	// HasBattery is false on a desktop: it never runs on battery.
	HasBattery bool `json:"hasBattery"`
	OnBattery  bool `json:"onBattery"`
	// Percent is the battery's charge, 0 to 100.
	Percent int `json:"percent"`
}

// ReadPower asks the system for its power source.
func ReadPower() Power { return readPower() }
