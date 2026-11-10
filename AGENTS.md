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
| ⑥ action layer (buttons, flows, app screens — ADR 0019) | `internal/actions` (`app.go` is the in-chat app, `manage.go` the add wizard and settings) |
| languages and translations (ADR 0023, `docs/guides/translating.md`) | `internal/i18n` (`languages.json` registers them, `locales/<code>.json` holds chat and server texts); web texts in `web/src/locales/<code>.json` |
| file parsers for import (Wallos, CSV) | `internal/importer` |
| ⑪ scheduler | `internal/scheduler` |
| ⑫ event bus | `internal/bus` |
| ⑬⑭ SQLite store, migrations, sqlc | `internal/store` |
| ① REST API, SSE, MCP endpoint | `internal/httpapi` |
| ⑰ MCP server | `internal/agents` |
| ⑰ OAuth 2.1 authorization server for remote MCP (ADR 0024) | `internal/oauth` (endpoints in `internal/httpapi/oauth.go`) |
| ⑰ in-app assistant: provider clients, tool loop, confirm gate, statement reader | `internal/assistant` (`docs/guides/assistant.md`) |
| ⑰ tool schemas shared by MCP and the assistant | `internal/toolschema` |
| ② Telegram | `internal/channels/telegram` |
| ③ Slack (Socket Mode, Block Kit, Home tab) | `internal/channels/slack` |
| ④ Web Push (encryption, VAPID, action tokens — ADR 0026) | `internal/channels/webpush`; devices and keys in `internal/channels/notifiers`; service worker `web/public/sw.js` |
| ⑮ CLI, supervisor, logging | `cmd/exhale`, `internal/platform` |

## Commands

- `go test ./...` (fast) · `mise run test` (race detector) · `mise run lint`
- `mise run gen` after editing SQL in `internal/store/queries` or migrations
- `go test ./internal/actions -update` to regenerate conversation golden files (review the diff) and the web's copy of the server texts; same for `./internal/assistant`
- `pnpm -C web i18n` checks the web's translations (`--update` fills an incomplete language)

## Rules

- New business operations go in `internal/app`, get an `Op` in `ops.go` and are added to `Catalog()` and `httpapi.registerAll`. Never put business logic in adapters.
- The assistant offers every catalog op to the model. A write must have a `dryRun` field (the assistant refuses to start otherwise): it is previewed and applied only after the person confirms. Never let model output apply a change or write the text of a confirmation.
- Writes must go through `Service.write` (audit log, dry run, events). Every write accepts `dryRun`.
- Dates of charges are `engine.Date`, never `time.Time`. Money is `money.Money`, never floats.
- The web never computes charge dates; it calls `preview_schedule`.
- Every text shown in chat goes through `tr(locale, "English text")` (counted words through `plural`) and has an entry in `internal/i18n/locales/es.json` and every other complete language; a test fails otherwise (ADR 0023). Never hard-code a language list: use `internal/i18n` (Go) or `languages` from `web/src/lib/i18n.ts`.
- Every text of the web goes through `t("English text")` or `tn(n, "one", "many")` from `web/src/lib/i18n.ts` and has an entry in `web/src/locales/es.json`; `pnpm -C web i18n` fails otherwise.
- Errors people may see are written with `domain.Invalid`, `NotFound`, `Missing`, `Conflict` or `Fail` (never `fmt.Errorf` with a sentinel), and other server texts people read (the assistant's cards) with `domain.T`; a field name or a status passed as an argument is a `domain.Word`. The format is a constant English sentence. Add the Spanish to `internal/i18n/locales/es.json` (Go verbs), then run `go test ./internal/actions -update`: it copies it to `web/src/locales/server/es.json` with `{0}` placeholders (never edit that copy). MCP agents keep getting the English `Error()`. A new op's `Title` needs an entry too.
- New migrations get a number above every existing one; never renumber or reuse one that shipped.
- `mise run test:e2e` drives a real browser against a fresh server; keep it green when changing web flows.
- Commits: Conventional Commits, signed off (`git commit -s`).
