//go:build desktop

// Package window shows Exhale's web UI in a window of its own (ADR 0030):
// a native webview on the local server, WebKit on macOS, WebView2 on Windows
// and WebKitGTK on Linux, through Wails. The server keeps running on its
// own; closing the window only closes the window.
//
// It is built only with the desktop tag, in the desktop apps (Exhale.app,
// Exhale.exe) and in exhale-window on Linux, so the server binary everywhere
// else stays free of cgo and graphical libraries.
package window

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/akamachinecode/exhale/internal/desktop"
)

//go:embed icon.png
var icon []byte

// SmokeEnv names a file: when set, the window writes "ready" to it as soon
// as the web UI has loaded and talked to the window, then closes. The
// installers workflow opens each app this way on a real machine.
const SmokeEnv = "EXHALE_WINDOW_SMOKE"

// DebugEnv, when set, logs the window's messages and Wails's own debug log
// to stderr.
const DebugEnv = "EXHALE_WINDOW_DEBUG"

// smokeTimeout bounds a smoke run: a window that never loads fails.
const smokeTimeout = 90 * time.Second

// maxSave bounds a file the web hands over to save (exports, the archive
// with documents).
const maxSave = 512 << 20

// Options are what the window needs.
type Options struct {
	URL     string // the web UI, http://127.0.0.1:8484
	DataDir string // Exhale's data directory; WebView2 keeps its profile there
}

// Run opens the window and returns when it is closed.
func Run(o Options) error {
	base, err := url.Parse(o.URL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return fmt.Errorf("window: not a web address: %q", o.URL)
	}
	level := slog.LevelWarn
	if os.Getenv(DebugEnv) != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	webkitSandbox(log)
	b := &bridge{base: base, smoke: os.Getenv(SmokeEnv), log: log}
	app := application.New(application.Options{
		Name:        "Exhale",
		Description: "Your subscriptions, handled.",
		Icon:        icon,
		Logger:      log,
		LogLevel:    level,
		Mac: application.MacOptions{
			// A Dock icon while the window is open; quitting closes only
			// the window, the server keeps sending reminders.
			ActivationPolicy: application.ActivationPolicyRegular,
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: webviewDataPath(o.DataDir),
		},
		Linux: application.LinuxOptions{
			// The Wayland app id and X11 class, which the launcher entry
			// names in StartupWMClass so the dock shows Exhale's icon.
			ApplicationID: desktop.ID,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               desktop.ID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { b.raise() },
		},
		RawMessageHandler: b.handle,
	})
	b.app = app
	b.win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Exhale",
		URL:              base.String(),
		Width:            1280,
		Height:           860,
		MinWidth:         380,
		MinHeight:        560,
		InitialPosition:  application.WindowCentered,
		BackgroundColour: application.NewRGB(7, 7, 7), // --bg of the dark theme, no white flash
	})
	if b.smoke != "" {
		time.AfterFunc(smokeTimeout, func() { b.finishSmoke(fmt.Errorf("the web UI did not load within %s", smokeTimeout)) })
	}
	if err := app.Run(); err != nil {
		return err
	}
	return b.smokeErr()
}

// webviewDataPath keeps WebView2's profile (the session cookie) with
// Exhale's data on Windows; elsewhere the webview has its own place.
func webviewDataPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "WebView2")
}

// bridge answers the web UI, which sends "exhale:" and a JSON message
// through the webview (web/src/lib/desktop.ts).
type bridge struct {
	base  *url.URL
	app   *application.App
	win   *application.WebviewWindow
	smoke string
	log   *slog.Logger

	mu        sync.Mutex
	smokeDone bool
	smokeFail error
}

// message is what the web UI sends.
type message struct {
	Type string `json:"type"` // ready, open or save
	URL  string `json:"url,omitempty"`
	Name string `json:"name,omitempty"`
	Data string `json:"data,omitempty"` // base64
}

func (b *bridge) handle(_ application.Window, raw string, origin *application.OriginInfo) {
	body, ok := strings.CutPrefix(raw, "exhale:")
	if b.log.Enabled(context.Background(), slog.LevelDebug) {
		b.log.Debug("window: message", "message", truncate(raw, 120), "origin", fmt.Sprintf("%+v", origin))
	}
	if !ok || !b.trusted(origin) {
		return
	}
	var m message
	if json.Unmarshal([]byte(body), &m) != nil {
		return
	}
	switch m.Type {
	case "ready":
		b.finishSmoke(nil)
	case "open":
		b.open(m.URL)
	case "save":
		go b.save(m.Name, m.Data)
	}
}

// trusted accepts messages only from Exhale's own pages: the window may
// also show a sign-in provider's page (OIDC), which must not reach here.
// The origin is the sending frame's, so a frame of another site is turned
// away too. (IsMainFrame is not filled in on every system.)
func (b *bridge) trusted(o *application.OriginInfo) bool {
	if o == nil {
		return false
	}
	u, err := url.Parse(o.Origin)
	return err == nil && u.Scheme == b.base.Scheme && u.Host == b.base.Host
}

// open hands a link to another site to the person's browser (or Telegram,
// or their mail app): the window has no address bar to come back from it.
func (b *bridge) open(link string) {
	u, err := url.Parse(link)
	if err != nil {
		return
	}
	switch u.Scheme {
	case "http", "https", "mailto", "tg":
		_ = b.app.Browser.OpenURL(u.String())
	}
}

// save asks where to save a file the web UI downloaded (an export, a
// document) and writes it there.
func (b *bridge) save(name, data string) {
	if base64.StdEncoding.DecodedLen(len(data)) > maxSave {
		return
	}
	content, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return
	}
	dlg := b.app.Dialog.SaveFile().
		SetFilename(safeName(name)).
		CanCreateDirectories(true).
		AttachToWindow(b.win)
	if home, err := os.UserHomeDir(); err == nil {
		if d := filepath.Join(home, "Downloads"); isDir(d) {
			dlg = dlg.SetDirectory(d)
		}
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		b.app.Dialog.Error().SetTitle("Exhale").SetMessage(err.Error()).Show()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// safeName keeps a suggested file name to a name, never a path.
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" || name == "" {
		return "exhale-download"
	}
	return name
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// raise brings the window back when Exhale is opened again.
func (b *bridge) raise() {
	if b.win == nil {
		return
	}
	b.win.Show()
	b.win.UnMinimise()
	b.win.Focus()
}

// finishSmoke ends a smoke run: err nil means the web UI loaded.
func (b *bridge) finishSmoke(err error) {
	if b.smoke == "" {
		return
	}
	b.mu.Lock()
	if b.smokeDone {
		b.mu.Unlock()
		return
	}
	b.smokeDone, b.smokeFail = true, err
	b.mu.Unlock()
	result := "ready\n"
	if err != nil {
		result = "failed: " + err.Error() + "\n"
	}
	_ = os.WriteFile(b.smoke, []byte(result), 0o600)
	b.app.Quit()
}

func (b *bridge) smokeErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.smoke != "" && !b.smokeDone {
		return errors.New("window: closed before the web UI loaded")
	}
	return b.smokeFail
}
