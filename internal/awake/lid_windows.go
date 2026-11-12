package awake

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// winLid sets the power plan's "when I close the lid" to do nothing, on
// power and on battery, and puts back what it was. The plan is the
// person's own, so no administrator is needed. Windows does not look at a
// lid that is already closed when the setting comes back, and Exhale has
// no way to read the lid: a laptop closed at that moment stays awake until
// it is opened, or the idle timeout sends it to sleep.
type winLid struct{ m marker }

func newLid(dir string) Lid { return &winLid{newMarker(dir)} }

type lidActions struct{ AC, DC int }

func (l *winLid) Available() error {
	_, err := exec.LookPath("powercfg")
	return err
}

func (l *winLid) Setup(context.Context) error { return l.Available() }

func (l *winLid) Hold() error {
	if _, ok := l.m.read(); !ok { // a hold in place keeps the values from before it
		out, err := powercfg("/query", "SCHEME_CURRENT", "SUB_BUTTONS", "LIDACTION")
		if err != nil {
			return err
		}
		ac, dc, err := parsePowercfgLid(out)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(lidActions{ac, dc})
		if err := l.m.write(b); err != nil {
			return err
		}
	}
	if err := setLidAction(lidActions{0, 0}); err != nil {
		_ = l.Release()
		return err
	}
	return nil
}

func (l *winLid) Release() error {
	b, ok := l.m.read()
	if !ok {
		return nil
	}
	var prev lidActions
	if json.Unmarshal(b, &prev) != nil {
		prev = lidActions{1, 1} // sleep: Windows' default
	}
	if err := setLidAction(prev); err != nil {
		return err
	}
	l.m.remove()
	return nil
}

func (l *winLid) Recover() error { return l.Release() }

func setLidAction(a lidActions) error {
	if _, err := powercfg("/setacvalueindex", "SCHEME_CURRENT", "SUB_BUTTONS", "LIDACTION", strconv.Itoa(a.AC)); err != nil {
		return err
	}
	if _, err := powercfg("/setdcvalueindex", "SCHEME_CURRENT", "SUB_BUTTONS", "LIDACTION", strconv.Itoa(a.DC)); err != nil {
		return err
	}
	_, err := powercfg("/setactive", "SCHEME_CURRENT")
	return err
}

func powercfg(args ...string) (string, error) {
	cmd := exec.Command("powercfg", args...) //nolint:gosec // our own fixed arguments
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("powercfg %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return string(out), nil
}

// systemPowerStatus is SYSTEM_POWER_STATUS (winbase.h).
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var getSystemPowerStatus = syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

func readPower() Power {
	var s systemPowerStatus
	if r, _, _ := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 { //nolint:gosec // a pointer to a struct of ours, for the call
		return Power{}
	}
	p := Power{Known: true}
	if s.BatteryFlag&128 != 0 || s.BatteryFlag == 255 { // no system battery, or unknown
		return p
	}
	p.HasBattery = true
	p.OnBattery = s.ACLineStatus == 0
	if s.BatteryLifePercent <= 100 {
		p.Percent = int(s.BatteryLifePercent)
	}
	return p
}
