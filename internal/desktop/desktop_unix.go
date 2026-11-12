//go:build !darwin && !windows

package desktop

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// systemUnits are where the .deb and .rpm packages put the service.
var systemUnits = []string{"/usr/lib/systemd/system/exhale.service", "/lib/systemd/system/exhale.service", "/etc/systemd/system/exhale.service"}

func misplaced(string) bool { return false }

// start makes sure the server runs. The packages run it as a system
// service, which only root can start; anywhere else it is started here.
func (a App) start(_ context.Context, up bool, _ string) (string, error) {
	if up {
		return "", nil
	}
	for _, u := range systemUnits {
		if _, err := os.Stat(u); err == nil {
			return "", errSystemService
		}
	}
	return "", a.spawn()
}

// iconAtLogin adds the tray icon to the person's startup apps (XDG
// autostart), the first time Exhale opens in its window. Only once: a
// person who removes it, by deleting the file or in their desktop's
// startup settings, does not get it back.
func (a App) iconAtLogin() error {
	if a.Background || windowProgram(a.Exe) == "" || os.Getuid() == 0 {
		return nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	marker := filepath.Join(config, "exhale", "autostart-added")
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entry := filepath.Join(config, "autostart", ID+".desktop")
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(entry, autostartEntry(a.Exe), 0o644); err != nil { //nolint:gosec // a desktop entry, read by the session
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("The tray icon was added to ../autostart once; Exhale does not add it again.\n"), 0o600)
}

// DefaultLogFile is the log of a server started without a terminal.
func DefaultLogFile(dataDir string) string { return filepath.Join(dataDir, "exhale.log") }

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func browse(url string) error { return exec.Command("xdg-open", url).Start() } //nolint:gosec // an http:// address of this machine

// alert shows a desktop notification when it can, and writes to stderr.
func alert(title, message string) {
	_, _ = os.Stderr.WriteString(title + ": " + message + "\n")
	if p, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(p, "--app-name=Exhale", "--urgency=critical", title, message).Run() //nolint:gosec // notify-send, texts as arguments
	}
}

func systemLanguages() []string { return languagesFromEnv() }
