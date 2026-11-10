# 0022. Data in and out: import with a review list, export, calendar feed

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

Onboarding is where people abandon manual subscription trackers, and missing export is punished in reviews as lock-in ([feature scan](../research/2026-11-10-feature-scan.md)). Imports that save directly (screenshot or email scans) get the worst reviews: they create wrong data silently. Exhale must make it easy to arrive from Wallos or a spreadsheet, easy to leave, and must show its dates in the calendar people already use.

## Criteria

- Nothing is created without the person seeing it first.
- One import path for files, AI agents and the built-in assistant, so each new source is only a parser.
- Export is complete and needs no tool to read.
- No public URL required, no central service.

## Decision

**Import.** One operation, `import_subscriptions`, takes a list of items that name categories, payment methods and payers by name (missing categories and payment methods are created). It skips and reports duplicates (same name, price and currency) and invalid items instead of failing the batch. With `dryRun` it is the **review list**: the web shows it with a tick per item and applies only what stays ticked; an agent shows it to the user before applying.

Parsers (`internal/importer`) only read files and produce items: a **Wallos database** (the most faithful source: currency codes, start dates, reminder days), a Wallos export, a CSV with common English and Spanish column names, and Exhale's own export. Inactive Wallos subscriptions arrive as ended subscriptions with their real dates. Bank statements and screenshots go through the same list once the assistant extracts the items (ADR 0018).

**Export.** `export_data` returns everything in one JSON document (`exhale/1`): settings, members, categories, payment methods, and every subscription with all its charges. Two CSV downloads (subscriptions, charges) serve spreadsheets.

**Calendar.** A `calendar` operation returns dated events for a period: real charges and, beyond the materialized horizon, the ones the schedule will produce, plus trial ends, expiries and last days to cancel. The same events are served as an **iCal feed** on a link that carries its own secret (calendar apps cannot send tokens). Only a hash is stored; each member has one link and can replace or remove it. Events are all-day with stable ids.

## Consequences — cost we accept

- Importing an Exhale export recreates subscriptions, not their charge history. Moving a whole instance is done with a backup.
- A Wallos export has currency symbols, not codes; the review list says so and recommends the database file.
- Whoever has the feed link can read the calendar until it is replaced. The link is shown once.

## Revisit when

- People need to import charge history (paid dates and amounts) from another tool.
- CalDAV two-way sync is requested enough to justify it.
