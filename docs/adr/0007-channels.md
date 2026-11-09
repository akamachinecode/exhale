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
