# 0021. The dates that matter: trials, notice periods and expiries

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

A reminder about the renewal date is often about the wrong date. The [feature scan](../research/2026-11-10-feature-scan.md) of Wallos and about 45 other tools found the same unmet demand everywhere: people lose money on the day a free trial starts charging, on the last day a contract can be cancelled, and on the day something expires without renewing. Wallos has had free trials open since 2023 and end dates since 2024; its cancellation reminder fires on one exact date and must be re-entered every year.

Exhale has to know, per subscription, which date decides something, remind about that date, and let the reminder end in a decision.

## Criteria

- One subscription model. No parallel "trial" or "contract" objects that drift from the subscription they describe.
- Dates are computed by the billing engine from the schedule, never typed twice.
- A reminder must be answerable in place (ADR 0019): keep, cancel, renewed, let it expire.
- Old data keeps working: every new fact is optional.

## Options

| Option | Verdict |
|---|---|
| **A) A cancellation date field, like Wallos** | Rejected. A single date goes stale every cycle and says nothing about why it matters. |
| **B) Separate entities** (Trial, Contract, Expiry) linked to a subscription | Rejected. Three lifecycles to keep consistent with the schedule; every adapter and tool would need three more sets of operations. |
| **C) Facts on the subscription, dates derived per charge** | **Chosen.** |

## Decision

A subscription carries optional **details** (one JSON column, read and written together): `startedOn`, `trialEndsOn`, `noticeDays`, `renewal` (`auto` or `manual`), `cancelUrl`, `account`, cost `shares`, a scheduled `nextPrice` and the last `usage` answer.

What each one changes:

- **Free trial** (`trialEndsOn`): the first charge is the day the trial ends and the price is what it becomes. That charge has kind `trial`; its reminder says the trial ends and offers "cancel before it charges".
- **Notice period** (`noticeDays`): every charge has a `cancelBy` date (due date minus the notice). A separate reminder kind, `decision`, asks "keep or cancel?" seven days before that date and on it. "Keep" is recorded on the charge (`decision`) and stops the questions; cancelling uses the normal cancel operation. Charges are materialized further ahead for these subscriptions so the deadline is never missed. A subscription shows the next deadline that can still be met.
- **Manual renewal** (`renewal=manual`): the date is when it expires. The reminder says so, "renewed" marks the charge paid, "let it expire" ends the subscription. It is never auto-confirmed. This replaces Wallos's "one-time payment" for domains, licences and prepaid plans.
- **Cancel link**: shown as a link button wherever cancelling is offered, with one honest sentence: Exhale stops tracking it, the provider still has to be told.
- **Price announced in advance** (`nextPrice`): charges from that date carry the new amount; the plan promotes it to the current price when the day comes.

A subscription that already ended can be recorded directly (`status=cancelled` with its real start and end), so history imported from other tools is not lost.

## Consequences — cost we accept

- Details are not filterable in SQL. Nothing needs it today; everything is computed in the service.
- Two reminder streams can exist for one charge (deadline and renewal). They are different questions on different days, so we accept it.
- "Last day to cancel" is only as good as the notice period the person enters. Exhale does not know providers' terms.

## Revisit when

- People ask for per-provider terms to be filled in automatically (a catalog, or the assistant reading a contract).
- A notice period needs to be counted in months or to a fixed calendar date ("before 31 October"), not in days.

## Implementation notes: reminders that end in an answer (2026-11-10)

The [feature scan](../research/2026-11-10-feature-scan.md) asked for snooze, "repeat until handled", duplicates as a message and card-expiry impact. User guide: [reminders that end in an answer](../guides/reminders.md).

- **Everything is an outbox item.** A snooze, a repeat, a duplicate notice and a card notice are rows of the outbox, so they get the same guarantees as reminders: a dedupe key, retries, catch-up after downtime, and nothing lost on restart. Items that are not about a charge say what they are about in a new `ref` column (a payment method, or a duplicate group).
- **Snooze has a last useful day** (`app.SnoozeDay`): the charge date for a reminder, the last day to cancel for a keep-or-cancel question, none for "was it charged?". A later day is moved back to it, not refused, so "in 3 days" two days before the deadline comes back on the deadline. When a message of the same kind is already planned for that charge on that day, no second one is added. One cost: a snooze is not cancelled when a reminder of the same day is added later (for example after the reminder days change), so that day can have two messages.
- **"Not yet" is a one-day snooze of "was it charged?"**. Before, it said "I'll ask again tomorrow" while the question had a single fixed dedupe key, so nothing ever came back.
- **Repeats** live in `ReminderPolicy` (`confirmTimes`, `confirmEveryDays`; default 3 times, daily). Like the reminder time, the household holds the default, new subscriptions copy it, and changing it moves the subscriptions still on the old one. The first ask keeps its old dedupe key, so upgrading does not ask again about charges already asked about. A repeat whose day passed while the server was down is skipped instead of sent late: repeats nag, and three late questions at once about an old charge would be noise.
- **Duplicate groups** have a stable key: a hash of the ids of the subscriptions in the group. A group that gains or loses a member is a new group, and is announced once. Each member is told only about groups made of subscriptions they can see, and only if they pay for one of them. "That's fine" is stored per household (`dismissed_findings`): one person saying so is enough, and audit stops reporting the group.
- **Card notices** go out 30 days before expiry (`app.CardNoticeDays`), once per card and expiry date: renewing the card with a new date allows a new notice. When the message is due, Exhale checks it again. If the card was renewed or nothing uses it any more, it is recorded as no longer needed instead of sent.
- **"You stopped paying"** counts subscriptions with status `cancelled`, at their last price, converted into the display currency. Paused ones are left out. A cancelled one whose service is still paid by an active subscription is a replacement and is left out. The scan advised against a manual replacement link, and the service match from duplicate detection covers the common case.
