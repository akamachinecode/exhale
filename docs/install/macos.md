# macOS

Exhale only sends reminders and answers Telegram buttons while the Mac is awake; what came due while it slept is sent when it wakes. Exhale can keep the Mac awake for you ([below](#keep-the-mac-awake)).

## Install the app

1. Download **`Exhale.dmg`** from the [download page](https://akamachinecode.github.io/exhale/) (or the [releases page](https://github.com/akamachinecode/exhale/releases/latest)). One app for Apple silicon and Intel, macOS 12 or later.
2. Open it and drag **Exhale** to **Applications**.
3. Open Exhale from Applications. The first time, macOS says it cannot verify the developer (Exhale is not signed by Apple yet): click **Done**, open **System Settings → Privacy & Security**, scroll to Security and click **Open Anyway**, then **Open**. Only the first time.

Exhale starts in the background and opens in its own window, where you create your account. From then on it starts when you log in: macOS lists it in **System Settings → General → Login Items** as Exhale. Opening the app again brings its window back. Closing the window does not stop Exhale: reminders keep going out.

Links to other sites (Telegram, a service's page) open in your browser, and downloads ask where to save them. If you prefer the browser, Exhale is also at `http://127.0.0.1:8484`.

| | |
|---|---|
| Data | `~/Library/Application Support/Exhale` (backups in its `backups` folder) |
| Log | `~/Library/Logs/Exhale.log` (also in Console.app) |
| Upgrade | Download the new `Exhale.dmg`, drag Exhale to Applications again (Replace) and open it: the running server is restarted in the new version |
| Stop it starting at login | Turn Exhale off in Login Items |
| Uninstall | Turn it off in Login Items and drag Exhale to the Trash. Your data stays until you delete its folder. If you allowed the lid option: `sudo pmset disablesleep 0 && sudo rm /etc/sudoers.d/exhale` |

Opening Exhale straight from the disk image does not work: it asks you to move it to Applications first, because it would stop working when the image is ejected.

## Without the app: binary and launchd

For a Mac used as a server, or to manage it from the terminal. Download the tarball for your Mac from the [releases page](https://github.com/akamachinecode/exhale/releases): `darwin_arm64` for Apple silicon, `darwin_amd64` for Intel. Then, in the folder with the download and `checksums.txt`:

```sh
shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf exhale_0.1.0_darwin_arm64.tar.gz
sudo mkdir -p /usr/local/bin
sudo install -m 0755 exhale /usr/local/bin/exhale
exhale version
```

If you downloaded it with a browser, macOS refuses to run the binary until you remove the quarantine mark:

```sh
xattr -d com.apple.quarantine /usr/local/bin/exhale
```

`exhale serve` runs it in the terminal (stop it with Ctrl-C); `exhale open` starts it in the background if needed and opens the browser.

### Run it at login (launchd)

The tarball includes a launchd agent, [`deploy/launchd/io.github.akamachinecode.exhale.plist`](../../deploy/launchd/io.github.akamachinecode.exhale.plist). It starts Exhale when you log in and restarts it if it fails. It has the same name as the app's agent: use one or the other.

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

In the web, **Settings → General → This computer** (owners), or from Exhale's icon in the menu bar: *Always*, *Only between two times* (e.g. 08:00 to 23:00, in the household's time zone; a range can cross midnight), or *now* for 1, 2 or 8 hours, until a time or until you stop it. Exhale then runs Apple's `caffeinate` while it should be awake, tied to its own process, so the Mac is free to sleep again as soon as Exhale stops. *Let the display sleep* is on by default: only the Mac stays awake, the screen turns off as usual.

### With the lid closed

`caffeinate` cannot stop a closed MacBook from sleeping; `pmset disablesleep 1` can, and it needs root. Turn on *Keep going with the lid closed* and press *Allow on this Mac*: macOS asks for an administrator password, on the Mac that runs Exhale, once. Exhale then installs `/etc/sudoers.d/exhale`, a rule that lets your user run exactly two commands without a password, nothing else:

```
#501 ALL=(root) NOPASSWD: /usr/bin/pmset disablesleep 1, /usr/bin/pmset disablesleep 0
```

From then on Exhale turns lid sleep off while it keeps the Mac awake, and back on when it stops, when the battery reaches its level (20 % unless you change it) or, with *Only while plugged in*, when you unplug it. If the lid is closed at that moment, the Mac goes to sleep at once. If Exhale is stopped by force, it turns lid sleep back on the next time it starts.

To undo it all by hand:

```sh
sudo pmset disablesleep 0
sudo rm /etc/sudoers.d/exhale
```

## Settings

Exhale.app writes its agent again each time it is opened, so settings added to it do not last: for custom settings use the agent from the tarball. With that agent, edit the `EnvironmentVariables` section of `~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist`, using the names in [Install Exhale](README.md#settings). For example, to reach Exhale from other devices on your network:

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

## Upgrade and uninstall (binary)

Install the new binary over the old one (the `install` line above) and restart the agent. To remove it:

```sh
launchctl bootout gui/$(id -u)/io.github.akamachinecode.exhale
rm ~/Library/LaunchAgents/io.github.akamachinecode.exhale.plist
sudo rm /usr/local/bin/exhale
```

Your data stays in `~/Library/Application Support/Exhale` until you delete that folder.
