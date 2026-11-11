package desktop

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// agentPlist is the launchd agent of the macOS app: it runs exe serve when
// the person logs in, and again if it stops with an error. A server that
// exits cleanly (Ctrl-C, a stop from the web) stays stopped until the next
// login or the next time the app is opened.
func agentPlist(exe, logFile string) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return []byte(strings.NewReplacer("{id}", ID, "{exe}", esc(exe), "{log}", esc(logFile)).Replace(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by Exhale.app each time it is opened (ADR 0029). To stop Exhale
     starting at login, turn it off in System Settings → General → Login Items. -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{id}</string>
	<key>AssociatedBundleIdentifiers</key>
	<string>{id}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{exe}</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>{log}</string>
	<key>StandardErrorPath</key>
	<string>{log}</string>
</dict>
</plist>
`))
}
