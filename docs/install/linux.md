# Linux

Two ways to get the same result: Exhale running as a systemd service under its own user.

- [Packages](#packages-deb-and-rpm) for Debian, Ubuntu, Raspberry Pi OS, Fedora and RHEL.
- The [install script](#install-script) for any other Linux with systemd.

Pick the file for your CPU: `amd64` (most PCs and servers), `arm64` (Raspberry Pi 4 and 5 with a 64-bit system) or `armv7` (32-bit Raspberry Pi OS). `uname -m` tells you: `x86_64` is `amd64`, `aarch64` is `arm64`, `armv7l` is `armv7`.

## Packages (deb and rpm)

Download the package from the [download page](https://akamachinecode.github.io/exhale/) (or the [releases page](https://github.com/akamachinecode/exhale/releases/latest)) and open it with your software center, or install it from a terminal:

```sh
# Debian, Ubuntu, Raspberry Pi OS
sudo apt install ./exhale_linux_amd64.deb

# Fedora, RHEL
sudo dnf install ./exhale_linux_amd64.rpm
```

The package creates the `exhale` user, installs the service, starts it and enables it at boot. It also adds **Exhale to your apps**: opening it shows Exhale in its own window (on amd64 and arm64, with GTK 4 and WebKitGTK 6.0: Ubuntu 24.04, Debian 13, Fedora 40 or newer). On older systems and on 32-bit Raspberry Pi OS it opens in your browser instead, and `exhale open --browser` always does. Closing the window does not stop Exhale. On a server, check the log for the address:

```sh
sudo journalctl -u exhale -n 20
```

On a new install the log also shows the one-time setup code, needed only when the wizard is opened from another machine.

The package also lets the service keep the computer awake when you ask for it in the web (a polkit rule, `/usr/share/polkit-1/rules.d/50-exhale.rules`).

## Install script

```sh
curl -fsSLO https://raw.githubusercontent.com/akamachinecode/exhale/main/scripts/install.sh
less install.sh                 # read it first
sh install.sh --service
sudo systemctl start exhale
```

The script downloads the release tarball and `checksums.txt` from GitHub, checks the SHA-256, and only then copies the binary to `/usr/local/bin`. It asks for `sudo` when it needs root.

| Option | Effect |
|---|---|
| `--service` | Also creates the `exhale` user, `/var/lib/exhale`, `/etc/exhale/exhale.env` and the systemd service |
| `--version 0.1.0` | Installs that release instead of the latest |
| `--bin-dir DIR` | Puts the binary somewhere else |

Without `--service` you get only the binary. Start it yourself with `exhale serve`; data then lives in `~/.local/share/exhale`.

Running the script again upgrades Exhale. It keeps your settings file and restarts the service only if the binary changed.

## First start

Exhale listens on `127.0.0.1:8484`, which only answers on the server itself.

- **Server with a screen or SSH:** open a tunnel from your computer, `ssh -L 8484:127.0.0.1:8484 you@server`, and browse to `http://127.0.0.1:8484`.
- **From your network:** set `EXHALE_LISTEN=0.0.0.0:8484` in `/etc/exhale/exhale.env`, restart, open the port in your firewall if you have one, and browse to `http://<server>:8484`. The wizard asks for the setup code from the log (`sudo journalctl -u exhale | grep setup_code`).

For access from outside your network see [HTTPS and remote access](https.md).

## Where things live

| What | Where |
|---|---|
| Binary | `/usr/bin/exhale` (package) or `/usr/local/bin/exhale` (script) |
| App launcher | `/usr/share/applications/io.github.akamachinecode.exhale.desktop` (package) |
| Window | `/usr/bin/exhale-window` (amd64 and arm64 packages) |
| Service | `exhale.service`, running as user `exhale` |
| Settings | `/etc/exhale/exhale.env` (readable by root and the service only) |
| Data | `/var/lib/exhale` (the database `exhale.db`) |
| Backups | `/var/lib/exhale/backups` |
| Logs | The journal: `journalctl -u exhale` |

## Change a setting

Edit `/etc/exhale/exhale.env` (one `NAME=value` per line; the file lists them all) and restart:

```sh
sudoedit /etc/exhale/exhale.env
sudo systemctl restart exhale
```

The service is sandboxed: it can write only to `/var/lib/exhale`. To keep backups in another place, allow that directory and make it writable by the `exhale` user:

```sh
sudo install -d -o exhale -g exhale -m 0750 /mnt/backups/exhale
sudo systemctl edit exhale
```

```ini
[Service]
ReadWritePaths=/mnt/backups/exhale
```

Then set `EXHALE_BACKUP_DIR=/mnt/backups/exhale` and restart. If the directory is not allowed, the log says `automatic backup failed … read-only file system` and Exhale keeps running.

The service has no privileges, so it cannot listen on ports below 1024. For port 443, put a [reverse proxy](https.md) in front.

## Run a command

Commands such as `exhale backup`, `exhale setup-code`, `exhale user reset-password` or `exhale user reset-2fa` (turns off two-factor sign-in for a lost phone) must run as the service user and on the service's data:

```sh
sudo -u exhale env EXHALE_DATA_DIR=/var/lib/exhale exhale user reset-password --email you@example.com
sudo -u exhale env EXHALE_DATA_DIR=/var/lib/exhale exhale backup
```

Do not run them as root: files created by root in `/var/lib/exhale` cannot be written by the service afterwards.

## Upgrade

- **Package:** download the new package and install it the same way. A running service is restarted.
- **Script:** run `sh install.sh --service` again.

There is no apt or dnf repository yet, so upgrades are not automatic.

On the first start after an upgrade Exhale updates its database by itself. Before doing so it saves a copy in `/var/lib/exhale/backups/pre-migrate-…db`.

## Uninstall

```sh
sudo apt remove exhale          # or: sudo dnf remove exhale
```

Installed with the script:

```sh
sudo systemctl disable --now exhale
sudo rm /etc/systemd/system/exhale.service /usr/local/bin/exhale
sudo systemctl daemon-reload
```

Uninstalling never deletes your data. `/var/lib/exhale` and the `exhale` user stay, and installing again picks them up. To delete everything for good:

```sh
sudo rm -rf /var/lib/exhale /etc/exhale
sudo userdel exhale
```

## Verify a download

Each release has a `checksums.txt`. The install script checks it for you; by hand:

```sh
sha256sum --check --ignore-missing checksums.txt
```

`checksums.txt` is signed with [cosign](https://docs.sigstore.dev/) in the release workflow (keyless, no key to trust). To check that it was produced by this repository's workflow:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/akamachinecode/exhale/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Without systemd

Install the binary (`sh install.sh` without `--service`) and start `exhale serve` from your init system as an unprivileged user, with `EXHALE_DATA_DIR` pointing to a directory that user owns. Exhale stops cleanly on `SIGTERM` within 8 seconds.
