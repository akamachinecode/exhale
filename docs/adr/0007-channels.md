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

Slack is described in the next section.

## Implementation notes: Slack (2026-11-10)

Slack shipped as `internal/channels/slack`. Nothing here has run against a real workspace yet: the tests use a fake Slack (Web API over `httptest` plus a Socket Mode WebSocket).

**Library: `slack-go/slack` v0.35.0** (BSD-2-Clause, released 2026-11-05), as decided above. The choice was checked again against a thin hand-written client (`net/http` plus `coder/websocket`):

- The part we cannot test here is the transport: the Socket Mode handshake, WebSocket pings, `disconnect` and refresh requests, reconnection with backoff, the 20 KB limit on acknowledgements. `slack-go` has all of that in production use. A hand-written client would be about 300 lines of exactly the code that only real Slack can prove right.
- Its cost is small: one dependency of its own, `gorilla/websocket` v1.5.3 (its test-only requirements stay out of our build; our fake Slack in the tests uses the same WebSocket library). It is still 0.x, so it stays isolated: only `internal/channels/slack` imports it, and Block Kit is built in one file (`render.go`).
- Web API calls go through it too, for the typed Block Kit structs and its error types (`SlackErrorResponse` tells "Slack said no" from "Slack is unreachable", which is what token validation needs).

**Socket Mode, outbound only.** The adapter needs two tokens: the bot token (`xoxb-`) for the Web API and an app-level token (`xapp-`, scope `connections:write`) that opens the socket. Each team creates its own Slack app from a manifest (`slack.Manifest`, shown in Settings and in `docs/guides/slack.md`; a test keeps the guide and the code equal). No URL is configured anywhere: events, interactivity and the slash command all use the socket.

**Tokens** come from `EXHALE_SLACK_BOT_TOKEN` + `EXHALE_SLACK_APP_TOKEN` (both, and they win) or from the web, where they are validated before saving (`auth.test`, then `apps.connections.open`), stored in `settings` and hot-swapped by `slack.Manager`, the same shape as `telegram.Manager`. The status endpoint never returns them; errors and logs are scrubbed of tokens and of Socket Mode URLs, which carry a ticket.

**Who is the chat.** For the action layer a Slack chat is a person: `ChatRef.ChatID` and `channel_links.external_id` are the Slack user id, wherever a button was pressed (direct message, Home tab or a shared channel). The adapter opens the direct message with `conversations.open`. One consequence: a question that needs a typed answer always continues in the direct message, which is the only place the app reads (`message.im`).

**Every envelope is acknowledged first**, then its work runs on one queue, in order, so typed answers cannot overtake each other and Slack's 3 seconds are never at risk.

**Limits** (docs.slack.dev, checked 2026-11-10): 25 elements per `actions` block, 50 blocks per message, 100 in the Home tab, 3000 characters per text, 150 per header, 75 per button label, 2000 per button value. The renderer splits long text on line boundaries, continues a row of more than 25 buttons in a second block, shortens labels, drops buttons whose value or URL cannot be sent, and when a screen has more blocks than the surface takes it keeps the first ones, says how many are missing and keeps the last row (the way home). A month calendar is 46 buttons in 8 blocks and fits whole.

**Not done yet**

- Retrying after `rate_limited`: the update is skipped and logged; the next change redraws the message.
- Email and the webhook channels (④), unchanged.
- A member whose Slack account is re-linked to another member keeps the old member on app messages already recorded until they are redrawn from scratch (the same holds for Telegram).
