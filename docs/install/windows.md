# Windows

Exhale only sends reminders and answers Telegram buttons while the PC is on; what came due while it slept is sent when it wakes. Exhale can keep the PC awake for you (in the web, **Settings → General → This computer**).

## Install

1. Download **`Exhale-Setup.exe`** from the [download page](https://akamachinecode.github.io/exhale/) (or the [releases page](https://github.com/akamachinecode/exhale/releases/latest)). Windows 10 or 11, 64-bit.
2. Open it. Exhale is not signed yet, so **SmartScreen** may say it protected your PC: click **More info → Run anyway**.
3. That is all: it installs for you (no administrator needed), starts Exhale and opens it in your browser at `http://127.0.0.1:8484`, where you create your account.

From then on Exhale starts when you sign in to Windows, without a window: it lives in your browser. To open it, choose **Exhale** in the Start menu.

| | |
|---|---|
| Program | `%LOCALAPPDATA%\Programs\Exhale` |
| Data | `%LOCALAPPDATA%\Exhale` (backups in its `backups` folder) |
| Log | `%LOCALAPPDATA%\Exhale\exhale.log` |
| Upgrade | Download the new `Exhale-Setup.exe` and open it: it stops Exhale, replaces it and opens the new version |
| Stop it starting at sign-in | Task Manager → Startup apps → Exhale → Disable |
| Uninstall | Settings → Apps → Installed apps → Exhale → Uninstall. Your data stays until you delete `%LOCALAPPDATA%\Exhale` |

## Settings

Exhale reads the same `EXHALE_*` environment variables as on every system ([Install Exhale](README.md#settings)). Set them as user environment variables (Start → "Edit environment variables for your account") and sign out and in again. For example, `EXHALE_LISTEN=0.0.0.0:8484` lets other devices on your network open it (allow it in Windows Firewall when asked).

## Without the installer

The release also has `exhale_<version>_windows_amd64.zip` with `exhale.exe`, a console program: run `exhale.exe serve` in a terminal, or `exhale.exe open` to start it in the background and open the browser. It does not add itself to sign-in.

## Run a command

`Exhale.exe` has no console, so commands print nothing with it. Use `exhale.exe` from the zip above in PowerShell; it finds the same data:

```powershell
.\exhale.exe backup
.\exhale.exe user reset-password --email you@example.com
```
