# 0029. Desktop installers and a download page

- Status: Accepted
- Date: 2026-11-11
- Amends: [0001](0001-distribution-model.md) (the "later and optional" desktop part), [0009](0009-runtime-and-system.md) (installing as a service)

## Context — what it has to do

Until now, installing Exhale on a Mac or a Windows PC meant a terminal: unpack a tarball, copy a binary, write a launchd agent or start `exhale.exe serve` by hand. Linux had `.deb`/`.rpm` packages, but they left the service stopped. The goal is the shortest path there is: **download the installer, open it, done**, on macOS, Windows and Linux, from a page that is not GitHub's release list.

What an installed Exhale physically has to do on a personal computer:

1. Put the program in a stable place.
2. Run `exhale serve` in the background, **as the person** (their data folder; keep-awake needs their session on macOS), from every login, restarted if it crashes, with no terminal window.
3. Give a door: an icon in Applications, the Start menu or the app launcher that opens Exhale.
4. Upgrade by installing the new version over the old one, and uninstall without losing data.
5. Open without the system calling it malware. That depends on signing, not on code.

## Criteria

| Criterion | Weight |
|---|---|
| Steps for the person: download, open, done | High |
| No infrastructure of ours and no paid service to ship (ADR 0001) | High |
| One binary, `CGO_ENABLED=0`, cross-compiled (ADR 0002, 0009) | High |
| Each installer is tried on a real machine before release | Medium |
| Room for signing and notarization later, without redesign | Medium |

## Options

| | Per-OS native installers around the same binary | A desktop shell (Tauri/Electron) | Package managers only (Homebrew, winget, apt repo) | GoReleaser Pro (dmg, msi, app bundles) |
|---|---|---|---|---|
| Steps for the person | Download, open | Download, open | Terminal first | Download, open |
| Stays one Go binary | ✅ | ❌ second runtime, a window to maintain | ✅ | ✅ |
| Cost | None | Large | A tap repo, tokens, manifests per channel | A paid licence |
| Background service | The binary registers itself | Same problem, plus the shell | `brew services`; winget has none | Same as first column |

`kardianos/service` (ADR 0009) installs system services: root, `LocalSystem` on Windows. A desktop needs a **per-user** login item, which it does not cover.

## Decision

**Native installers around the same binary, which knows how to install and open itself.**

- **`exhale open`** (and the desktop builds opened with no arguments) checks `GET /healthz`, starts the server in the background if it does not answer, waits for it and opens the browser. `/healthz` now sends `X-Exhale-Version`, so an upgraded app restarts an older server. Errors show a native dialog (macOS `osascript`, Windows `MessageBox`, Linux `notify-send`), translated with the chat catalogs.
- **macOS: `Exhale.dmg`** with `Exhale.app` (universal: Apple silicon and Intel; `LSUIElement`, no Dock icon). Each time it is opened, it writes its launchd agent (`~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist`, `RunAtLoad`, restarted on failure, `AssociatedBundleIdentifiers` so Login Items shows "Exhale" with its icon) and loads or reloads it. Opening a newer app restarts the server: that is the upgrade. Opened from the disk image or from a read-only App Translocation copy, it asks to be moved to Applications first. Built on a macOS runner: `lipo`, `iconutil`, an ad hoc `codesign` of the whole bundle (so Gatekeeper offers "Open Anyway" instead of calling it damaged), `hdiutil`.
- **Windows: `Exhale-Setup.exe`**, a one-click NSIS installer, cross-built with `makensis` (Homebrew on a macOS runner, so a release does not wait on Ubuntu's package mirrors): per-user (`%LOCALAPPDATA%\Programs\Exhale`, no administrator), Start menu entry, an entry in Settings → Apps with an uninstaller, then it opens Exhale. `Exhale.exe` is built with `-H windowsgui` (no console window), logs to `%LOCALAPPDATA%\Exhale\exhale.log`, and adds itself to `HKCU\…\Run` when opened. The installer stops a running Exhale before replacing it; the uninstaller removes the sign-in entry and keeps the data.
- **Linux: the `.deb` and `.rpm`** now start the service on install, add Exhale to the app launcher (`exhale.desktop` runs `exhale open`) with its icon, and ship a polkit rule that lets the `exhale` user take the sleep lock keep-awake needs (ADR 0028). Still a system service: it runs without anyone logged in, which is what a reminder server wants.
- **Stable file names** (`Exhale.dmg`, `Exhale-Setup.exe`, `exhale_linux_<arch>.deb|rpm`), so `releases/latest/download/<name>` always works.
- **CI tries them before publishing.** `.github/workflows/installers.yml` builds both installers, mounts the dmg, copies the app to `/Applications` and opens it on a Mac runner, and installs, opens and uninstalls the setup on a Windows runner, checking the server answers in the right version. The release workflow calls it, and GoReleaser adds both files to the release and to the signed `checksums.txt`. On pull requests the installers stay as downloads of the run.
- **A download page** in `site/`, published by GitHub Pages (`pages.yml`) at `akamachinecode.github.io/exhale`: picks the visitor's system, English and Spanish, version from the GitHub API, and the first-open instructions for unsigned apps. No build step and nothing third-party at runtime besides that API call.
- Still **no tray icon**: the web is the interface (ADR 0001).

## Consequences — cost we accept

- **Unsigned.** The first time, macOS says it cannot verify the developer (System Settings → Privacy & Security → Open Anyway) and Windows SmartScreen asks for "More info → Run anyway". The download page says so next to each button. An Apple Developer ID (99 $/year) turns the ad hoc signature into a real one plus notarization in the same macOS job; Windows code signing likewise.
- **Uninstalling on a Mac** is two gestures: turn Exhale off in Login Items and drag the app to the Trash. The agent file stays behind, harmless, until the app is installed again.
- On Windows only amd64 is built; Windows on ARM runs it emulated.
- A server of another version on Windows is not restarted by `Exhale.exe` (it cannot stop it gracefully): the installer stops it.
- Two installers built outside GoReleaser, in shell scripts under `deploy/desktop/`, that we maintain.

## Revisit when

- We pay for signing: add the Developer ID, notarization and the Windows certificate to the existing jobs.
- People ask for a menu bar or tray icon (status, quit), or for automatic updates.
- Homebrew, winget or an apt/dnf repository is requested: they can point at these same files.
