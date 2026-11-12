package awake

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// linuxLid takes a systemd-inhibit block lock on the lid switch: logind
// then ignores the lid while it is held. The lock dies with Exhale, so
// nothing has to be recovered.
type linuxLid struct{ p *proc }

func newLid(string) Lid { return &linuxLid{} }

func (l *linuxLid) Available() error {
	if inContainer() {
		return ErrContainer
	}
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return errNoSystemd
	}
	return nil
}

func (l *linuxLid) Setup(context.Context) error { return l.Available() }

func (l *linuxLid) Hold() error {
	_ = l.Release()
	cmd := exec.Command("systemd-inhibit", "--what=handle-lid-switch", "--mode=block", "--who=Exhale",
		"--why=Sending reminders with the lid closed", "sleep", "infinity")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	p, err := startProc("systemd-inhibit", cmd)
	if err != nil {
		return err
	}
	l.p = p
	return nil
}

// Release drops the lock. logind does not act on a lid that closed while
// it was held, so a lid still closed a moment later is suspended here,
// unless the machine already slept in between (the clock jumped).
func (l *linuxLid) Release() error {
	if l.p == nil {
		return nil
	}
	l.p.stop()
	l.p = nil
	if !lidClosed() {
		return nil
	}
	// The wall clock, not the monotonic one: only the wall clock counts the
	// time spent asleep.
	start := time.Now().Round(0)
	time.Sleep(3 * time.Second)
	if time.Now().Round(0).Sub(start) > 10*time.Second || !lidClosed() {
		return nil
	}
	return exec.Command("systemctl", "suspend").Run()
}

func (l *linuxLid) Recover() error { return nil }

// lidClosed reads ACPI's lid state, when the machine has one.
func lidClosed() bool {
	files, _ := filepath.Glob("/proc/acpi/button/lid/*/state")
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "closed") { //nolint:gosec // a path under /proc from Glob
			return true
		}
	}
	return false
}

// readPower reads /sys/class/power_supply: batteries of the system (not of
// a mouse, whose scope is Device) and mains adapters.
func readPower() Power {
	dirs, err := filepath.Glob("/sys/class/power_supply/*")
	if err != nil {
		return Power{}
	}
	read := func(dir, name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a path under /sys from Glob
		return strings.TrimSpace(string(b))
	}
	p := Power{Known: true}
	mains := false
	for _, d := range dirs {
		switch read(d, "type") {
		case "Mains":
			if read(d, "online") == "1" {
				mains = true
			}
		case "Battery":
			if read(d, "scope") == "Device" {
				continue
			}
			p.HasBattery = true
			if n, err := strconv.Atoi(read(d, "capacity")); err == nil {
				p.Percent = n
			}
			if read(d, "status") == "Discharging" {
				p.OnBattery = true
			}
		}
	}
	if mains {
		p.OnBattery = false
	}
	return p
}
