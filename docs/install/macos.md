# macOS

For a Mac that stays on (a Mac mini, an iMac that does not sleep). Exhale only sends reminders and answers Telegram buttons while the Mac is awake; what came due while it slept is sent when it wakes.

## Install the binary

Download the tarball for your Mac from the [releases page](https://github.com/akamachinecode/exhale/releases): `darwin_arm64` for Apple silicon, `darwin_amd64` for Intel. Then, in the folder with the download and `checksums.txt`:

```sh
shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf exhale_0.1.0_darwin_arm64.tar.gz
sudo mkdir -p /usr/local/bin
sudo install -m 0755 exhale /usr/local/bin/exhale
exhale version
```

The binary is not notarized by Apple. If you downloaded it with a browser, macOS refuses to open it until you remove the quarantine mark:

```sh
xattr -d com.apple.quarantine /usr/local/bin/exhale
```

## Try it

```sh
exhale serve
```

Open `http://127.0.0.1:8484` and create your account. Stop it with Ctrl-C.

Data lives in `~/Library/Application Support/Exhale`, backups in its `backups` folder.

## Run it at login (launchd)

The tarball includes a launchd agent, [`deploy/launchd/io.github.akamachinecode.exhale.plist`](../../deploy/launchd/io.github.akamachinecode.exhale.plist). It starts Exhale when you log in and restarts it if it fails.

```sh
mkdir -p ~/Library/LaunchAgents
cp deploy/launchd/io.github.akamachinecode.exhale.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist
```

| To | Run |
|---|---|
| See the log | `tail -f ~/Library/Logs/Exhale.log` |
| Restart | `launchctl kickstart -k gui/$(id -u)/io.github.akamachinecode.exhale` |
| Stop and disable | `launchctl bootout gui/$(id -u)/io.github.akamachinecode.exhale` |

The agent runs while you are logged in. For a Mac used as a server, turn on automatic login and keep it awake (below).

## Keep the Mac awake

In the web, **Settings → General → This computer** (owners): *Always*, or *Only between two times* (e.g. 08:00 to 23:00, in the household's time zone; a range can cross midnight). Exhale then runs Apple's `caffeinate` while it should be awake, tied to its own process, so the Mac is free to sleep again as soon as Exhale stops. *Let the display sleep* is on by default: only the Mac stays awake, the screen turns off as usual.

A MacBook with its lid closed still sleeps, unless it is on power with an external display. You can also set it once in System Settings → Energy ("Prevent automatic sleeping when the display is off").

## Settings

Edit the `EnvironmentVariables` section of `~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist`, using the names in [Install Exhale](README.md#settings). For example, to reach Exhale from other devices on your network:

```xml
<key>EXHALE_LISTEN</key>
<string>0.0.0.0:8484</string>
```

Then load it again:

```sh
launchctl bootout gui/$(id -u)/io.github.akamachinecode.exhale
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist
```

## Run a command

Stop nothing; commands work while the agent runs:

```sh
exhale backup
exhale user reset-password --email you@example.com
exhale user reset-2fa --email you@example.com
```

## Upgrade

Install the new binary over the old one (the `install` line above) and restart the agent.

## Uninstall

```sh
launchctl bootout gui/$(id -u)/io.github.akamachinecode.exhale
rm ~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist
sudo rm /usr/local/bin/exhale
```

Your data stays in `~/Library/Application Support/Exhale` until you delete that folder.
