# 0001. Distribution: one self-hosted server, Docker image + native service, no cloud

- Status: Accepted
- Date: 2026-11-04

## Context — what it has to do

The core feature (editing a subscription from inside a Telegram/Slack reminder) needs **something that answers** when the button is pressed, possibly hours after the reminder was sent. Reminders must also go out on time. That requires a process that is **always on**.

The project must be self-hosted, run on Linux, macOS and Windows, and must not depend on any infrastructure we operate.

## Options

| | Desktop app first (Electron/Tauri) | Server (Docker + native binary) | Cloud relay run by us |
|---|---|---|---|
| Reliable reminders | ❌ Only while the computer is awake | ✅ | ✅ |
| Mobile access | ❌ Needs something else anyway | ✅ Web/PWA | ✅ |
| Distribution effort | High (macOS notarization, Windows signing, auto-update) | Low (images + binaries on GitHub Releases) | — |
| Self-hosted / no central infra | ✅ | ✅ | ❌ Breaks the principle |

## Decision

- **One server process** containing the web UI, the API and the chat bots.
- Shipped as a **Docker image** (main channel, homelab audience) and as a **native binary that installs itself as an OS service** (systemd, launchd, Windows service) for users without Docker.
- **Outbound connections only:** Telegram via long polling, Slack via Socket Mode. No open ports, no public URL, no certificates needed. Works behind any NAT.
- **No cloud relay**, ever: we do not run, pay for or charge for any infrastructure.
- **Laptop mode:** an opt-in keep-awake mode, intended for when the laptop is plugged in. Its limits are documented (on macOS, closing the lid still sleeps).
- Later and optional: a tray app that only starts the binary and opens the web UI.

## Consequences — cost we accept

- Instant button responses require an always-on machine: home server, NAS, Raspberry Pi, VPS or a desktop that never sleeps.
- On a sleeping laptop, Telegram keeps updates for up to 24 h and they are applied on wake. **Slack does not queue**: clicks made while the server is down fail.

## Revisit when

Strong demand from desktop-only users → build the optional tray wrapper.
