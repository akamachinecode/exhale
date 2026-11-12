# 0028. Keep this computer awake, from the web, without cgo

- Status: Accepted
- Date: 2026-11-10
- Amended by: [0032](0032-menu-bar-and-keep-awake-sessions.md) (sessions, a closed lid, battery guards, the menu bar icon)

## Context — what it has to do

People run Exhale on the desktop Mac or Windows PC they already have. When that machine sleeps, reminders are not sent and the buttons in Telegram and Slack stop answering until it wakes up. The install guides said "a Mac that does not sleep" and left it to the system settings, which people forget or never find.

The owner should be able to say, from Exhale's own settings: never let it sleep, or only between two times of day (e.g. 08:00 to 23:00, a range that may cross midnight), and whether the display may still turn off. The setting must be readable and writable by agents like every other one (ADR 0018), and the web must say plainly whether it is in effect and, if not, why.

Constraints: one static binary built with `CGO_ENABLED=0` and cross-compiled for linux, darwin and windows on amd64, arm64 and arm7 (ADR 0002, 0009). A laptop with its lid closed sleeps whatever any program asks; that is out of reach and the web says so.

## Criteria

1. **No cgo**, no new modules: the cross-compiled build stays as it is.
2. **Nothing left behind**: if Exhale crashes or is killed, the machine must be free to sleep again.
3. **Honest status**: in a container, on an unsupported system, or when the OS refuses, the person reads the reason.
4. **One place for the logic**: the schedule (hours, time zone, midnight) is tested once, not per OS.

## Options

**A) A tray or menu-bar app** that holds the assertion. Natural on a desktop, but it is a second program per OS, needs cgo or a GUI toolkit, and leaves the headless and service installs out. Rejected.

**B) cgo bindings** to IOKit (`IOPMAssertionCreateWithName`) on macOS. The cleanest API, but cgo breaks the cross-compiled static build for one feature. Rejected.

**C) Ask the OS with what it already ships**, per platform, behind a four-method interface (`internal/awake.Keeper`):

| OS | How | Dies with Exhale because |
|---|---|---|
| macOS | runs `/usr/bin/caffeinate -i [-d] -w <pid>` | `-w` makes it exit when our process does |
| Windows | `SetThreadExecutionState(ES_CONTINUOUS \| ES_SYSTEM_REQUIRED [\| ES_DISPLAY_REQUIRED])` from kernel32, through `syscall.NewLazyDLL`, on a goroutine locked to its thread until release | the state belongs to the thread and the process |
| Linux | runs `systemd-inhibit --what=idle:sleep --mode=block --who=Exhale --why="Sending reminders" sleep infinity` | `Pdeathsig=SIGTERM`, from a thread kept locked while the helper lives (the signal follows the thread that started it) |
| a container (`/.dockerenv`, `/run/.containerenv`), other systems | not available, with the reason | — |

## Decision

C. A controller in `internal/app` (`KeepAwake`, a supervised service of `exhale serve`) reads the setting (key `keep_awake` of the settings table, JSON) and the household time zone, holds or releases through the Keeper, and sleeps until the next edge of the hours or a change, at most a minute, so a change made by another process (`exhale mcp`) is picked up too. It uses the service clock, so the schedule is tested with a fake keeper and a moving clock. A refused hold is retried every 15 minutes, not every minute; a helper that dies is noticed and started again.

`get_keep_awake` and `set_keep_awake` are ordinary catalog ops: REST, MCP and the assistant get them, writes go through `Service.write` (dry run, audit, event) and can be undone. The web shows it to owners in Settings → General → This computer.

## Consequences — cost we accept

- macOS and Windows behaviour is checked by compiling and by reading the documentation; the tests on Linux take a real lock only when asked (`EXHALE_TEST_AWAKE=1`).
- On Linux, blocking sleep needs polkit's permission, which an active desktop session has and a system service or SSH session usually does not: there the status shows systemd's refusal. A Linux server rarely sleeps; a polkit rule allowing `org.freedesktop.login1.inhibit-block-sleep` for the `exhale` user is the fix. The display option does nothing on Linux: the screen saver belongs to the desktop, not to logind.
- On Windows, `ES_SYSTEM_REQUIRED` does not stop a sleep the person asks for (lid, Start → Sleep), only idle sleep. Same for `caffeinate -i` on macOS.
- Hours follow the household time zone, not the machine's: they are the household's hours.

## Revisit when

- Someone needs it inside a container (it would mean talking to the host's logind over a mounted socket).
- A Windows service install exists: services keep the machine awake the same way, but the docs will need a line.
