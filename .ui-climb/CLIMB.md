# UI hill climb

Process requested by the maintainer (see the conversation of 2026-11-06): turn "the UI looks good" into a score that a council of clean-context Claude reviewers raises round by round, measured against fixed anchors.

## Surface

Exhale web UI (*Operate* surface: a working dashboard), served by `bin/exhale` with demo data from `scripts/seed-demo.sh`.

Server: `exhale serve --listen 127.0.0.1:18484` on a seeded data dir. dekit is not installed on this machine, so the server runs as a background process for the climb only.

Captures: `node web/scripts/capture.mjs .ui-climb/round-NN http://127.0.0.1:18484 --set working|holdout|all`. Fixed viewports (1440×900, 390×844), full page, `reducedMotion: reduce`, animations and transitions disabled, fonts awaited, SSE blocked so the network goes idle.

### Working screens

| id | URL | viewport | theme |
|---|---|---|---|
| overview-1440-light | `/` | 1440 | light |
| overview-1440-dark | `/` | 1440 | dark |
| overview-390-light | `/` | 390 | light |
| subscriptions-1440-light | `/subscriptions` | 1440 | light |
| detail-1440-dark | `/subscriptions/<Netflix>` | 1440 | dark |
| calendar-1440-light | `/calendar?month=2026-11` | 1440 | light |

### Holdout screens (scored only at the start and the end)

| id | URL | viewport | theme |
|---|---|---|---|
| agents-1440-light | `/agents` | 1440 | light |
| activity-390-dark | `/activity` | 390 | dark |

## Anchors

Captured once on 2026-11-06 into `.ui-climb/anchors/`, never re-captured: `linear.png` (linear.app product UI), `copilot-money.png` (copilot.money app UI), `stripe-billing.png` (stripe.com/billing dashboard imagery). They are public marketing pages showing each product's UI, since the dashboards themselves need accounts. Reviewers judge only the product UI visible in them. **The score that matters is relative: ours − anchors.**

## Council

Clean-context, read-only reviewers launched in parallel with the Agent tool (`subagent_type: Explore`, `model: haiku | sonnet | opus`). They only get the screenshots, the anchors and a short brief.

- Haiku every round. Sonnet when Haiku ≥ 8. Opus when Sonnet ≥ 8.5. Full council every 4 rounds.
- Categories (0–10, 0.5 steps): typography · hierarchy & density · spacing & layout · color & contrast · iconography & imagery · consistency & polish · clarity & usability · overall. Plus anchor scores, issues `{screen, category, severity, fix}`, and "what would make it a 10".
- Gate = minimum overall across active reviewers. Issues are deduplicated and ranked by how many reviewers raise them.

## Guards

- `go test ./...`, `pnpm exec tsc --noEmit`, `pnpm exec biome check`, `vite build` green.
- Never remove content or features to raise a score. Copy only changes when it makes sense.
- `impeccable detect` findings must not increase between rounds.

## Target ladder

- **E1:** overall ≥ 9 for all three reviewers.
- **E2:** every category ≥ 9.5.
- **E3 (optional):** 10.

## Budget

At most 20 rounds for this surface, and a reasonable cap on Opus calls (≤ 6).

## Loop

Each round:

1. Pick at most 3 issues: lowest category first, then those raised by several reviewers.
2. Write the hypothesis in `LEDGER.md` before editing.
3. Implement, run the guards, capture, run the council.
4. Accept and commit if the gate rises above the noise and no category drops beyond the noise; otherwise revert and log why.
5. Two rejected rounds in a row: switch category. Three: stop and report.
