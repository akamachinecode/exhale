# Ledger

| round | hypothesis | changes | Haiku | Sonnet | Opus | gate | decision |
|---|---|---|---|---|---|---|---|
| 00 | baseline | — | 6.5 / 6.0 (noise ±0.5) | 6.5 / 6.5 (noise 0) | — | 6.0 | Anchors: Linear 8.5–9, Copilot 8–8.5, Stripe 7.5–8.5 → relative ≈ −2.3. Holdout: Haiku 6.0, Sonnet 6.0. Detector (file scan): 0. |
| 01 | Mobile overflow (grid children lack min-w-0, rows too wide), unlabeled/repeated row actions, and generic monogram tiles are the issues every reviewer raised; fixing them lifts consistency, clarity and iconography (lowest categories) | min-w-0 grids + wrapping charge rows; full-height sidebar column; labeled "Mark paid" only for due/past charges, quiet hover actions with tooltips for future ones; simple-icons brand logos with darker dark-mode tiles | | | | | |
| 02 | Hierarchy/density gaps every reviewer named: calendar has no totals and tiny chips; the subscriptions table has no totals, counts or sorting and repeats an identical "Active" pill; category bars are one color and the right column of the overview is dead space | Calendar month summary, per-day totals, larger status-tinted chips, dimmed past days; subscriptions summary line, tab counts, sortable headers, monthly column, status shown only when not active; per-category hues with % of total and a "By payment method" card | 7.5 (typo 7.5, hier 7.5, space 8, color 7, icon 6.5, polish 7, clarity 7) | — (Haiku < 8) | — | 7.5 (+1.0) | **Accepted**: above noise, every category up. |
| 03 | Mixed-currency totals are misleading (high, every round); detail rows hide the move action; "every 1 month" copy; neighbouring category hues too similar; phone meta truncates | ECB exchange rates (piece ⑤) → summary totals, category and payment-method spend converted to the main currency with the rate date; persistent row actions on the detail page; natural cadence copy; fixed categorical palette; phone row meta on two lines | 7.0 / 7.0 (two runs; space 7–7.5, color 6.5–7, icon 6) | — | — | 7.0 (−0.5) | **Rejected for the climb** (gate did not rise). The ECB conversion is kept as a data-correctness fix (mixed-currency totals were wrong, flagged high by every reviewer); the UI tweaks stay because no category fell beyond the ±0.5 noise except spacing (−0.5…−1), targeted next. |
| 04 | Same issues keep recurring: truncated KPI context on phones, icon-only actions on the detail page, unexplained calendar chip colours | Shorter KPI context that wraps; labelled persistent actions on the detail page; calendar legend for paid / unconfirmed / upcoming | 6.5 | 7.0 | 7.5 | 6.5 (full council) | **Rejected and reverted** (gate below round 2). Full council anchors: Linear 9–9.5, Copilot 8.5–9, Stripe 7–8.5 → relative ≈ −1.5 (was −2.3). Holdout (Sonnet): 6.5 (baseline 6.0) → no overfitting. Two rejections in a row → switch category. |
| 05 | Colour & contrast is the lowest category for every reviewer (6.5): faint secondary text, flat dark surfaces, weak "in N days" pills | Token-only change: darker muted/faint text in light, brighter in dark, distinct dark elevation steps, stronger neutral pills | 7.0 (color 7.5 ↑) | — | — | 7.0 | **Rejected and reverted** (gate not above round 2's 7.5); patch kept in `round-05-contrast.patch`. Third rejection in a row → **climb stopped** per protocol. |

## Status (2026-11-06, end of night 1)

Stopped after round 5 (three rejections in a row). Accepted rounds: 01, 02. The ECB conversion from round 03 stays as a correctness fix.

| | Baseline (00) | Best accepted (02) | Latest full council (04) |
|---|---|---|---|
| Haiku overall | 6.5 / 6.0 | 7.5 | 6.5 |
| Sonnet overall | 6.5 / 6.5 | — | 7.0 |
| Opus overall | — | — | 7.5 |
| Relative to anchors | ≈ −2.3 | — | ≈ −1.5 |
| Holdout (Sonnet) | 6.0 | — | 6.5 |

Judge noise is the bottleneck: Haiku moved ±0.5–1.0 across runs on identical screenshots. Next session: average two Haiku runs per round (or gate on Sonnet), then tackle the recurring issues: an edit action on the detail page, labelled touch-sized actions on phones, a hero monthly figure with trend, a calendar legend with readable swatches, and the contrast patch.
