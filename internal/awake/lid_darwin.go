package awake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	sudo    = "/usr/bin/sudo"
	pmset   = "/usr/bin/pmset"
	ioreg   = "/usr/sbin/ioreg"
	sudoers = "/etc/sudoers.d/exhale"
)

// macLid turns lid sleep off with `sudo -n pmset disablesleep 1`, allowed
// without a password by the rule Setup installs.
type macLid struct{ m marker }

func newLid(dir string) Lid { return &macLid{newMarker(dir)} }

func (l *macLid) Available() error {
	// -n never asks for a password: without the rule it fails at once.
	if exec.Command(sudo, "-n", "-l", pmset, "disablesleep", "1").Run() != nil { //nolint:gosec // fixed paths
		return ErrNeedsSetup
	}
	return nil
}

// Setup installs the sudoers rule. macOS shows its own password dialog, on
// this computer's screen, through osascript: the server runs in the
// person's login session (a launchd agent), so the dialog reaches them.
// The rule is checked with visudo before it is put in place.
func (l *macLid) Setup(ctx context.Context) error {
	rule := sudoersRule(os.Getuid())
	script := strings.Join([]string{
		`f=$(/usr/bin/mktemp /tmp/exhale-sudoers.XXXXXX)`,
		`/usr/bin/printf '%s\n' '` + rule + `' > "$f"`,
		`/usr/sbin/visudo -cf "$f" >/dev/null`,
		`/usr/sbin/chown root:wheel "$f"`,
		`/bin/chmod 0440 "$f"`,
		`/bin/mkdir -p /etc/sudoers.d`,
		`/bin/mv "$f" ` + sudoers,
	}, " && ")
	as := fmt.Sprintf(`do shell script %s with prompt %s with administrator privileges`,
		appleString(script), appleString("Exhale wants to keep this Mac awake with its lid closed."))
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", as).CombinedOutput() //nolint:gosec // our own script
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") { // User canceled.
			return errors.New("the administrator's password was not given")
		}
		return fmt.Errorf("osascript: %s", msg)
	}
	return l.Available()
}

// appleString quotes s as an AppleScript string.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (l *macLid) Hold() error {
	if err := l.m.write([]byte("darwin\n")); err != nil {
		return err
	}
	if err := pmsetDisableSleep(true); err != nil {
		l.m.remove()
		return err
	}
	return nil
}

func (l *macLid) Release() error {
	if err := pmsetDisableSleep(false); err != nil {
		return err
	}
	l.m.remove()
	if lidClosed() {
		_ = exec.Command(pmset, "sleepnow").Run() //nolint:gosec // fixed path
	}
	return nil
}

func (l *macLid) Recover() error {
	if _, ok := l.m.read(); !ok {
		return nil
	}
	if out, err := exec.Command(pmset, "-g").Output(); err == nil && !parsePmsetSleepDisabled(string(out)) { //nolint:gosec // fixed path
		l.m.remove() // someone already turned it back on
		return nil
	}
	return l.Release()
}

func pmsetDisableSleep(off bool) error {
	v := "0"
	if off {
		v = "1"
	}
	out, err := exec.Command(sudo, "-n", pmset, "disablesleep", v).CombinedOutput() //nolint:gosec // fixed paths
	if err != nil {
		return fmt.Errorf("pmset disablesleep %s: %s", v, strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return nil
}

func lidClosed() bool {
	out, err := exec.Command(ioreg, "-r", "-k", "AppleClamshellState", "-d", "1").Output() //nolint:gosec // fixed path
	return err == nil && parseClamshell(string(out))
}

func readPower() Power {
	out, err := exec.Command(pmset, "-g", "batt").Output() //nolint:gosec // fixed path
	if err != nil {
		return Power{}
	}
	return parsePmsetBatt(string(out))
}
