# 0033. Updating Exhale with one click

- Status: Accepted
- Date: 2026-11-12
- Amends: [0029](0029-desktop-installers.md) (upgrading meant downloading the installer again), [0016](0016-repository-ci-releases.md) (releases are now also read by Exhale itself)

## Context — what it has to do

Until now a new version reached people only if they went looking: download `Exhale.dmg` or `Exhale-Setup.exe` again, pull the image, install the new package. Most never do, so fixes and features shipped in a release sit unused. The owner asked for the opposite: **when a version is published, one click in Exhale installs it.**

What updating physically involves, per way of installing (ADR 0029):

| Installed as | Who runs the server | Can it write its own program? | What has to restart |
|---|---|---|---|
| macOS `Exhale.app` | a launchd agent, as the person | yes, normally: the person dragged the app into `/Applications` | the server, and the window with its menu bar icon |
| Windows `Exhale.exe` | the person (`HKCU\…\Run`) | yes: `%LOCALAPPDATA%\Programs\Exhale` | the server and the window; a running `.exe` cannot be overwritten |
| A binary (archive, `install.sh` without root) | the person, systemd or a terminal | if its folder is the person's | the server |
| `.deb` / `.rpm` | systemd, as the `exhale` user | no: `/usr/bin` is root's, and the package manager owns it | — |
| Docker | the container | no: the image is replaced from outside | — |

## Criteria

| Criterion | Weight |
|---|---|
| One click, no terminal, where the system allows it | High |
| Never left half-updated: a bad download or a binary that does not start changes nothing | High |
| No infrastructure of ours (ADR 0001): GitHub Releases is the source | High |
| No new module and no cgo in the server (ADR 0002, 0009) | High |
| Every catalog op also works for agents, with a dry run (ADR 0018) | Medium |
| Where it cannot update itself, say exactly what to run | Medium |

## Options

| | **The server updates itself from GitHub Releases** | A separate updater program per OS | Sparkle (macOS) / WinSparkle | Package repositories (apt, dnf, Homebrew, winget) |
|---|---|---|---|---|
| One click in the web | ✅ | ✅, through the server anyway | Its own dialog, outside the web | ❌ terminal or store |
| New dependencies | none: `net/http`, `archive/tar`, `crypto/sha256` | a second binary in every installer | Objective-C / C frameworks, cgo | hosting, signing keys, manifests |
| Linux packages and Docker | says the command to run | same | — | ✅ (the real fix, later) |
| Signature | SHA-256 against the release's `checksums.txt` | same | EdDSA, needs a key we keep | the repository's |

## Decision

**The server checks GitHub for the newest release and, where it may, installs it and restarts.**

- **Checking.** `GET https://api.github.com/repos/akamachinecode/exhale/releases/latest`, a minute after the start and then once a day, cached for 6 hours. Owners see a link in the sidebar ("Exhale 0.5.0 is out") and a card in Settings → General → Updates with the release notes and **Update to 0.5.0**. `--no-update-check` (`EXHALE_UPDATE_CHECK=false`) stops the daily check: only **Check now** asks GitHub then.
- **Two catalog ops.** `get_update` (version, newest release, notes, how this copy was installed, progress) and `apply_update` (owners only, with a dry run). Like `setup_lid_closed`, it changes the computer and not the database, so there is no transaction and no undo: the dry run is the preview. It runs in the background, and `get_update` follows it: downloading (with bytes), installing, restarting, or why it failed.
- **Checking the download.** The file is compared with its line in the release's `checksums.txt` (SHA-256), the same check `install.sh` does. Then the new program must answer `exhale version` with the new version before anything is replaced. A failure leaves the running version untouched and says why. The database needs nothing: a new version already saves `pre-migrate-…db` before it migrates.
- **Installing, per way of installing:**
  - **Binary:** the binary from the archive replaces this one with a rename in the same folder. Then the server stops cleanly and `exec`s the new one: **same process**, so launchd, systemd or the terminal keep following it.
  - **macOS app:** `hdiutil attach` on `Exhale.dmg`, `ditto` of `Exhale.app` next to the installed one, then two renames swap them, and the server `exec`s itself as above. If macOS refuses the swap (App Management, a folder the person cannot write), the disk image opens in Finder: dragging Exhale to Applications finishes it.
  - **Windows app:** once the server has stopped, it runs `Exhale-Setup.exe /S /open`, detached. The installer already stops Exhale, replaces it and, with `/open`, opens it again.
  - **Elsewhere,** the card shows the exact command: `docker compose pull && docker compose up -d`, `sudo dnf install <rpm>`, `curl … && sudo apt install ./<deb>`, or `sh install.sh [--service]` for a binary owned by root.
- **The window follows.** The window and its menu bar icon (ADR 0030, 0032) are a second process. Every 5 seconds it checks whether its program on disk is still the same file. When it is not, it starts itself again once it has exited, visible or in the background as it was. This also covers a package manager replacing `exhale-window`. On Windows the installer closes and reopens it.
- **The web follows.** While an update runs the card asks the server every second, and when the server answers in the new version the page reloads, so the new web UI is loaded.

## Consequences — cost we accept

- **Trust in GitHub's HTTPS.** `checksums.txt` comes from the same release as the file, so the check catches a corrupt or truncated download, not a compromised release. The cosign signature of `checksums.txt` (ADR 0016) is not verified yet: that needs the sigstore library and its trust root.
- **A daily request to GitHub** from every instance, with `exhale/<version>` as User-Agent and nothing else. It can be turned off.
- **macOS App Management** may refuse the swap of an ad hoc signed app. Then the update ends with one drag in Finder, not one click. A Developer ID signature would make the swap reliable.
- **Unverified on real machines when written.** The binary path was tried end to end on Linux, from the button to the new version in the same process. The macOS swap and the Windows installer path were not tried on a Mac or a Windows PC: they must be tried by hand before the release that announces them.
- **Linux packages and Docker** still need a terminal.

## Revisit when

- We sign the apps: then macOS's swap is reliable and Windows SmartScreen stays quiet.
- We verify the cosign bundle of `checksums.txt` before installing.
- An apt/dnf repository exists: the packages update with the system.
- People ask for automatic updates (install at night without a click).
