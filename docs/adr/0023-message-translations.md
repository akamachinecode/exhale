# 0023. Translating chat messages and the web

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

A household chooses its language (English or Spanish), and until now only amounts followed it. Reminders, the in-chat app, the add wizard and the check-up must speak that language, on every channel, without each channel adapter translating anything (ADR 0003: adapters never decide content).

## Criteria

- The code stays readable: a reader sees the sentence where it is used.
- A missing translation is caught by a test, not by a user.
- No runtime dependency and no extra files to ship: the binary is one file.
- Works for text with arguments in a different order.

## Options

| Option | Verdict |
|---|---|
| **A) Message ids** (`T("reminder.renews")`) with catalogs per language | Rejected. The code stops being readable and the English text lives far from its use. |
| **B) gettext-style: English text is the key** | **Chosen.** |
| **C) golang.org/x/text/message with extracted catalogs** | Rejected for now: a generation step and plural rules we do not need with two languages. |

## Decision

Messages are written in English at the call site through `tr(locale, "English text %s", args…)`. A catalog per language (`internal/actions/i18n_es.go`) maps the English text to its translation; a missing entry falls back to English. Dates, relative days ("in 3 days") and billing cycles ("monthly", "every 3 months") have their own small helpers. Translations may reorder arguments with indexed verbs.

A test parses the package and fails when a text passed to `tr` or `plural` has no Spanish translation, when a translation is no longer used, or when a translation takes different arguments than its English text.

Texts a channel says outside any screen (linking a chat, joining, help) live in the action layer too, so Telegram and Slack say the same thing. An unlinked chat is answered in the language the chat platform reports.

The **web** follows the same idea in TypeScript: `t("English text {0}", value)` and `tn(count, "one", "many")` in `web/src/lib/i18n.ts`, with the Spanish catalog in `web/src/lib/es.ts`. The language is the browser's unless the person switches it (saved in that browser); dates, amounts and billing cycles follow it. `pnpm i18n`, run in CI, applies the same three checks as the Go test.

Errors raised by the service (validation messages) stay in English for now. *(Updated 2026-11-10: they are translated now, with the English text still as the key; see "Finished later" in ADR 0018's implementation notes and the rule in AGENTS.md.)*

## Consequences — cost we accept

- Changing an English sentence breaks the test until its translation is updated. That is the point.
- The web language is per browser, not per account: someone using two devices chooses twice.
- Sentences split around a link or a bold word are translated in pieces, which limits how freely a translator can reorder them.
- Languages with complex plural rules would need more than "one or many".

## Revisit when

- A third language arrives, or one with plural rules beyond singular and plural.
- Service errors need to be translated (they would need error codes).
