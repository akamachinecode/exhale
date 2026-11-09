# 0003. Architecture: one process, channel-agnostic action layer

- Status: Accepted
- Date: 2026-11-05

## Decision

A single Go process (`exhale`) made of these pieces. The numbers are used throughout the docs and the interactive diagram ([architecture-diagram.html](../architecture-diagram.html)).

| # | Piece | Responsibility | Package |
|---|---|---|---|
| ① | HTTP server | Embedded web UI, REST API, SSE, ICS feed, auth | `internal/httpapi`, `internal/auth` |
| ② | Telegram adapter | Long polling; renders actions as inline buttons; edits messages in place | `internal/channels/telegram` |
| ③ | Slack adapter | Socket Mode; renders actions as Block Kit buttons and modals | `internal/channels/slack` |
| ④ | Simple notifiers | Email, ntfy, Gotify, Pushover, Discord and generic webhooks | `internal/channels/notify` |
| ⑤ | Exchange rates | Daily ECB rates with history | `internal/fx` |
| ⑥ | **Action layer** | Which actions a reminder carries; multi-step flows with server-side state; **knows nothing about Telegram or Slack** | `internal/actions` |
| ⑦ | Use cases | Mark paid, change date, change method, cancel, edit reminder… **shared by the web and the chat** | `internal/app` |
| ⑧ | Domain | Subscription, Charge (with state), Payment method, Category, Household, Channel, Reminder rule | `internal/domain` |
| ⑨ | Time engine | Billing recurrence, dates, time zones | `internal/engine` |
| ⑩ | Money | Exact amounts and conversion | `internal/money` |
| ⑪ | Scheduler ("clock") | Sleeps until the next due item; outbox; catch-up after downtime | `internal/scheduler` |
| ⑫ | Event bus | Every change → live web update (SSE) and re-edit of already-sent messages | `internal/bus` |
| ⑬ | Repository | sqlc queries + mapping to domain types; migrations | `internal/store` |
| ⑭ | Data folder | SQLite (WAL), logos, backups | — |
| ⑮ | Runtime and system | Supervisor, config, logs, OS service, keep-awake, backups | `internal/platform` |
| ⑯ | Observability | Status page, per-reminder timeline, alerts, metrics, OTLP export | `internal/observe` |

## Key rules

- **Charges are events with state** (pending, paid, delayed, skipped, failed), not just a single "next date". Each reminder refers to one concrete charge.
- **The web never goes through ⑥.** It calls the use cases ⑦ directly. Chat and web share exactly the same business operations.
- **⑥ is channel-agnostic.** Adapters only render. A fake text channel exists from day one for tests, so the abstraction never leaks the concepts of a specific channel.
- **Telegram allows only 64 bytes of callback data per button.** Flow state therefore lives in the database, and buttons carry only a short reference.
