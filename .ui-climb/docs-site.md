# UI hill climb: documentation site (2026-11-12)

A short climb (the maintainer asked for a lighter version than the web UI's) on the new Starlight site at `/exhale/docs/`.

- **Screens:** working = home (1440 dark, 390 dark), install/macOS (1440 dark), an API operation (1440 dark), guides/reminders (1440 light). Holdout = an ADR (1440 dark), install/Linux (390 dark).
- **Anchors:** Linear docs (Start Guide) and Vercel docs (Getting started), dark, 1440.
- **Council:** Haiku and Sonnet every round, clean context, same brief. Gate = the lower of the two overall scores.

## Ledger

| Round | Change | Haiku | Sonnet | Gate | Verdict |
|---|---|---|---|---|---|
| 00 | Baseline: Starlight mapped to the app's greys, Geist, monochrome sidebar | 7.0 | 5.5 | 5.5 | |
| 01 | Code in greys (min-dark/min-light), API method badges in greys, brighter secondary text, outline TOC marker, quiet phone menu button | 7.0 | 6.5 | 6.5 | Accepted |
| 02 | Home: Install / Download buttons and a reminder drawn in HTML (layout and buttons of `reminder_conversation.golden`) | 6.5 | 6.5 | 6.5 | Not above noise; kept for round 03 |
| 03 | Reminder emoji in text presentation, no rule under the title, first paragraph as lead | 7.0 | 6.5 | 6.5 | Held; sub-scores up |
| 04 | Emoji as text site-wide, smaller lead on phones, sentence-case group labels | 7.0 | 7.0 | 7.0 | Accepted |
| close | Group labels white 600, clearer active item; code frames fixed (see below). Scored on all 7 screens | 6.5 | 7.0 | 6.5 | |

Anchors scored 8.0–9.0 throughout.

**Holdout** (blind A = baseline, B = final): Sonnet 7.0 → 7.5, Haiku 7.0 → 6.5. Haiku's drop pointed at a real defect: code blocks had lost their frame in the dev server. The cause was Astro's content cache keeping the link to the previous Expressive Code stylesheet after its config changed (fixed by deleting `docs/.astro`); the production build was never affected.

## What the council still asks for

Structural work, left for later:

- One monochrome icon set (cards, sidebar groups), and a decision on the emoji of real button labels in prose.
- API reference: parameter descriptions and required markers (most come from the OpenAPI document, so this starts in the Go structs' `doc` tags), response examples.
- Install guides: Starlight `<Steps>` and `<Aside>` for the first-launch warnings, tabs for distro and CPU, screenshots of the Gatekeeper and SmartScreen dialogs.
- ADRs: status and date as a header strip.
