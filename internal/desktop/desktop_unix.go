//go:build !darwin && !windows

package desktop

import (
	"context"
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
