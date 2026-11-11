# 0030. Exhale's own window, with Wails

- Status: Accepted
- Date: 2026-11-11
- Amends: [0029](0029-desktop-installers.md) (the installed app opened the browser), [0002](0002-backend-language-go.md) and [0009](0009-runtime-and-system.md) (one binary without cgo: still true of the server)
- Amended by: [0031](0031-window-title-bar.md) (the page draws the title bar)

## Context — what it has to do

With ADR 0029, opening the installed app started the server and opened the web UI in a browser tab. The owner tried the Linux package and asked for the opposite: **Exhale opens in a window of its own, on every system, never in the browser**. The local server and its port stay; only what shows them changes.

Two pieces, then. The **server** is the engine: it sends the reminders and answers Telegram, starts at login (launchd, the `Run` key, systemd) and is unchanged. The **window** is the dashboard: it shows the web UI of that server and closing it never stops a reminder. Physically, the window has to:

1. Show `http://127.0.0.1:8484` in a native window with Exhale's icon in the Dock, taskbar or launcher.
2. Come back to the front when Exhale is opened again, instead of opening a second one.
3. Behave like an app: copy and paste, ⌘Q on a Mac, a sign-in that survives restarts.
4. Hand links to other sites (Telegram's BotFather, docs, a service's cancel page) to the person's browser: the window has no address bar to come back from them.
5. Save downloads (exports, the archive, documents) where the person chooses: the window has no download bar.
6. Leave the server as it is: one binary without cgo for Docker, servers and Raspberry Pis.

## Criteria

| Criterion | Weight |
|---|---|
| Feels like an app on macOS, Windows and Linux (points 1–5) | High |
| The server binary keeps no graphical dependency and no cgo (ADR 0002, 0009) | High |
| Languages and tools a single maintainer keeps up: Go and TypeScript | High |
| Security patches arrive without us shipping a release | High |
| Download size and memory | Medium |
| Each installer tried on a real machine before release (ADR 0029) | Medium |

## Options

| | Electron | Tauri 2 | **Wails 3** | webview/webview_go | Install the web as a PWA |
|---|---|---|---|---|---|
| Web engine | Its own Chromium | The system's | The system's | The system's | The browser's |
| Download size | +100 MB | ~10 MB | ~10 MB | ~5 MB | 0 |
| Language of the shell | TypeScript | Rust (a third language) | Go | Go + C | — |
| Security patches | We must ship Chromium's, every few weeks | The OS's | The OS's | The OS's | The browser's |
| Menus, single instance, dialogs | ✅ | ✅ | ✅ | Ours to write | — |
| State | Mature | Mature | 3.0 stable since 2026-10-26; the 3.x line in wide use as alphas for two years | No release since 2024 | Still a browser, no PWA install in Firefox |

## Decision

**Wails 3 (3.1.0), with its default cgo backend, in a window that loads the local server.** Same idea as Tauri, in the language Exhale is written in.

- **One window on the server's address.** `internal/desktop/window` opens a Wails window on the URL `exhale open` already computes. The web UI, the API, the session cookie and live updates work exactly as in a browser, because it is the same origin; nothing is proxied or bundled into the window.
- **Only the desktop builds include it.** It is behind the `desktop` build tag. `exhale` without it (Docker, archives, the server in the Linux packages) links no graphical library and stays `CGO_ENABLED=0`.
  - **macOS:** `Exhale.app`'s binary is built with the tag and cgo (WebKit), for arm64 and amd64 on the Mac runner. launchd still runs that same binary as the server, which never touches AppKit, so it shows no Dock icon. The app now shows one while the window is open (`LSUIElement` is gone), and `NSAllowsLocalNetworking` lets the window load `127.0.0.1`.
  - **Windows:** `Exhale.exe` is built with the tag and still cross-compiled without cgo: WebView2 is called from Go. Its profile (the session) lives in `%LOCALAPPDATA%\Exhale\WebView2`.
  - **Linux:** a separate program, `exhale-window` (`cmd/exhale-window`), built with cgo against GTK 4 and WebKitGTK 6.0 on Ubuntu 24.04 runners (amd64 and arm64) and added to those `.deb`/`.rpm` next to `exhale`. GTK and WebKitGTK are *recommended* dependencies: every desktop has them, and a server installs without them. `exhale open` runs `exhale-window` when it is there. If its libraries are missing (the loader exits 127), or on the armv7 packages, which have none, the browser opens as before. The launcher entry is named after the window's app id (`io.github.akamachinecode.exhale`), so the dock matches the two.
- **A bridge for links and downloads.** The web UI detects the window by its message channel (`window.webkit.messageHandlers.external`, `window.chrome.webview`) and sends `exhale:` + JSON messages (`web/src/lib/desktop.ts`):
  - **Links:** a link to another origin opens in the system browser (or Telegram, or the mail app).
  - **Downloads:** a same-origin download is fetched with the session and handed over in base64, and the window asks where to save it with a native dialog.

  The window accepts messages only from the server's origin, so a sign-in provider's page shown during OIDC cannot use it. In a browser, none of this runs.
- **One window, the app's own lifetime.** A second launch brings the first window to the front (Wails single instance). Closing the window ends the window process, never the server. `exhale open --browser` still opens the browser.
- **Tried before release.** In the installers workflow, each installer opens the window on a real Mac, a real Windows machine and Linux (Xvfb). `EXHALE_WINDOW_SMOKE` makes the window close once the web UI has loaded *and* talked back through the bridge, so the check covers window, webview, page and bridge.

## Consequences — cost we accept

- **Three engines.** The UI runs in WebKit on macOS (as Safari, which the iPhone already uses), Chromium (WebView2) on Windows and WebKitGTK on Linux, and must work in all three.
- **No web push in the window.** Webviews do not offer it. Telegram, Slack, email and ntfy are the channels; push still works in a browser.
- **A separate sign-in.** The window keeps its own cookies, so you sign in once in it as well.
- **Linux coverage.** The window needs GTK ≥ 4.14 and glibc ≥ 2.39: Ubuntu 24.04+, Debian 13+, Fedora 40+. Older systems and 32-bit Raspberry Pi OS get the browser.
- **WebKitGTK's sandbox where it can run.** WebKit isolates each page with bubblewrap, which needs unprivileged user namespaces. Ubuntu (since 24.04) can have AppArmor refuse them, and WebKit then aborts the window. When bubblewrap cannot start there, the window turns that sandbox off (`WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS`) and logs it: it shows Exhale's own pages from the same computer. Everywhere else the sandbox stays on. CI opens the window both ways.
- **The release is heavier.** Two more Linux jobs install GTK headers from Ubuntu's mirrors, retried. Exhale.app is built with cgo.
- **Young as stable.** Wails 3.0 is two weeks old as a stable release. Its CGO-free backend (`purego`) would let us cross-compile everything from one machine, but it is still experimental, so not yet.

## Revisit when

- Wails 3 breaks or stalls: Tauri is plan B with the same design (a window on the local server). Only `internal/desktop/window` and the builds change.
- Wails's `purego` backend leaves experimental: drop the Linux cgo jobs and build `exhale-window` (also for armv7) in GoReleaser.
- People ask for a menu bar or tray icon, native notifications or automatic updates: Wails offers all three.
- We pay for signing (ADR 0029): the same jobs sign the window too.
