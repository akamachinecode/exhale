# 0009. Runtime and system: kong, suture, slog, kardianos/service

- Status: Accepted
- Date: 2026-11-06

## Context — what it has to do

- One binary with subcommands.
- Infrastructure config read before the database is opened.
- **Pieces kept alive independently:** Telegram failing must not take the web down.
- Graceful shutdown within Docker's 10 s.
- Report health.
- Integrate with each OS.

## Decision

| Piece | Choice | Why |
|---|---|---|
| CLI + config | **`alecthomas/kong`** | One struct defines commands, flags, env vars and help. Zero runtime dependencies. Cobra rejected (deps, boilerplate, viper temptation) |
| Supervisor | **`thejerf/suture` v4** | Erlang-style restart with backoff and panic recovery; used by Syncthing. `errgroup` rejected: it cancels everything on the first error |
| Logs | **`log/slog`** + **`lmittmann/tint`** (zero-dep colours in TTY) | JSON via `EXHALE_LOG_FORMAT=json`. Secrets redacted centrally in `ReplaceAttr` |
| OS service | **`kardianos/service`** | systemd, launchd and Windows; only depends on `x/sys` |
| Keep-awake | Own, per OS, opt-in | macOS `caffeinate -i -w <pid>`, Linux `systemd-inhibit`, Windows `SetThreadExecutionState` |
| Health | Own | `/healthz`, `/readyz`, and `exhale doctor` (Telegram/Slack/SMTP reachability, clock, disk, permissions) |
| Backups | Own, SQLite `VACUUM INTO` | A consistent snapshot without stopping (copying the file live can corrupt it). Rotation: 7 daily + 4 weekly |

## Configuration

- Priority: flags > env > defaults. **No config file in v1.**
- Default port **different from Wallos (8282)**, so both can run side by side during migration.
- Env vars: `EXHALE_DATA_DIR`, `EXHALE_LISTEN`, `EXHALE_BASE_URL`, `EXHALE_LOG_LEVEL`, `EXHALE_LOG_FORMAT`, `EXHALE_TRUSTED_PROXIES`, `EXHALE_KEEP_AWAKE`, `EXHALE_SECRET_KEY`, `EXHALE_UPDATE_CHECK`.
- Secrets (bot tokens, SMTP password) are encrypted with AES-256-GCM **only if `EXHALE_SECRET_KEY` is provided**. Otherwise they are stored with `0600` permissions and a warning. A key stored next to the data protects nothing.

## Privacy promises

- **Zero telemetry, ever.**
- The update check is a daily GET to GitHub Releases that sends no data, and can be disabled.
- No auto-update in v1.

## Revisit when

A config file is requested (kong supports it), or Prometheus-style operational needs grow beyond 0011.
