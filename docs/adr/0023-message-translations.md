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

Messages are written in English at the call site through `tr(locale, "English text %s", args…)`. A catalog per language (`internal/actions/i18n_es.go`; *since 2026-11-10 `internal/i18n/locales/<code>.json`, see the implementation notes*) maps the English text to its translation; a missing entry falls back to English. Dates, relative days ("in 3 days") and billing cycles ("monthly", "every 3 months") have their own small helpers. Translations may reorder arguments with indexed verbs.

A test parses the package and fails when a text passed to `tr` or `plural` has no Spanish translation, when a translation is no longer used, or when a translation takes different arguments than its English text.

Texts a channel says outside any screen (linking a chat, joining, help) live in the action layer too, so Telegram and Slack say the same thing. An unlinked chat is answered in the language the chat platform reports.

The **web** follows the same idea in TypeScript: `t("English text {0}", value)` and `tn(count, "one", "many")` in `web/src/lib/i18n.ts`, with the Spanish catalog in `web/src/lib/es.ts` (*now `web/src/locales/<code>.json`*). The language is the browser's unless the person switches it (saved in that browser); dates, amounts and billing cycles follow it. `pnpm i18n`, run in CI, applies the same three checks as the Go test.

Errors raised by the service (validation messages) stay in English for now. *(Updated 2026-11-10: they are translated now, with the English text still as the key; see "Finished later" in ADR 0018's implementation notes and the rule in AGENTS.md.)*

## Consequences — cost we accept

- Changing an English sentence breaks the test until its translation is updated. That is the point.
- The web language is per browser, not per account: someone using two devices chooses twice. *(Solved 2026-11-10: each person has a language of their own; see the implementation notes.)*
- Sentences split around a link or a bold word are translated in pieces, which limits how freely a translator can reorder them.
- Languages with complex plural rules would need more than "one or many".

## Revisit when

- A third language arrives, or one with plural rules beyond singular and plural.
- Service errors need to be translated (they would need error codes).

## Implementation notes: a language is a contributor-sized job (2026-11-10)

The market scan (`docs/research/2026-11-10-feature-scan.md`) leaves more languages for later but asks for the structure now: translations bring contributors. Adding Portuguese now means registering it in one place and translating two JSON files; the checks say what is left. The steps are in [docs/guides/translating.md](../guides/translating.md). No Spanish text changed.

**Registered in one place.** `internal/i18n/languages.json` lists the languages (code, own name, complete or not). Go embeds it; the web imports the same file. Chat settings, the web's picker and Preferences list the languages from it, each by its own name; Telegram publishes its command menu in each.

**Two files per language, not one shared.** Considered: one JSON per language, embedded in Go and imported by Vite. Rejected:

- the placeholders differ: Go verbs carry formatting (`%q`, `%02d`, indexed `%[2]s`), the web uses `{0}` and `{n}`. A shared file needs a runtime conversion and rules for a literal `%` in web text;
- the web would download ~750 chat texts it never shows, and the binary would carry ~1000 web texts twice (it embeds the built web already).

So `internal/i18n/locales/<code>.json` holds chat and server texts, and `web/src/locales/<code>.json` the web's. The only texts both sides show, the server's (`domain.Text`), are translated once, in the Go file: `go test ./internal/actions -update` writes the web's copy to `web/src/locales/server/<code>.json` with `domain.WebKey`, and a test fails when it is stale. The web merges it under its own catalog, so a web text that is also a server text keeps a wording for its place on screen (three do in Spanish: "expires", "From", "Delete {0}"). `web/scripts/server-texts.json` is gone. Both files are flat JSON from English to the translation, which translators and tools such as Weblate read without knowing Go or TypeScript.

**Only the chosen language downloads.** `i18n.ts` loads the two catalogs with `import.meta.glob`, so each is its own chunk (Spanish: ~28 kB gzipped; English: nothing). It awaits them at the top level, so texts translated when a module loads (tables of labels) already see the catalog.

**Checks per language.** The Go test (`TestEveryMessageIsTranslated`) and `pnpm i18n` check every registered language for missing texts, unused texts, placeholders that differ (Go compares the arguments by index, so a translation may reorder them) and plural forms the language lacks. A language with `"complete": true` fails CI on a missing or unused text; Spanish is complete. An incomplete one ships with English in its gaps and only gets the list. `-update` / `--update` gives an incomplete language an empty entry for each missing text and drops its unused ones, so a translator works from the file alone.

**Plurals follow CLDR.** The web uses `Intl.PluralRules`. Go has a small table of rules for whole numbers (`internal/i18n/plural.go`, 24 languages), compared in a test with what `Intl.PluralRules` says. A translation of the plural key may be one text per category (`{"one": …, "few": …, "many": …, "other": …}`); a plain string keeps the two-form behaviour. A text with no translation is counted in English. Chat's day and month names became four translated lists ("Sun, Mon, …") in the same file.

**Choosing the language.**

| Where | Order |
|---|---|
| Chat, reminders, the assistant | the member's own language, else the household's |
| Web, signed in | the same, reported by the session (`language`); the page reloads into it when it differs |
| Web, before sign-in | the one last shown in this browser, else the first registered language in `navigator.languages` (what the browser sends as `Accept-Language`; the server renders no pages, so reading it in the browser needs no server change) |
| A chat Exhale does not know yet | the language the chat platform reports, if registered |

The member's language is new (migration 00027, `members.locale`, empty for the household's). The web's picker saves it (`POST /api/v1/auth/language`), so it holds on every device; joining from the web in a language other than the household's keeps it. Chat settings still change the household's language.

Cost we accept: a per-browser choice made before this change is not carried over; once signed in, the page shows the person's or household's language until they pick again. There is no button yet to go back to following the household (the API takes an empty language). The household language changed from the web reloads the page of everyone without a language of their own.

