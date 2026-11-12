//go:build desktop

package main

import "github.com/akamachinecode/exhale/internal/desktop/window"

// openWindow shows the web UI in a window of its own, with Exhale's icon in
// the menu bar or tray: the desktop apps, Exhale.app and Exhale.exe, are
// built with the desktop tag (ADR 0030, 0032).
func openWindow(dataDir string) func(string, bool) error {
	return func(url string, background bool) error {
		return window.Run(window.Options{URL: url, DataDir: dataDir, Background: background})
	}
}
