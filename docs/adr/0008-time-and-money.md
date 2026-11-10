# 0008. Time and money: own billing engine, bojanz/currency, ECB rates

- Status: Accepted
- Date: 2026-11-06

> The billing engine is one of the project's key success factors. It gets the same care as the action layer ⑥.

## Context — how subscriptions really recur

- Every N days, weeks, months or years (monthly and yearly most common; also quarterly, "every 28 days", "every 4 weeks").
- **Day 31:** subscribed on 31 Jan → charged 28/29 Feb → **31 Mar again** (Stripe, Apple and Google semantics).
- **No drift:** every charge is computed from the **anchor**, never from the previous charge.
- One-off changes ("this month it was charged on the 10th") must not move the series. Series changes ("from now on, the 15th") create a new **rule version**.
- Free trials, end dates, fixed number of charges, cancellation at period end.

## Why not RRULE

In RFC 5545, `FREQ=MONTHLY;BYMONTHDAY=31` **skips** months without a 31st. Emulating billing semantics needs fragile tricks (`BYMONTHDAY=28,29,30,31;BYSETPOS=-1`) that still break on day 30. RRULE models calendar events, not billing cycles. `teambition/rrule-go` has also had no activity since Aug 2024.

## Decision

| Piece | Choice |
|---|---|
| ⑨ Recurrence | **Own engine** (~300 lines, pure): `interval + unit + anchor + rule versions with effective date`. Concrete charges are generated from it; a moved charge keeps `original_date` and `actual_date` |
| Engine tests | Property-based tests with `pgregory.net/rapid` (no drift, anchor respected, never two charges in one period…) plus known calendars |
| Dates | **A charge is a civil date, a reminder is an instant.** Own `Date` type (y, m, d) on stdlib `time`. Time zone stored per household. `time/tzdata` embedded. `cloud.google.com/go/civil` rejected (pulls the Google Cloud module) |
| ICS feed | `arran4/golang-ical`, **one VEVENT per charge** (−3 / +12 months), so calendars show exactly what the engine computed, delays included |
| ⑩ Money | **`bojanz/currency`** (apd decimals, CLDR v49 data, locale formatting for bot messages, custom currencies). Stored as **INTEGER minor units + ISO 4217 code**. The web formats with `Intl.NumberFormat` |
| Real amount | A paid charge can record the **amount actually charged** in the account currency (e.g. Copilot $10 → €9.24 on Revolut) |
| ⑤ FX | **ECB directly** (free, no key, ~30 currencies, 90-day history for backfill). Rates stored with history. Past charges are valued at **their day's** rate. Manual rates for currencies the ECB does not publish. Keyed providers and crypto later, behind an interface |

## Revisit when

Users need truly calendar-like rules ("second Tuesday of the month"). That would mean adding RRULE as an additional rule type. *(Partly done without RRULE: see the 2026-11-10 notes below.)*

## Implementation notes

### 2026-11-10: weekday of the month and weekend shifts

The [feature scan](../research/2026-11-10-feature-scan.md) found two requests the engine could not express: "the third Wednesday" and "if it falls on a weekend, the next business day". Both are now part of the rule, not a second rule type. RRULE is still not needed.

The rule gains two optional fields (stored in the same schedule JSON, so no migration):

- **`dayRule`** for monthly and yearly rules: `""` (the anchor's day number, as before), `weekday` (the anchor's weekday in the anchor's week of the month: anchored on the third Wednesday, always the third Wednesday) or `last-weekday` (the last such weekday). The weekday and the week are read from the anchor, so there is one source of truth. Only the 1st to 4th and the last exist in every month; a fifth weekday is refused with a hint to use "last".
- **`weekendShift`**: `""`, `next` or `previous`. A charge that falls on a Saturday or Sunday moves to the next or previous business day.

What stays true:

- **No drift.** The engine computes the *nominal* date from the anchor (`Rule.Nominal`) and only then applies the shift (`Rule.Occurrence`). A charge moved from Saturday to Monday does not move the next one. Property tests check this over 1990–2100, every month length and every weekday.
- **Strictly increasing charges.** A shift moves a date by at most two days, so it is refused for daily cycles shorter than 3 days, where two charges could land on the same business day.
- **Due dates everywhere.** Everything outside the engine (charges, reminders, the calendar feed, insights, the preview) sees the shifted date; version boundaries, `Until` and the charge limit compare shifted dates. A new rule version starts at its anchor, or at its first charge when a shift to the previous business day makes that earlier.
- **Edits keep the billing day.** "From now on" edits anchor on the date the next charge was scheduled for before the shift (`Schedule.NominalOf`), and keep the rule's kind (`Rule.Reanchor`): moving a third-Wednesday charge to a Thursday makes it the third Thursday. Views expose `nextScheduled` so the web and exports can do the same.

**Weekends only.** There are no holiday calendars: they differ per country, region and bank, and the data would have to be shipped and kept up to date. Revisit if people ask for a bank holiday shift with a concrete calendar in mind.
