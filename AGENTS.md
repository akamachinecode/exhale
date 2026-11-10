# AGENTS.md

Guidance for AI coding agents working on this repository (Claude Code, Codex, Cursor…).

## What this is

Exhale is a self-hosted subscription manager written in Go (backend, single binary) with an embedded React web UI. Reminders arrive in Telegram/Slack with buttons to fix the subscription in place, and every use case is exposed to AI agents over MCP. Read `docs/vision.md` and the ADRs in `docs/adr/` before changing architecture.

## Map

| Piece | Package |
|---|---|
| ⑨ billing engine (pure) | `internal/engine` |
| ⑩ money | `internal/money` |
| ⑧ domain | `internal/domain` |
| ⑦ use cases + catalog | `internal/app` (`ops.go` is the catalog) |
| ⑥ action layer (buttons, flows, app screens — ADR 0019) | `internal/actions` (`app.go` is the in-chat app, `manage.go` the add wizard and settings, `i18n_es.go` the Spanish texts) |
| file parsers for import (Wallos, CSV) | `internal/importer` |
| ⑪ scheduler | `internal/scheduler` |
| ⑫ event bus | `internal/bus` |
| ⑬⑭ SQLite store, migrations, sqlc | `internal/store` |
| ① REST API, SSE, MCP endpoint | `internal/httpapi` |
| ⑰ MCP server | `internal/agents` |
| ② Telegram | `internal/channels/telegram` |
| ③ Slack (Socket Mode, Block Kit, Home tab) | `internal/channels/slack` |
| ⑮ CLI, supervisor, logging | `cmd/exhale`, `internal/platform` |

## Commands

- `go test ./...` (fast) · `mise run test` (race detector) · `mise run lint`
- `mise run gen` after editing SQL in `internal/store/queries` or migrations
- `go test ./internal/actions -update` to regenerate conversation golden files (review the diff)

## Rules

- New business operations go in `internal/app`, get an `Op` in `ops.go` and are added to `Catalog()` and `httpapi.registerAll`. Never put business logic in adapters.
- Writes must go through `Service.write` (audit log, dry run, events). Every write accepts `dryRun`.
- Dates of charges are `engine.Date`, never `time.Time`. Money is `money.Money`, never floats.
- The web never computes charge dates; it calls `preview_schedule`.
- Every text shown in chat goes through `tr(locale, "English text")` and has a Spanish entry in `internal/actions/i18n_es.go`; a test fails otherwise (ADR 0023).
- New migrations get a number above every existing one; never renumber or reuse one that shipped.
- Commits: Conventional Commits, signed off (`git commit -s`).
