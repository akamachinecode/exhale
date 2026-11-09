# 0010. Testing strategy

- Status: Accepted
- Date: 2026-11-06

## Principle

Tests exist to catch **the failures that hurt most**, before users do. Coverage percentage is not a goal.

| Failure | Pain | How we catch it |
|---|---|---|
| Engine computes a wrong date or amount (⑨⑩) | 🔴 Silent, destroys trust | Property-based tests + known calendars + fuzzing of parsers |
| A migration breaks existing installs | 🔴 Unfixable remotely | A sample DB from **every released version** in `testdata/migrations/`, upgraded to HEAD on every change |
| A reminder is lost or duplicated (⑪) | 🔴 Core promise | Simulated time (`testing/synctest`): "advance 3 days", "crash mid-send" |
| A bot flow goes wrong (⑥) | 🟠 The differentiator | **Conversation scripts** rendered by the fake text channel, compared with readable golden files |
| Telegram/Slack change something | 🟠 | Fake servers (`httptest`, fake Slack WebSocket) + optional periodic smoke test against a real bot |
| Web/server drift | 🟡 | The generated TS client stops compiling in CI |
| Security regressions | 🔴 | Session, CSRF, token scope and unlinked-chat tests + dependency scanning |
| OS-specific bugs | 🟡 | Tests on Linux, macOS and Windows |
| Data races | 🟠 | `go test -race` always |

## Tools

- stdlib `testing` + `google/go-cmp` (testify not needed).
- `pgregory.net/rapid` and native Go fuzzing.
- `testing/synctest` (Go 1.25+).
- An own golden-file helper with `-update`.
- `httptest` + `humatest`.
- **Real SQLite** in a temp dir (no DB mocks).
- Frontend tests: see 0015.
- Trace assertions and latency budgets: see 0011.

## Rules

- No coverage gate, but **⑨⑩⑥ do not accept changes without tests**.
- Every bug fix comes with the test that reproduces it.
- CI blocks a PR when any of these fails: tests, lint, race detection, the migration-upgrade test or the generated-code check.

## Revisit when

Browser E2E becomes flaky or slow. It would then move to a nightly job.
