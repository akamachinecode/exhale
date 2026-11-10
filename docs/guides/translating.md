# Translating Exhale

Exhale speaks English and Spanish. Adding a language takes no Go or TypeScript: you register it, translate two JSON files, and run two checks that list what is left. How it works and why: [ADR 0023](../adr/0023-message-translations.md).

## What there is to translate

Texts are written in English where they are used, and the English text is the key of every translation. A language has two files:

| File | What it holds | Placeholders |
|---|---|---|
| `internal/i18n/locales/<code>.json` | Chat (Telegram, Slack), reminders, and what the server says: errors, the assistant's cards | Go verbs: `%s`, `%d`, `%q`, `%v` |
| `web/src/locales/<code>.json` | The web app | `{0}`, `{1}`…, and `{n}` for a count |

Both are flat JSON: `"English text": "your translation"`. An empty translation (`""`) means "not yet": that text shows in English.

The web never translates the server's texts itself. `go test ./internal/actions -update` copies them from the first file into `web/src/locales/server/<code>.json`, with `{0}` in place of the verbs. Do not edit that copy.

## Add a language

Portuguese (`pt`) as the example. Use the ISO 639-1 code.

1. **Register it** in `internal/i18n/languages.json`, with its name in the language itself:

   ```json
   { "code": "pt", "name": "Português" }
   ```

   Without `"complete": true` the language is *incomplete*: it can ship half done, missing texts show in English, and the checks list what is missing without failing.

2. **Create the two files**, with an empty entry for every text:

   ```sh
   go test ./internal/actions -update     # internal/i18n/locales/pt.json
   pnpm -C web i18n --update              # web/src/locales/pt.json
   ```

   Run both again whenever you like: they add the texts that are new, drop the ones no longer used, and keep what you translated.

3. **Translate** the empty entries. Start with the web file and with the chat texts of reminders (search for 🔔, ⏰, ⌛): they are what people see most.

4. **Check** (see below), then open a pull request. Partial translations are welcome.

5. **When every text is translated**, add `"complete": true` to the language. From then on a missing translation fails CI, so the language stays complete.

The language then shows in the web's language picker, in Preferences, and in the chat settings, by the name you gave it. Telegram shows the bot's commands in it to people whose Telegram is in that language.

If the Go test says there is no plural rule for the language, add its CLDR rule to `internal/i18n/plural.go` and the line for it in `internal/i18n/i18n_test.go`: the test tells you what browsers say for each number. `pt`, `fr`, `de`, `it`, `ca`, `nl`, `pl`, `ru`, `uk`, `cs`, `ro`, `ja`, `zh`, `ko` and a few more are already there.

## Run the checks

```sh
go test ./internal/actions -run 'TestEveryMessageIsTranslated|TestServerTexts' -v   # chat and server texts
pnpm -C web i18n                                                                   # web texts
```

Both check every registered language and say, per language:

- texts with no translation (an error for a complete language, a list for an incomplete one);
- translations no longer used, because the English changed (the same);
- translations with other placeholders than their English text, or plural forms the language does not have (always an error).

You do not need a running Exhale. To see your work, run Exhale locally (`CONTRIBUTING.md`) and choose the language in the picker at the bottom of the sidebar.

## Plurals

A text with a count has a singular and a plural in English, and both are keys:

```json
"{n} subscription": "{n} assinatura",
"{n} subscriptions": "{n} assinaturas"
```

Exhale picks one with the language's plural rules ([CLDR](https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html)), so in Portuguese 0 takes the singular too. A language with more forms gives the plural key one text per form, and the singular key can stay empty:

```json
"{n} subscriptions": { "one": "{n} subskrypcja", "few": "{n} subskrypcje", "many": "{n} subskrypcji", "other": "{n} subskrypcji" }
```

The forms are `zero`, `one`, `two`, `few`, `many` and `other`; `other` is required. In chat the counted word is often alone (`"subscription"`, `"subscriptions"`), next to a number in another text (`"It pays %d %s"`).

## Style

Follow the Spanish translation (`internal/i18n/locales/es.json`, `web/src/locales/es.json`):

- **Plain and short.** Reminders are read on a phone lock screen. Say what happens, not how the software works.
- **Informal.** Spanish uses *tú* ("¿Te cobraron Netflix?"), not *usted*. Use your language's everyday form of address.
- **Keep every emoji** at the start of a text and every placeholder (`%s`, `%d`, `{0}`, `{n}`). You may move placeholders. In Go texts, give them numbers when you swap them: `%[2]s … %[1]s`.
- **Keep words that are names:** Exhale, Telegram, Slack, MCP, the names of plans and fields that the API uses (`contract`, `invoice`…) when they appear between quotes or in error messages about the API.
- **Dates:** the chat file has four lists, `"Sun, Mon, …"`, `"Sunday, Monday, …"`, `"Jan, Feb, …"`, `"January, February, …"`. Translate them as lists with the same number of names, separated by commas, in your language's case (Spanish writes them in lower case). The web formats dates with the browser.
- **One text, one translation.** The same English text in two places has one translation. If it reads wrong in one place, say so in the pull request: the English can be split.
- **No machine translation** sent as is. Use tools if they help, but read every text in place.
