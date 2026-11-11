//go:build desktop && !linux

package window

import "log/slog"

// webkitSandbox has nothing to do outside Linux: WebKit on macOS and
// WebView2 on Windows sandbox their pages by themselves.
func webkitSandbox(*slog.Logger) {}
