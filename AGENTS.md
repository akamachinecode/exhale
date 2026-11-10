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
| ⑰ in-app assistant: provider clients, tool loop, confirm gate, statement reader | `internal/assistant` (`docs/guides/assistant.md`) |
| ⑰ tool schemas shared by MCP and the assistant | `internal/toolschema` |
| ② Telegram | `internal/channels/telegram` |
| ③ Slack (Socket Mode, Block Kit, Home tab) | `internal/channels/slack` |
| ④ Web Push (encryption, VAPID, action tokens — ADR 0026) | `internal/channels/webpush`; devices and keys in `internal/channels/notifiers`; service worker `web/public/sw.js` |
| ⑮ CLI, supervisor, logging | `cmd/exhale`, `internal/platform` |

## Commands

- `go test ./...` (fast) · `mise run test` (race detector) · `mise run lint`
- `mise run gen` after editing SQL in `internal/store/queries` or migrations
- `go test ./internal/actions -update` to regenerate conversation golden files (review the diff); same for `./internal/assistant`

## Rules

- New business operations go in `internal/app`, get an `Op` in `ops.go` and are added to `Catalog()` and `httpapi.registerAll`. Never put business logic in adapters.
- The assistant offers every catalog op to the model. A write must have a `dryRun` field (the assistant refuses to start otherwise): it is previewed and applied only after the person confirms. Never let model output apply a change or write the text of a confirmation.
- Writes must go through `Service.write` (audit log, dry run, events). Every write accepts `dryRun`.
- Dates of charges are `engine.Date`, never `time.Time`. Money is `money.Money`, never floats.
- The web never computes charge dates; it calls `preview_schedule`.
- Every text shown in chat goes through `tr(locale, "English text")` and has a Spanish entry in `internal/actions/i18n_es.go`; a test fails otherwise (ADR 0023).
- Every text of the web goes through `t("English text")` or `tn(n, "one", "many")` from `web/src/lib/i18n.ts` and has a Spanish entry in `web/src/lib/es.ts`; `pnpm -C web i18n` fails otherwise.
- New migrations get a number above every existing one; never renumber or reuse one that shipped.
- `mise run test:e2e` drives a real browser against a fresh server; keep it green when changing web flows.
- Commits: Conventional Commits, signed off (`git commit -s`).
