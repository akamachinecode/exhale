//go:build !desktop

package main

// openWindow is nil without the desktop tag: `exhale open` then uses the
// window program next to it (exhale-window in the Linux packages) or the
// browser, and this binary links no graphical library (ADR 0030).
func openWindow(string) func(string, bool) error { return nil }
