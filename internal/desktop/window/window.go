//go:build desktop

// Package window shows Exhale's web UI in a window of its own (ADR 0030):
// a native webview on the local server, WebKit on macOS, WebView2 on Windows
// and WebKitGTK on Linux, through Wails. The server keeps running on its
// own; closing the window only closes the window.
//
// The same process shows Exhale's icon in the menu bar (the tray on Windows
// and Linux, ADR 0032): the next charges and keeping the computer awake.
// With the icon there, closing the window hides it and the process stays;
// "Quit" in the icon's menu ends both, never the server. At login it starts
// with the window hidden (Options.Background).
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
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

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
	DataDir string // Exhale's data directory: the icon's token, WebView2's profile
	// Background starts with the window hidden: only the icon (at login).
	Background bool
}

// Run opens the window and returns when it is closed, or with the icon,
// when Quit is chosen.
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
	// A smoke run only tries the window, as before the icon.
	withTray := b.smoke == "" && trayAvailable(log, o.Background)
	if o.Background && !withTray {
		return nil // nothing to show until Exhale is opened
	}
	policy := application.ActivationPolicyRegular
	if o.Background {
		policy = application.ActivationPolicyAccessory // no Dock icon while the window is hidden
	}
	app := application.New(application.Options{
		Name:        "Exhale",
		Description: "Your subscriptions, handled.",
		Icon:        icon,
		Logger:      log,
		LogLevel:    level,
		Mac: application.MacOptions{
			// A Dock icon while the window is open; quitting closes only
			// the window and the icon, the server keeps sending reminders.
			ActivationPolicy: policy,
			ApplicationShouldTerminateAfterLastWindowClosed: !withTray,
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath:           webviewDataPath(o.DataDir),
			DisableQuitOnLastWindowClosed: withTray,
		},
		Linux: application.LinuxOptions{
			// The Wayland app id and X11 class, which the launcher entry
			// names in StartupWMClass so the dock shows Exhale's icon.
			ApplicationID:                 desktop.ID,
			DisableQuitOnLastWindowClosed: withTray,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: desktop.ID,
			// Opening Exhale again shows the window; a second start at
			// login has nothing to add.
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				if !slices.Contains(d.Args, desktop.BackgroundFlag) {
					b.show("")
				}
			},
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
		Hidden:           o.Background,
		BackgroundColour: application.NewRGB(7, 7, 7), // --bg of the dark theme, no white flash
		// The web UI draws the whole window, title bar included (ADR 0031).
		// On Windows and Linux there is no system frame: the page has its
		// own minimise, maximise and close buttons and drag area. On a Mac
		// the traffic lights stay, over the sidebar.
		Frameless: runtime.GOOS != "darwin",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})
	b.watchState()
	if withTray {
		b.tray = startTray(app, b, desktop.Client{Base: base.String(), DataDir: o.DataDir}, log)
		// Closing the window hides it: the icon stays, and brings it back.
		b.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			e.Cancel()
			b.hide()
		})
		// A Mac opens a running app again by asking it to reopen.
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { b.show("") })
	}
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
	tray  *tray
	smoke string
	log   *slog.Logger

	mu        sync.Mutex
	smokeDone bool
	smokeFail error
}

// message is what the web UI sends.
type message struct {
	Type   string `json:"type"` // ready, open, save or window
	URL    string `json:"url,omitempty"`
	Name   string `json:"name,omitempty"`
	Data   string `json:"data,omitempty"`   // base64
	Action string `json:"action,omitempty"` // window: minimise, maximise, close or titlebar (a double click)
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
		b.sendState()
		b.finishSmoke(nil)
	case "window":
		b.control(m.Action)
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

// control runs the page's own window buttons.
func (b *bridge) control(action string) {
	if b.win == nil {
		return
	}
	switch action {
	case "minimise":
		b.win.Minimise()
	case "maximise":
		if b.win.IsFullscreen() {
			b.win.UnFullscreen()
		} else {
			b.win.ToggleMaximise()
		}
	case "close":
		b.win.Close()
	case "titlebar": // a double click on the title bar
		if b.win.IsFullscreen() {
			return
		}
		switch titlebarDoubleClick() {
		case titlebarZoom:
			b.win.ToggleMaximise()
		case titlebarMinimise:
			b.win.Minimise()
		case titlebarNothing:
		}
	}
}

// watchState tells the page when the window is maximised or full screen:
// the maximise button turns into restore, and a Mac in full screen has no
// traffic lights to make room for.
func (b *bridge) watchState() {
	for _, e := range []events.WindowEventType{
		events.Common.WindowMaximise,
		events.Common.WindowUnMaximise,
		events.Common.WindowFullscreen,
		events.Common.WindowUnFullscreen,
		events.Common.WindowRestore,
	} {
		b.win.OnWindowEvent(e, func(*application.WindowEvent) { b.sendState() })
	}
}

func (b *bridge) sendState() {
	if b.win == nil {
		return
	}
	b.win.ExecJS(stateJS(b.win.IsMaximised(), b.win.IsFullscreen()))
}

// stateJS is the event the page listens to (web/src/lib/desktop.ts).
func stateJS(maximised, fullscreen bool) string {
	return fmt.Sprintf(`window.dispatchEvent(new CustomEvent("exhale:window",{detail:{maximised:%t,fullscreen:%t}}))`, maximised, fullscreen)
}

// show brings the window to the front, at path of the web UI when one is
// given: when Exhale is opened again, or from the icon's menu.
func (b *bridge) show(path string) {
	if b.win == nil {
		return
	}
	if path != "" {
		if u, err := b.base.Parse(path); err == nil && u.Host == b.base.Host {
			b.win.SetURL(u.String())
		}
	}
	setDock(true)
	b.win.Show()
	b.win.UnMinimise()
	b.win.Focus()
}

// hide puts the window away; the icon stays. A Mac drops the Dock icon
// until the window shows again.
func (b *bridge) hide() {
	if b.win == nil {
		return
	}
	b.win.Hide()
	setDock(false)
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
