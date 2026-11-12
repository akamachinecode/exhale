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

// MenuBarID labels the launchd agent that starts the menu bar icon.
const MenuBarID = ID + ".menubar"

// menuBarPlist is the launchd agent that shows the menu bar icon from login
// on (ADR 0032): exe open --background, in the person's graphical session.
// It is not restarted: Quit in the icon's menu means until the next login
// or the next time the app is opened.
func menuBarPlist(exe, logFile string) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return []byte(strings.NewReplacer("{label}", MenuBarID, "{id}", ID, "{exe}", esc(exe), "{log}", esc(logFile), "{flag}", BackgroundFlag).Replace(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by Exhale.app each time it is opened (ADR 0032): Exhale's menu
     bar icon from login on. To stop it, turn Exhale off in System Settings
     → General → Login Items, or choose Quit in the icon's menu until the
     next login. -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{label}</string>
	<key>AssociatedBundleIdentifiers</key>
	<string>{id}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{exe}</string>
		<string>open</string>
		<string>{flag}</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>{log}</string>
	<key>StandardErrorPath</key>
	<string>{log}</string>
</dict>
</plist>
`))
}

// autostartEntry is the XDG autostart entry of the tray icon on Linux (ADR
// 0032). TryExec skips it once Exhale is uninstalled.
func autostartEntry(exe string) []byte {
	return []byte(`[Desktop Entry]
# Added by Exhale the first time it opened (ADR 0032): Exhale's tray icon
# from login on. Delete this file, or turn Exhale off in your desktop's
# startup apps, to stop it; Exhale does not add it again.
Type=Application
Name=Exhale
Comment=Your next charges and keep awake, in the tray.
Exec=` + desktopExec(exe) + ` open ` + BackgroundFlag + `
TryExec=` + exe + `
Icon=exhale
Terminal=false
NoDisplay=true
X-GNOME-Autostart-enabled=true
`)
}

// desktopExec quotes a program for the Exec key of a desktop entry.
func desktopExec(exe string) string {
	if !strings.ContainsAny(exe, " \t\n\"'\\><~|&;$*?#()`") {
		return exe
	}
	r := strings.NewReplacer(`"`, `\"`, "`", "\\`", "$", `\$`, `\`, `\\`)
	return `"` + r.Replace(exe) + `"`
}
