# 0007. Channels: go-telegram/bot, slack-go, go-mail

- Status: Accepted
- Date: 2026-11-06

## Decision

| Piece | Choice | Evidence (Nov 2026) |
|---|---|---|
| ② Telegram | **`go-telegram/bot`** | Zero dependencies; Bot API 10.3; v1.27.0 (Sep 2026); 30 contributors |
| ③ Slack | **`slack-go/slack`**, version pinned and isolated in the adapter | De facto Go SDK (official SDKs exist only for JS, Python and Java); Socket Mode + Block Kit; only `gorilla/websocket`; still 0.x, so API breaks are possible |
| ④ Email | **`wneessen/go-mail`** | SMTP edge cases (STARTTLS, auth, encoding) are not worth writing by hand |
| ④ ntfy, Gotify, Pushover, Discord webhooks, generic webhooks | **Own, `net/http`** | Each one is a single POST, sharing the scheduler's outbox and retries |

**Slack ships in v1.0 as milestone 2**, after Telegram and the web. Building two real adapters (plus the fake test channel) keeps the action layer ⑥ honestly channel-agnostic.

## Options rejected

- **telego:** pulls sonic (architecture-specific assembly JSON) and fasthttp.
- **telebot:** last release in June 2024.
- **telegram-bot-api:** abandoned in 2021.
- **shoutrrr:** its own retry model conflicts with our outbox, and it brings 20 services we do not need.

## Revisit when

`go-telegram/bot` lags behind Bot API releases for about 3 months, or `slack-go` breaks its API often.

## Implementation notes (2026-11-10)

What shipped for the notify-only channels, and where it differs from the table above:

- **Three channels instead of five.** Email, ntfy and a templated webhook. The [feature scan](../research/2026-11-10-feature-scan.md) showed that a webhook with a body template covers Discord, Slack incoming webhooks, Gotify, Pushover-style services and Home Assistant, so separate adapters for them are not built. Guide: [notifications](../guides/notifications.md).
- **Email uses the standard library** (`net/smtp` with `crypto/tls`), not `go-mail`. The cases we need (STARTTLS, implicit TLS, a local relay without TLS, PLAIN and LOGIN auth) are covered by tests against an in-process SMTP server, and it keeps one dependency out of the binary. Revisit if real servers show problems.
- **Destinations are per member** and live in `channel_links` (an address, a topic, a URL). Instance settings (mail server, ntfy server) are saved from the web like the Telegram token, never returned once saved, and overridden by environment variables.
- **One-way by design.** These channels cannot edit a message, so they are not tracked for re-editing; a reminder already delivered to one channel is not sent again when another channel is retried.
- **Requests to user-chosen addresses are guarded** (`internal/netguard`): http and https only; loopback, link-local and cloud metadata are never reachable; private networks only when an owner allows it. The check runs on the address actually dialed.
- **A reminder with nowhere to go is recorded as not sent**, with the reason, and shown in the reminder log.
- **Links back to the web** need `--public-url`; without it messages carry no link.
- **Languages.** Every chat text goes through one translation catalog (ADR 0023). Telegram shows its command menu in Spanish to people whose Telegram is in Spanish.

Slack (interactive, over Socket Mode) is built separately; its notes are added when it lands.
