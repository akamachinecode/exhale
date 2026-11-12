//go:build desktop && !linux

package window

import "log/slog"

// trayAvailable is always true on macOS and Windows, which always have a
// menu bar or a notification area.
func trayAvailable(*slog.Logger, bool) bool { return true }
