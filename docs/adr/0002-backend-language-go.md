# 0002. Backend language: Go

- Status: Accepted
- Date: 2026-11-04

## Context — what the program physically is

The app is idle **99.9 % of the time**, waiting on a timer, two outbound sockets (Telegram, Slack) and one local HTTP port. Data volume is tiny: about 6,000 charge rows in 10 years. The property that matters is **staying idle cheaply and reliably for months**: never losing a connection, never corrupting data. Raw speed does not matter.

## Options

| | Go | Rust | TypeScript (Bun/Node) | C |
|---|---|---|---|---|
| Idle RAM (order of magnitude) | ~15–25 MB | ~5–15 MB | ~60–100 MB | ~2–5 MB |
| Single binary, easy cross-compile | ✅✅ (pure-Go SQLite, no cgo) | ✅ (C toolchain for SQLite) | ⚠️ Bun compile ~60–100 MB | ✅ |
| Long-lived concurrent connections | ✅✅ goroutines + context | ✅ async (more complex) | ✅ single event loop | ❌ hand-rolled |
| Telegram / Slack libraries | ✅ | ✅ | ✅✅ | ❌ none |
| Domain modelling (sum types) | ⚠️ none | ✅✅ | ✅✅ | ❌ |
| Development speed / contributors | ✅✅ | ⚠️ | ✅✅ | ❌ |
| Long-term stability (Go 1 compatibility promise, stdlib coverage) | ✅✅ | ✅ | ⚠️ npm churn and supply chain | ✅ |

## Decision

**Go** for the backend. The web UI is TypeScript in any case (see 0012).

## Consequences — cost we accept

- No sum types. Mitigated with the `exhaustive` linter; the domain is small (a few entities, one state machine).
- Verbose error handling, which in exchange forces us to decide what happens on every failure.
- Two languages in the repo. Mitigated by generating TypeScript types from OpenAPI (0005, 0013).

## Revisit when

The domain grows into a complex accounting engine. That would favour Rust or TypeScript for modelling.
