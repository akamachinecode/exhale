//go:build desktop

package main

import "github.com/akamachinecode/exhale/internal/desktop/window"

// openWindow shows the web UI in a window of its own: the desktop apps,
// Exhale.app and Exhale.exe, are built with the desktop tag (ADR 0030).
func openWindow(dataDir string) func(string) error {
	return func(url string) error { return window.Run(window.Options{URL: url, DataDir: dataDir}) }
}
