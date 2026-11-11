package desktop

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// misplaced reports an app that runs from its disk image or from the
// read-only copy macOS makes of a downloaded app that was never moved (App
// Translocation): a login item pointing there would break.
func misplaced(exe string) bool {
	return strings.HasPrefix(exe, "/Volumes/") || strings.Contains(exe, "/AppTranslocation/")
}

// start makes sure the server runs. The app writes its launchd agent and
// loads it; an agent that points elsewhere (the app was moved or replaced)
// is reloaded, and a running server of another version is restarted, so
// opening a new Exhale.app is how it upgrades.
func (a App) start(ctx context.Context, up bool, version string) (string, error) {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	target := domain + "/" + ID
	loaded := launchctl(ctx, "print", target) == nil
	if !a.Login {
		switch {
		case up:
			return "", nil
		case loaded:
			return "", launchctl(ctx, "kickstart", target)
		default:
			return "", a.spawn()
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "Library", "LaunchAgents", ID+".plist")
	plist := agentPlist(a.Exe, a.logPath())
	old, _ := os.ReadFile(path) //nolint:gosec // our own agent file
	changed := !bytes.Equal(old, plist)
	if changed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // ~/Library/LaunchAgents is 0755
			return "", err
		}
		if err := os.WriteFile(path, plist, 0o644); err != nil { //nolint:gosec // launchd agents are 0644; nothing secret in it
			return "", err
		}
	}
	switch {
	case changed || !loaded:
		if loaded {
			_ = launchctl(ctx, "bootout", target)
		}
		// bootout returns before launchd has let go of the agent.
		var err error
		for range 20 {
			if err = launchctl(ctx, "bootstrap", domain, path); err == nil {
				return a.Version, nil
			}
			time.Sleep(250 * time.Millisecond)
		}
		return "", err
	case up && version != "" && version != a.Version:
		return a.Version, launchctl(ctx, "kickstart", "-k", target)
	case !up:
		return "", launchctl(ctx, "kickstart", target)
	}
	return "", nil
}

func launchctl(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput() //nolint:gosec // fixed program, our own arguments
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DefaultLogFile is the log of a server started by the app, where
// Console.app looks for logs.
func DefaultLogFile(string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "Exhale.log")
}

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func browse(url string) error { return exec.Command("/usr/bin/open", url).Run() } //nolint:gosec // an http:// address of this machine

// alert shows a dialog: the app has no window, and its errors would
// otherwise go nowhere.
func alert(title, message string) {
	_ = exec.Command("/usr/bin/osascript", //nolint:gosec // texts are passed as arguments, never as script
		"-e", "on run argv",
		"-e", "display alert (item 1 of argv) message (item 2 of argv) as critical giving up after 600",
		"-e", "end run",
		title, message).Run()
}

// systemLanguages are the person's preferred languages: an app started
// from Finder has no LANG.
func systemLanguages() []string {
	out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return languagesFromEnv()
	}
	return append(parseAppleLanguages(string(out)), languagesFromEnv()...)
}
