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

On a desktop Mac or Windows PC, let Exhale keep the machine from sleeping: in the web, **Settings → General → This computer** (owners), choose *Always* or *Only between two times*. The display still turns off unless you untick *Let the display sleep*. Exhale stops holding the machine when it exits.

- **Windows**: Exhale asks Windows itself (`SetThreadExecutionState`), nothing to install. Sleep you ask for (Start → Sleep, closing a laptop lid) still happens.
- **macOS**: see [macOS](macos.md#keep-the-mac-awake).
- **Linux**: Exhale takes a `systemd-inhibit` lock. A desktop session may; a system service usually needs a polkit rule, and the web shows the refusal until then. Most Linux servers never sleep anyway.
- **Docker**: not available: the container cannot keep its host awake. Use the host's own settings.

A laptop with its lid closed sleeps whatever Exhale asks.

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
| `EXHALE_TELEGRAM_TOKEN` | `--telegram-token` | none | Bot token. Usually set from the web instead; set here, the web cannot change it |
| `EXHALE_LOG_LEVEL` | `--log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `EXHALE_LOG_FORMAT` | `--log-format` | `text` | `text` or `json` |
| `EXHALE_TRUST_LOCAL` | `--trust-local` | off | Development only: requests from the same machine act as the owner without signing in. Never set it on a server behind a reverse proxy |

## Health

`GET /healthz` answers `ok` with status 200 while the server is up. Point your monitor (Uptime Kuma, a cron job with `curl -f`) at it.
