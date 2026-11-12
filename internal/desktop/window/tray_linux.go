//go:build desktop && linux

package window

import (
	"log/slog"
	"time"

	"github.com/godbus/dbus/v5"
)

// watcher is the D-Bus service that shows tray icons (StatusNotifierItem):
// KDE, Xfce, Cinnamon and most desktops have it; GNOME only with the
// AppIndicator extension (Ubuntu ships it on).
const watcher = "org.kde.StatusNotifierWatcher"

// loginWait is how long a start at login waits for the panel to come up.
const loginWait = 30 * time.Second

// trayAvailable reports whether the desktop shows tray icons. Without them
// the window works as before the icon: closing it quits.
func trayAvailable(log *slog.Logger, background bool) bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Warn("window: no D-Bus session, so no tray icon", "err", err)
		return false
	}
	defer conn.Close()
	deadline := time.Now().Add(loginWait)
	for {
		var has bool
		if conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, watcher).Store(&has) == nil && has {
			return true
		}
		if !background || time.Now().After(deadline) {
			log.Warn("window: this desktop shows no tray icons (on GNOME, the AppIndicator extension adds them); closing the window quits Exhale's window")
			return false
		}
		time.Sleep(2 * time.Second)
	}
}
