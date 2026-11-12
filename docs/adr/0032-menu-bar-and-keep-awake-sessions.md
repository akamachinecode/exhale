# 0032. A menu bar icon, and keeping a closed laptop awake

- Status: Accepted
- Date: 2026-11-12
- Amends: [0028](0028-keep-awake.md) (said a closed lid was out of reach; had only a standing rule), [0030](0030-desktop-window.md) (closing the window ended the window process)

## Context — what it has to do

The owner uses Exhale on a MacBook and asked for two things they know from menu bar utilities such as Vorssaint (open source, GPL-3.0; we take its ideas, not its code):

1. **An icon in the menu bar** (the tray on Windows and Linux) that shows the next charges and controls "keep this computer awake" without opening the window.
2. **Keep awake like a caffeine app**: for a while (1 h, 2 h, 8 h, until a time, until stopped, and longer if needed), **with the lid closed**, only while plugged in, never down to an empty battery. A session started before a restart comes back with the time it had left.

ADR 0028 said a closed lid sleeps whatever a program asks. That is true of the hold it uses (`caffeinate -i`, `SetThreadExecutionState`, an idle lock), not of every switch the systems have:

| System | Switch for the lid | Needs | Outlives Exhale |
|---|---|---|---|
| macOS | `pmset disablesleep 1` | root | yes |
| Linux | `systemd-inhibit --what=handle-lid-switch` | an active session (polkit), like the idle lock | no |
| Windows | the power plan's lid action set to "do nothing" (`powercfg … LIDACTION 0`) | nothing: the plan is the person's | yes |

Physically, a laptop kept awake in a bag gets hot and its battery runs out. So the dangerous part is not holding the lid. It is **letting go of it**, every time and even after a crash. Also, macOS does not look at the lid again when `disablesleep` goes back to 0, so a laptop that is already closed stays awake.

## Criteria

| Criterion | Weight |
|---|---|
| A closed laptop is never left awake by mistake (crash, low battery, end of a session) | High |
| No cgo and no new module in the server (ADR 0028, 0009) | High |
| One place for the logic: rule, session and battery tested once, not per OS | High |
| Everything the icon does is an ordinary catalog op (agents, web, chat get it too) | High |
| The icon reads the server; it never holds state of its own | Medium |

## Decision

### Keeping awake: a rule, a session on top, and guards

- **The setting** (`keep_awake`) keeps its standing rule (off, always, between two times) and gains `lidClosed`, `onlyOnPower` and `batteryFloor` (default 20 %), plus a **session**: `{active, until}`, where an empty `until` lasts until stopped. The new op `keep_awake_session` starts it for minutes, until a time of day, or until stopped, extends it, or stops it. Like every write it goes through `Service.write`, so it has a dry run and an audit entry, and it can be undone.
- **A restart keeps the session's time.** When the server stops (a restart, an update), it saves how long the session had left (`left`), and the next start gives that time back. A crash leaves `until` as it was, so the session ends at the time it would have.
- **Battery.** Every minute the controller reads the power source with the system's own tools (`pmset -g batt`, `/sys/class/power_supply`, `GetSystemPowerStatus`). On battery it pauses when `onlyOnPower` is set or the charge is at or below the floor, and the laptop may sleep. A desktop has no battery and is never paused.
- **The lid** (`internal/awake.Lid`) is held only while the system is held:
  - **macOS:** `sudo -n /usr/bin/pmset disablesleep 1|0`. A sudoers rule that allows exactly those two commands is installed once by `setup_lid_closed`. That op shows macOS's own administrator dialog (`osascript … with administrator privileges`) on the Mac that runs Exhale. The server is a launchd agent in the person's session, so the dialog reaches them. The rule names the user by uid, and `visudo -c` checks it before it is put in place.
  - **Linux:** a second `systemd-inhibit` lock on `handle-lid-switch`, which dies with Exhale.
  - **Windows:** the lid action of the current plan, set to 0 on AC and DC. The previous values are kept so they can be put back.
- **Letting go.**
  - **The marker.** Before turning the macOS or Windows switch on, the server writes a marker in the data directory, and removes it only once the switch is off again. Each start runs `Recover`, which puts back a switch that a crash left on.
  - **Sleeping a closed lid.** When the hold ends and the lid is already closed, the server puts the machine to sleep: `pmset sleepnow` on macOS (the lid is read with `ioreg`), `systemctl suspend` on Linux after 3 s, unless the machine already slept in between. Windows gives a program no way to read the lid, so a laptop closed at that moment sleeps at the next idle timeout.

### The menu bar icon

- **Who holds it:** the window process (ADR 0030), which now stays alive when the window closes. Closing hides the window; on macOS the Dock icon goes away while no window shows. "Quit" ends only the icon: the server keeps sending reminders. The icon starts at login, without a window (`exhale open --background`), next to the server's own login item.
- **What it shows:** a native menu, not a web popover. Linux trays do not tell an app where the icon is, so a popover could not be placed. The menu has:
  - the next charges, already written in the member's language;
  - what the next 30 days cost;
  - how many past charges wait for confirmation;
  - the keep-awake state, and actions to start, extend or stop a session and to toggle the lid and plugged-in options.

  It comes from one new read op, `glance`, made for small clients: the icon formats no money and computes no dates.
- **How it reads the server:** with an API token for the owner that the server keeps in `<data>/desktop-token` (0600), and replaces when it is revoked. Whoever can read that file can already read the database next to it, so the token adds no new trust. When the server runs as a system service the person cannot read, the menu offers only to open Exhale.

## Consequences — cost we accept

- **Root on a Mac, once.** The lid needs a sudoers rule. It is narrow (two exact commands) but it stays when Exhale is removed: the uninstall instructions remove `/etc/sudoers.d/exhale`.
- **A switch that outlives Exhale.** If Exhale is deleted while the lid is held (macOS, Windows), nothing puts it back. The docs say how: `sudo pmset disablesleep 0`, or the lid action in Power Options.
- **Heat.** A closed laptop kept awake in a bag gets hot. The battery floor is on by default, and the lid only stays awake once the owner has turned that option on.
- **Polling.** The battery is read every minute, so a laptop can run a minute below its floor.
- **GNOME** shows no tray icons without the AppIndicator extension (Fedora does not ship it enabled; Ubuntu does). Without it there is no icon, and the window still works.
- **Checked in CI and by reading the documentation:** the macOS and Windows switches. The parsers of `pmset`, `ioreg` and `powercfg` output are unit-tested on every system. The real switches are tried by hand on a real machine before each release.

## Revisit when

- Exhale gets a signed helper tool on macOS (SMAppService daemon): it would replace the sudoers rule.
- Wails offers a popover attached to the tray icon on all three systems: a small web view could then replace the native menu.
- People ask for more triggers (while an app runs, with an external display): they belong in the same controller, as more guards.
