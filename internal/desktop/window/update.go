//go:build desktop

package window

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/akamachinecode/exhale/internal/desktop"
	"github.com/akamachinecode/exhale/internal/update"
)

// updatePackage updates Exhale installed from the Linux packages, when the
// person presses Update in the window (ADR 0033). The server runs as the
// exhale system user and may not replace /usr/bin/exhale; this process is
// the person's, so it downloads the newest package, checks it, and installs
// it with pkexec, which asks for their password in the system's dialog.
// The package restarts the server, and followUpdates reopens this window.
func (b *bridge) updatePackage() {
	if runtime.GOOS != "linux" {
		return
	}
	b.mu.Lock()
	if b.updating {
		b.mu.Unlock()
		return
	}
	b.updating = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.updating = false
		b.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "exhale-update-")
	if err != nil {
		b.sendUpdate("failed", err.Error())
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	b.sendUpdate("downloading", "")
	u := &update.Updater{Version: b.serverVersion(ctx), Dir: filepath.Join(dir, "files"), API: os.Getenv("EXHALE_UPDATE_API")}
	kind := update.PackageKind()
	file, version, err := u.DownloadPackage(ctx, kind)
	if err != nil {
		b.sendUpdate("failed", err.Error())
		return
	}
	b.sendUpdate("installing", "")
	b.log.Info("window: installing the new package", "version", version, "file", file)
	if err := update.InstallPackage(ctx, kind, file); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127) {
			b.sendUpdate("cancelled", "")
			return
		}
		b.sendUpdate("failed", err.Error())
		return
	}
	b.sendUpdate("restarting", "")
}

// serverVersion is the version of the server this window shows.
func (b *bridge) serverVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.base.JoinPath("healthz").String(), nil)
	if err != nil {
		return ""
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	_ = res.Body.Close()
	return res.Header.Get(desktop.VersionHeader)
}

// sendUpdate tells the page how the update goes (web/src/lib/desktop.ts).
func (b *bridge) sendUpdate(phase, msg string) {
	if b.win == nil {
		return
	}
	detail, _ := json.Marshal(map[string]string{"phase": phase, "error": msg})
	b.win.ExecJS(fmt.Sprintf(`window.dispatchEvent(new CustomEvent("exhale:update",{detail:%s}))`, detail))
}
