//go:build desktop && !darwin

package window

// setDock does nothing outside a Mac: a hidden window leaves the taskbar
// by itself.
func setDock(bool) {}
