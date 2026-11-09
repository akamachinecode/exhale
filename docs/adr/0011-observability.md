# 0011. Observability module (⑯)

- Status: Accepted
- Date: 2026-11-06

## Context

Tests prove the app works *before release*; observability proves it works *on your machine, now*, and answers the most common issue: "why didn't I get that reminder?". It must work with zero setup, stay local, and optionally integrate with standard stacks. Both users and maintainers use it, and it is shown as a **professional dashboard inside our UI**.

## Measured cost (Go 1.27, stripped, `CGO_ENABLED=0`)

| Variant | Binary | Modules |
|---|---|---|
| Base (HTTP + slog) | 6.4 MB | 0 |
| + OpenTelemetry SDK (traces + metrics) | 7.6 MB (+1.2) | 23 |
| + official OTLP HTTP exporters | 15.3 MB (+8.9) | 91 (pulls gRPC even for HTTP) |
| + VictoriaMetrics/metrics | 6.8 MB (+0.4) | 4 |

## Decision — three layers

1. **Built-in, for everyone.** A Status page with:
   - Health of each piece.
   - **The story of every reminder** (scheduled → sent → clicked → saved → message re-edited, with latencies).
   - An event log and basic charts, including the response time to Slack interactions (3 s budget).
   - Storage: SQLite, 30-day retention. Every business trace is kept; HTTP traces only when they fail or take more than 500 ms.
2. **Alerts, for everyone.** Predefined rules with adjustable thresholds, delivered through the user's own channels, **through a different channel than the one that failed**:
   - Channel down for more than X minutes.
   - A reminder failed after all retries.
   - Backup failed or disk below 10 %.
   - FX rates stale for more than 3 days.
   - Slack token revoked.
   - Clock skew.

   Alerts are deduplicated and a "resolved" message follows. An **external heartbeat** (Healthchecks.io, Uptime Kuma push) covers the "whole app is down" case.
3. **Standard export, opt-in.**
   - `/metrics` in Prometheus format (VictoriaMetrics/metrics).
   - Traces via an **own OTLP/HTTP JSON exporter** (~300 lines; respects `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME`).
   - JSON logs with `trace_id`.
   - Grafana dashboards in the repo, plus a `docker compose --profile observability` with `grafana/otel-lgtm`.

Instrumentation uses the **OpenTelemetry API + SDK**. The same traces appear in development, in tests and in production.

## Link with tests

- Conversation tests also assert the expected spans, using `tracetest`.
- Latency budgets are tested (e.g. Slack ack < 3 s).
- Failed E2E runs upload their traces as CI artifacts.

## Non-goals

Monitoring the whole host. Only the app and what affects it: its data disk, its memory, the Go runtime and the clock.

## Consequences — cost we accept

We maintain our own OTLP exporter and one more module.

## Revisit when

Our exporter has compatibility problems, or OpenTelemetry ships an HTTP exporter without gRPC. Either way, switch to the official one.
