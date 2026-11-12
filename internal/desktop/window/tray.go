//go:build desktop

package window

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/akamachinecode/exhale/internal/desktop"
)

// refreshEvery is how often the icon reads the glance again. It also reads
// it after each action from its menu.
const refreshEvery = time.Minute

// glanceLimit is how many charges the menu lists.
const glanceLimit = 5

// tray is the menu bar icon (ADR 0032). Its menu is built by
// desktop.BuildMenu from the server's glance; this runs it.
type tray struct {
	app    *application.App
	b      *bridge
	icon   *application.SystemTray
	menu   *application.Menu
	client desktop.Client
	log    *slog.Logger
	lang   string // the person's language until the glance says the member's

	mu      sync.Mutex // one refresh at a time
	shown   *desktop.Menu
	awake   bool
	noToken bool
	kick    chan struct{}
}

// startTray adds the icon; it reads the glance once the app runs.
func startTray(app *application.App, b *bridge, client desktop.Client, log *slog.Logger) *tray {
	t := &tray{
		app: app, b: b, client: client, log: log,
		lang: desktop.SystemLanguage(),
		menu: app.NewMenu(),
		kick: make(chan struct{}, 1),
	}
	t.icon = app.SystemTray.New()
	t.setIcon(false)
	t.fill(desktop.BuildMenu(desktop.MenuInput{Lang: t.lang, Now: time.Now(), OS: runtime.GOOS}))
	t.icon.SetMenu(t.menu)
	// A double click on Windows opens the window, as tray icons do there.
	t.icon.OnDoubleClick(func() { b.show("") })
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go t.loop(app.Context())
	})
	return t
}

// loop reads the glance every minute, and at once when kicked.
func (t *tray) loop(ctx context.Context) {
	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()
	for {
		t.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-t.kick:
		}
	}
}

// refreshSoon reads the glance again now, after an action.
func (t *tray) refreshSoon() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

func (t *tray) refresh(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	in := desktop.MenuInput{Lang: t.lang, Now: time.Now(), OS: runtime.GOOS}
	g, err := t.client.Glance(ctx, glanceLimit)
	switch {
	case err == nil:
		in.Glance = &g
		t.noToken = false
	case errors.Is(err, desktop.ErrNoToken):
		// A new install has no owner yet; said once, tried every minute.
		if !t.noToken {
			t.log.Info("menu bar: no access to Exhale yet; the icon shows only Open and Quit", "dataDir", t.client.DataDir)
		}
		t.noToken = true
	default:
		t.log.Warn("menu bar: cannot read Exhale", "err", err)
	}
	m := desktop.BuildMenu(in)
	if t.shown != nil && reflect.DeepEqual(*t.shown, m) {
		return
	}
	t.shown = &m
	if m.Awake != t.awake {
		t.setIcon(m.Awake)
	}
	t.icon.SetTooltip(m.Tooltip)
	t.menu.Clear()
	t.fill(m)
	t.icon.SetMenu(t.menu)
}

// setIcon shows the mark, with the dot while the computer is kept awake.
func (t *tray) setIcon(awake bool) {
	t.awake = awake
	if runtime.GOOS == "darwin" {
		// Tinted by macOS for a light or dark menu bar.
		t.icon.SetTemplateIcon(desktop.IconPNG(44, true, awake))
		return
	}
	t.icon.SetIcon(desktop.IconPNG(64, false, awake))
}

// fill adds the menu's items to the native menu.
func (t *tray) fill(m desktop.Menu) {
	t.add(t.menu, m.Items)
}

func (t *tray) add(menu *application.Menu, items []desktop.MenuItem) {
	for _, it := range items {
		switch {
		case it.Separator:
			menu.AddSeparator()
		case len(it.Items) > 0:
			t.add(menu.AddSubmenu(it.Label), it.Items)
		case it.Check:
			a := it.Action
			menu.AddCheckbox(it.Label, it.Checked).SetEnabled(it.Enabled).OnClick(func(*application.Context) { t.run(a) })
		default:
			a := it.Action
			item := menu.Add(it.Label).SetEnabled(it.Enabled)
			if a.Kind != "" {
				item.OnClick(func(*application.Context) { t.run(a) })
			}
		}
	}
}

// run does what a menu item says. Wails calls it off the main thread.
func (t *tray) run(a desktop.MenuAction) {
	ctx := t.app.Context()
	var err error
	switch a.Kind {
	case desktop.ActOpen:
		t.b.show(a.Path)
		return
	case desktop.ActQuit:
		t.app.Quit()
		return
	case desktop.ActStart:
		err = t.client.Session(ctx, "start", a.Minutes)
	case desktop.ActExtend:
		err = t.client.Session(ctx, "extend", a.Minutes)
	case desktop.ActStop:
		err = t.client.Session(ctx, "stop", 0)
	case desktop.ActOnPower:
		err = t.client.SetKeepAwake(ctx, map[string]any{"onlyOnPower": a.On})
	case desktop.ActLid:
		err = t.client.SetKeepAwake(ctx, map[string]any{"lidClosed": a.On})
		if err == nil && a.Setup {
			t.refreshSoon() // ticked while the password is asked for
			err = t.client.SetupLid(ctx)
		}
	}
	if err != nil {
		t.log.Warn("menu bar: action failed", "action", a.Kind, "err", err)
		t.app.Dialog.Error().SetTitle("Exhale").SetMessage(err.Error()).Show()
	}
	t.refreshSoon()
}
