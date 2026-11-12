# Install Exhale

Exhale is one program with one data folder. It needs a machine that stays on: a home server, a NAS, a Raspberry Pi, a small VPS or a Mac that does not sleep. Reminders go out from that machine and the buttons in Telegram are answered by it.

It does not need a public address. Telegram is reached with outgoing connections only, so Exhale works behind any home router with no open ports.

## Pick a method

The quickest way is the **[download page](https://akamachinecode.github.io/exhale/)**: it offers the installer for your computer. Open it and Exhale starts, opens in its own window and starts again with your computer.

| You have | Use | Guide |
|---|---|---|
| A Mac | `Exhale.dmg`: drag Exhale to Applications and open it | [macOS](macos.md) |
| Windows 10 or 11 | `Exhale-Setup.exe`: open it, no administrator needed | [Windows](windows.md) |
| Debian, Ubuntu, Raspberry Pi OS, Fedora, RHEL | The `.deb` or `.rpm` package: a system service that starts at once and at every boot | [Linux](linux.md#packages-deb-and-rpm) |
| Docker (NAS, homelab, VPS) | The container image | [Docker](docker.md) |
| Another Linux with systemd | The install script: same service, no package manager | [Linux](linux.md#install-script) |

All of them run the same binary with the same settings. The same instance serves a home or a small team: see [Exhale for a team](../guides/teams.md).

> **Releases.** Installers, packages, images and tarballs are published on the [releases page](https://github.com/akamachinecode/exhale/releases) with each version, with a signed checksum file and build provenance. To build from source instead: `mise install && mise run build` produces `bin/exhale`.

## After installing

1. **Open the web** (the installers open it for you; otherwise `http://<server>:8484`) and create your account. Opened from another machine, the wizard asks for a one-time setup code: it is printed in Exhale's log at start.
2. **Add your Telegram bot** from the web (Settings → Telegram). The page guides you through @BotFather.
3. **Decide how you reach it.** At home, plain `http://` on your network is fine. From outside, use Tailscale or HTTPS: [HTTPS and remote access](https.md). Never forward the plain HTTP port to the internet.
4. **Check your backups.** Exhale copies its database once a day and keeps 14 copies. Put them on another disk: [Backups](backups.md).

## Keep the computer awake

On a desktop Mac or Windows PC, let Exhale keep the machine from sleeping: in the web, **Settings → General → This computer** (owners), or from Exhale's icon in the menu bar (the tray on Windows and Linux).

- **A rule**: *Always*, or *Only between two times* (e.g. 08:00 to 23:00, in the household's time zone; a range can cross midnight).
- **Now, for a while**: 1, 2 or 8 hours, until a time, or until you stop it, with *+1 h* to make it longer. A session started before the computer restarts comes back with the time it had left.
- **Keep going with the lid closed**: a laptop stays awake when you close it. On a Mac this needs your administrator password once (see [macOS](macos.md#keep-the-mac-awake)).
- **Only while plugged in**, and **stop on battery at** a level (20 % unless you change it): on battery, the laptop sleeps as usual.

The display still turns off unless you untick *Let the display sleep*. Exhale stops holding the machine when it exits, and lets a closed laptop sleep at once when it stops holding it.

- **Windows**: Exhale asks Windows itself (`SetThreadExecutionState`), nothing to install. With the lid option, it sets your power plan's "When I close the lid" to *Do nothing* while it holds, and puts back what it was. If the laptop is already closed when that happens, it sleeps at the next idle timeout.
- **macOS**: see [macOS](macos.md#keep-the-mac-awake).
- **Linux**: Exhale takes a `systemd-inhibit` lock (idle and sleep, and the lid switch with the lid option). A desktop session may; a system service usually needs a polkit rule, and the web shows the refusal until then. Most Linux servers never sleep anyway.
- **Docker**: not available: the container cannot keep its host awake. Use the host's own settings.

A laptop kept awake in a closed bag gets hot. Use the lid option on a desk, and keep the battery level on.

### The menu bar icon

The desktop apps put an Exhale icon in the menu bar (macOS) or the tray (Windows, Linux). It shows your next charges and what the next 30 days cost, and starts, extends or stops keeping the computer awake. It starts at login, and stays when you close the window; *Quit* in its menu closes only the icon: reminders keep going out.

On GNOME (Fedora, Debian), tray icons need the *AppIndicator and KStatusNotifierItem Support* extension (`gnome-shell-extension-appindicator`); Ubuntu has it on already, KDE needs nothing.

## Settings

Every setting is a flag of `exhale serve` and an environment variable. Flags win over the environment.

| Variable | Flag | Default | What it does |
|---|---|---|---|
| `EXHALE_DATA_DIR` | `--data-dir` | per system, see each guide | Where the database and backups live |
| `EXHALE_LISTEN` | `--listen` | `127.0.0.1:8484` | Address to listen on. `0.0.0.0:8484` answers on every network interface |
| `EXHALE_SECURE_COOKIES` | `--secure-cookies` | off | Set to `true` when people use `https://`. With plain `http://` leave it off, or nobody can sign in |
| `EXHALE_BACKUP_DIR` | `--backup-dir` | `<data dir>/backups` | Where the daily backups go |
| `EXHALE_BACKUP_KEEP` | `--backup-keep` | `14` | Automatic backups to keep. `0` keeps all |
| `EXHALE_NO_BACKUPS` | `--no-backups` | off | Turns the daily backups off |
| `EXHALE_UPDATE_CHECK` | `--[no-]update-check` | on | Checks GitHub once a day for a new version, shown to owners in Settings → Updates. `false`: only **Check now** asks |
| `EXHALE_TELEGRAM_TOKEN` | `--telegram-token` | none | Bot token. Usually set from the web instead; set here, the web cannot change it |
| `EXHALE_LOG_LEVEL` | `--log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `EXHALE_LOG_FORMAT` | `--log-format` | `text` | `text` or `json` |
| `EXHALE_TRUST_LOCAL` | `--trust-local` | off | Development only: requests from the same machine act as the owner without signing in. Never set it on a server behind a reverse proxy |

## Health

`GET /healthz` answers `ok` with status 200 while the server is up. Point your monitor (Uptime Kuma, a cron job with `curl -f`) at it.
