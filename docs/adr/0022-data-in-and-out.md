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

## Implementation notes: tags and the other price (2026-11-10)

The subscriptions CSV gained three columns at the end: `tags` (separated by commas), `other price` and `other price per` (`month` or `year`). The CSV importer reads them back (also `tag`, `labels`, `etiquetas`), and an Exhale JSON export carries `tags` and `altPrice` on each subscription, so both formats round-trip. Columns were added at the end so spreadsheets built on the old order keep working.
## Implementation notes: statements, import from chat, PDFs without a model (2026-11-10)

### Matching a statement to the expected charges

A statement lists payments, not subscriptions. Besides finding new recurring charges, its payments now **confirm the charges Exhale already expects**. No bank connection: the person imports a file.

`match_statement` (in the op catalog, so agents can use it over MCP) takes the payments (date, description, amount, currency) and changes nothing. A payment matches a charge when:

- **Merchant**: the description and the subscription name reduce to the same merchant (`internal/merchant`, the normalisation the importer already used: "NETFLIX.COM 866-579-7172" and "Netflix" are `netflix`). Same first word, or one written together starts the other ("disneyplus" and "Disney+"), at least four letters.
- **Date**: within 7 days of the due date, before or after. A subscription charged more often gets less (under half the gap between its charges), so a weekly charge cannot take the next week's payment.
- **Amount**: within 30% in the same currency, enough for a price rise and not for another product. In another currency (a USD service paid from a EUR card) the amount is not compared.

Each charge and each payment is used once; the closest pairs win (exact amount, then nearest date). A match is `mark_paid` for an open charge or `already_paid`. A different amount in the same currency carries the same price-change hint as `set_charge_status`, so the review asks "new price from now on?".

**Expected but not found**: open charges due in the statement period that no payment matches. The last 3 days are left out (a charge may not have reached the statement yet). A statement covers one card or account, so once something matched, only charges paid with the same payment methods (or with none) are listed; otherwise every card's charges would show as missing.

Applying is not a new write: the web and the chat call `set_charge_status` (paid, with the amount and currency the bank took) for each ticked match, and `import_subscriptions` for the ticked new ones. Both are previewed with `dryRun` when the list is built; a charge whose dry run fails is left out. A recurring payment whose merchant matched a subscription is not offered again as new.

It works with the assistant off. With it on, a model that reads a scan or a screenshot may also report the payments it sees (at most 300, checked like the items: a real date, a positive amount, one line of text), and they are matched the same way.

### Import from chat

A document or a photo sent to the Telegram bot runs the same reader as the web upload (`assistant.ReadImport`) and comes back as one message: the charges to mark paid, the new subscriptions, and what was expected but not found. Each line has a number button that skips it; **Accept all** or **Apply n of m** applies, **Discard** drops it. A charge paid at another amount then gets the usual "New price?" message with **Yes, from now on** / **Just this time**.

- **Long lists** (more than 8 lines) are not ticked in chat: the message offers **Accept all** and a link to the web (when `EXHALE_PUBLIC_URL` is set). The review waits for a day in the `flows` table (one per member; a new file replaces it; no migration) and shows at the top of Settings → Data → Import (`GET`/`DELETE /api/v1/import/review`).
- **Limits**: a document up to 20 MB, a photo up to 5 MB (the limits of the upload and of a picture sent to a model, ADR 0018). The size Telegram reports is checked before downloading, and the download is cut at the limit. The file is read in memory and never stored; only what was read from it waits for review.
- **Slack: not done.** Reading a file shared in Slack needs the `files:read` scope, which means every existing Slack app must be reinstalled, plus handling `file_share` messages and authenticated downloads. Not simple enough for this change. The review buttons are channel-agnostic, so only the intake is missing.

### PDF without a model: a small dependency (measured)

A PDF with text is turned back into lines in Go and read by the same line reader as pasted text (a date, a description, the first amount after it; a later amount is usually the balance). A PDF without text (a scan) says it needs the built-in assistant or a CSV.

Options: write a minimal extractor, or use `github.com/ledongthuc/pdf` (a maintained fork of `rsc.io/pdf`, BSD-3). Measured on the `exhale` binary, stripped static build:

| | Size | Modules added |
|---|---|---|
| without PDF text | 24.9 MB | 0 |
| + `ledongthuc/pdf` | 25.2 MB (**+0.34 MB**, +1.4%) | 1 (no dependencies of its own) |

**Verdict: the dependency.** What a statement PDF needs is not small: cross-reference streams and object streams (PDF 1.5+), Flate with predictors, font encodings and ToUnicode maps (most generators embed subset fonts), and RC4/AES decryption with an empty password (banks often "protect" statements that way). That is roughly a thousand lines we would own and fuzz ourselves, against 0.34 MB and one module.

The cost we accept: the library panics on some damaged files (recovered; the person is told the PDF could not be opened), it only reads the first 60 pages, and positions of text are approximate. Lines are rebuilt from glyph positions (same baseline within a third of the font size, sorted left to right), so column layouts drawn one column at a time still come out as rows. **Revisit** if real statements show layouts the line reader cannot follow; the model remains the fallback when the assistant is on.

### Not done

- Slack file shares (above).
- Dates without a year ("03 NOV") in a PDF or pasted lines are not read; the statement must print full dates.
- Matching does not learn: a bank description that does not share a word with the subscription name ("AMZN Mktp" for "Prime Video") is not matched. Renaming the subscription or adding the match by hand is the workaround.
- Not tested with real bank PDFs: the fixtures are invented (`internal/importer/testdata`).
