# Feature scan: Wallos and the subscription-app market

- Date: 2026-11-10
- Question: which features of Wallos and of the well-known subscription and expense apps are worth having in Exhale, which are not, and which would be worth it only if done better.

## Method and limits

- **Wallos:** its source was read at commit `50e92d7` (v5.11.1), plus its GitHub issues and discussions sorted by votes. Nothing was executed, so the date bugs below come from reading the code.
- **Other apps:** official sites, store listings, about 600 App Store reviews, vendor forums and GitHub issues. Reddit could not be opened, so user sentiment is weaker than it should be. Prices and some feature claims come from the vendors' own listings.
- **Apps covered:**
  - Manual trackers: Bobby, Subby, Subscriptions (Touchbits), TrackMySubs, Orbit, Chronicle, Subscription Day, ReSubs, Subtrack, Tilla, Outflow, Subo.
  - Bank-linked finance apps: Rocket Money, Monarch, Copilot, YNAB, Simplifi, PocketGuard, Lunch Money, Emma, Snoop, Cleo, Hiatus, Mint, and the built-in views of Revolut, Monzo, N26, Capital One, Apple, Google Play, PayPal and Privacy.com.
  - Self-hosted: SubTrackr, Firefly III, Actual, Sure, ezBookkeeping, Paisa, Wapy.dev, Renewlet, Zublo, SubTracker.
  - Business tools: Torii, Zylo, Ramp, Spendesk, Cledara, Zluri, 1Password SaaS Manager, Vendr.

## Verdicts used

| Verdict | Meaning |
|---|---|
| **Have** | Exhale already does it, usually better. |
| **Build** | Worth having; not in Exhale yet. |
| **Build better** | Weak or a gimmick as others ship it, but valuable in the form described. |
| **Later** | Worth it, but not before 1.0. |
| **Skip** | Not worth it for Exhale, even improved. |

## The main finding

The market fails at three things, and they are the same in every category:

1. **Reminders that are wrong, lost or cannot be answered.** "Notifications don't work" is the top one-star reason in manual trackers. Wallos sends one reminder on one exact day with no retry and no log. No major app lets you act inside the reminder; only Subby claims it.
2. **No memory.** Wallos and most trackers keep only the current state: no payment history, no price history. "Lifetime spend" is a guess.
3. **The dates that matter are not the renewal date.** Users ask for trial end, contract end and "cancel by" (notice period). Wallos has left trials open since 2023 and end dates since 2024.

Exhale already solves the first two by design (charges as a ledger, reminders through an outbox, the message as the app). The third is the biggest gap and the clearest next step.

## Part 1: Wallos, feature by feature

### Subscriptions and billing

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Subscription fields | Name, price, currency, cycle, next payment, start date, payment method, URL, notes, categories, payers | **Have**, one gap | Add a **start date** field. Today the Wallos import had to put it in the notes. |
| Cycles | Every N days, weeks, months or years | **Have** | Same range. "Third Wednesday" and weekend shifting are requested elsewhere: **Have** since 2026-11-10 (ADR 0008 notes). |
| Advancing the next date | A nightly cron adds the interval; month-end dates drift (31 Jan becomes early March for good) | **Have** | Dates are computed from the anchor, so they never drift. |
| Mark as paid | One "paid" date that resets each cycle (added in 5.11 after years of requests) | **Have** | Each charge is a row: pending, paid, failed or skipped, with the real amount. |
| Auto-renew off / manual renew | Date stays in the past until the user clicks; no reminder for overdue ones | **Have** | "Needs attention" plus a follow-up message after the due date. |
| Inactive + replacement = "savings" | An inactive subscription counts as money saved, minus its replacement | **Build better** | Show "you stopped paying X per year" from cancelled subscriptions. Skip the replacement link; it is rarely used. |
| One-time payments | A cycle type counted as zero per month | **Build better** | See "non-renewing items" in Part 2: domains, licences and lifetime purchases with an expiry date. |
| Cancellation date reminder | Fires only on the exact date, must be re-entered every year, missing on four channels | **Build better** | Replace with notice period and "cancel by" (Part 2). This is Wallos's most confusing feature and a top request. |
| Notes in Markdown, URL | Free text and a link | **Have** | Add a separate **cancel/manage link** (Part 2). |
| Folders, clone, bulk edit, filters | List tools for people with many subscriptions | **Later** | Filters and search matter first. Folders overlap with categories: **Skip** folders. |
| Several categories per subscription | Cost split evenly among them | **Skip** | One category keeps totals honest. Tags would cover the rest, **Later**. |

### People and sharing

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Household members | Just a name and an email; they cannot log in or see anything | **Have** | Real accounts with invites, from the web or Telegram. |
| Several payers, even split | Cost divided equally, no custom shares | **Build** | Shared subscriptions between real accounts is Wallos's top open request (three issues, 23 votes on the largest). Add per-person shares and "who owes whom this month". |
| Multi-user instance | Open registration, user cap, email verification | **Skip** | Exhale is one household per instance by decision. |

### Notifications

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Reminder engine | One reminder when the day count matches exactly; fixed 09:00; instance time zone; lost if the container is down; duplicates on manual runs | **Have** | Several reminders per charge, chosen hour, household time zone, outbox with deduplication and catch-up. |
| Message content | Plain-text digest, no buttons | **Have** | The message is the app: pay, move, change, cancel in place. |
| 11 channels | Email, Discord, Gotify, Telegram, Pushover, ntfy, PushPlus, Mattermost, ServerChan, webhook, Web Push | **Build**, fewer | Telegram and Slack (interactive), then email, ntfy and a templated webhook. That covers the long tail. **Skip** PushPlus, ServerChan and Mattermost as built-ins. |
| Templated webhook | Variables, custom headers, SSRF protection | **Build** | Cheap, and it replaces most niche channels. Keep the SSRF protection. |
| Web Push | Browser notifications from the PWA | **Later** | Already in the vision, with action buttons. |
| Pay-period summary line | "You need X this period, Y left of budget" | **Build better** | See "due before payday" in Part 2. |

### Calendar, statistics, budget

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Calendar | Month view with projected charges and a total | **Have** | Web calendar and "upcoming" in Telegram. |
| iCal feed | One event per subscription, next payment only, API key in the URL | **Build better** | A feed of real upcoming charges with a revocable signed link. Requested in Wallos, Touchbits and Monzo. |
| Totals and splits | Monthly and yearly cost, by category, payment method, member and currency | **Have** | Spending summary. |
| Trend | A weekly snapshot of the yearly total | **Build better** | The charge ledger gives real spend per month, not a snapshot. |
| Lifetime spend, heaviest month, average age, cheapest and dearest | Estimated from current prices | **Build better** | Real paid totals per subscription from the ledger. Skip the trivia charts. |
| Budget (monthly and pay-period) | A budget number with an over-budget warning | **Build better** | A plain budget number is weak. The valuable part is the pay-period view (Part 2). |

### Money

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Multi-currency | Latest rate only; needs an API key except Frankfurter; a missing rate silently mixes currencies in totals | **Have** | ECB rates without a key; totals say when something could not be converted. |
| Prices as floating point | Rounding errors possible | **Have** | Integer minor units. |

### AI

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| AI recommendations | One prompt returns 3 to 7 generic saving tips | **Build better** | Tips are garnish. What works is an agent that can read and act (MCP, already built) and a deterministic **audit** tool (Part 2). |
| Translate categories with AI | A button | **Skip** | |

### Logos and look

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Logo search | Scrapes image search engines; has broken several times | **Build better** | Exhale bundles brand marks and falls back to a monogram, offline. Add "upload your own" and a larger bundled set. Never scrape. |
| Logo background removal, light and dark variants | Experimental image processing | **Skip** | |
| Themes, custom colours, custom CSS | Five themes plus free CSS | **Skip** | Light and dark, done well. Custom CSS is a support burden. |
| 29 languages | Community translations | **Later** | Spanish and English first. Translations bring contributors, so plan the structure early. |
| PWA | Manifest, service worker, mobile navigation | **Later** | Telegram is the mobile app for now. |

### Access and administration

| Wallos feature | How Wallos does it | Verdict | In Exhale |
|---|---|---|---|
| Password login | Remember-me cookie; no lockout found | **Have** | With throttling. |
| OIDC | Configurable by UI or environment | **Build** | The most voted Wallos request ever (54) and the top request in SubTrackr. Already planned in ADR 0006. |
| TOTP | With backup codes | **Later** | Planned. OIDC covers most people who want it. |
| REST API | API key in the URL, no documentation | **Have** | OpenAPI, scoped tokens in headers. |
| Export CSV / JSON | Per user | **Build** | Missing export is punished in every category ("lock-in"). |
| Import | None, only a full backup at first run | **Build** | See Part 2. A **Wallos importer** is the first one: two newer competitors already ship one. |
| Backup and restore | Manual zip, admin only | **Build better** | Scheduled backups of the one data folder. |
| Docker | Not rootless; cron inside the container is fragile | **Have** | One binary, no cron. |

## Part 2: features from the rest of the market

### Worth building

| Feature | Best version seen | Why | How Exhale should do it |
|---|---|---|---|
| **Notice period and "cancel by" date** | Torii and 1Password SaaS Manager compute a deadline from notice days; SubTrackr added it in August 2026 | Gyms, insurance, telecoms and annual plans all have it. It is the date that costs money when missed. | Notice days per subscription give a computed "cancel by" date. Reminders count down to it, not to the charge. |
| **Renewal decision** | Ramp and 1Password: renew, cancel, undecided, snooze | A reminder should end in a decision, not in being dismissed. | For annual or large charges the message asks "Keep or cancel?" and records the answer. |
| **Free trials** | Subby: you enter the price it becomes; alert before the first charge | Wanted everywhere, open in Wallos since 2023. | Trial end date plus the price it converts to. The message offers "Cancel before it charges" with the cancel link. |
| **Price history and scheduled price changes** | Orbit: a future price applies on its date; billing-history timeline | Voted 13 in Wallos and rejected there. | Exhale already stores each charge's amount and schedule versions. Show it as a timeline and allow "price changes to X from date Y". |
| **Price-increase detection** | Rocket Money: alerts above about 5% | Liked by users; needs no bank link. | When a charge is confirmed with a different amount, ask "New price from now on?" and report the yearly difference. |
| **Cancel / manage link and account email** | Orbit and Touchbits fields | Small, and praised more than cancel-guide libraries. | Two fields; the link appears as a button in the cancel flow. |
| **Card-expiry impact** | Nobody ships it | "These 7 subscriptions use the card that expires next month." | Exhale already tracks expiry. Add the list of affected subscriptions and a one-step "move them to another card". |
| **Cost splitting between members** | Outflow: split by share | Wallos's top open request. | Shares per subscription and a monthly "who owes whom". |
| **Monthly equivalent and "set aside"** | Chronicle and YNAB | Annual bills surprise people. Most repeated wish in finance apps. | Show every subscription's monthly equivalent and "set aside X per month for the yearly ones". |
| **Due before payday** | Sure, Monzo beta, Wallos pay period | Needs only a payday date, not a bank balance. | Optional payday setting; home shows "X due before the 28th". |
| **Import with a review queue** | Monarch's "Recurring Review": candidates wait for approval | Onboarding is where people quit manual trackers. Screenshot and email imports that save directly get the worst reviews. | Statement, CSV or screenshot produce drafts that the user accepts, merges or rejects. An agent can do the same over MCP. |
| **Audit tool for agents** | Sure's `get_bill_audit`: duplicates, price changes, trials about to convert, overdue | Turns "AI tips" into facts. | One deterministic tool and one screen: duplicates across members, price rises, trials ending, cards expiring, overdue. |
| **Non-renewing items** | Requested on Hacker News; Subo has prepaid and fixed-term plans | Domains, licences, passports and prepaid plans expire without renewing. | An item with an expiry date and reminders, no charges. Replaces Wallos's "one-time". |

### Worth it only if done better

| Feature | Why it fails today | The version worth building |
|---|---|---|
| **"Is it worth it?" usage ratings** | Apps that ask users to log usage have no traction. | A periodic "Still using this?" inside the reminder, with the yearly cost. One tap. |
| **Cancel for you** | Rocket Money's concierge is its top source of complaints and cannot handle app stores. | The cancel link plus notice date, and an agent that walks the user through it. No human operations. |
| **Duplicate detection** | A dashboard in business tools. | One message: "You and Ana both pay for Spotify." |
| **Bank matching** | Bank links break constantly; the most common complaint in finance apps. | Confirm expected charges from an imported statement. No live connection. |
| **Snooze and repeat until handled** | Almost nobody has it; requested in Wallos. | "Remind me tomorrow" in the message. Exhale's follow-up already repeats once; make it configurable. |
| **Documents and invoices** | Storage alone is dull. | Attach a contract or invoice, and let the assistant read the notice period from it. **Later**. |
| **Annual versus monthly hint** | No app does it. | Only when the user records both prices. Low priority. |

### Skip

| Feature | Why |
|---|---|
| Live bank connection | Breaks often, costs money, needs a central service. Against Exhale's principles. |
| Bill negotiation | The most resented feature in the market: fees and fake savings. |
| Merchant or card blocking, virtual cards | Only a card issuer can do it. Blocking does not cancel the contract either. Document the pattern instead. |
| Low-balance warnings, "safe to spend" | Need live balances. |
| Approval chains, seat usage telemetry, price benchmarks | Business features that need pooled data or company structure. |
| Curated cancel-guide library | Expensive to keep current; the link field gets most of the value. |
| Personalities, confetti, alternate icons | Decoration. Subtrack's confetti on every "paid" annoys users. |
| A public status page, ten built-in channels | A webhook covers them. |

## Part 3: what Exhale already has that nobody else does

- **Reminders you can answer in place.** Not found in any major app or any self-hosted tracker; business tools only do it for spend approvals.
- **A charge ledger in a standalone tracker.** Only full finance apps (Actual, Sure) have one.
- **Safe agent access.** Preview, confirm and undo on every write. Monarch shipping an official MCP connector with write actions confirms the direction.
- **Real household accounts.** Wallos's top open request.

## Recommended order

1. **Dates that matter:** notice period with "cancel by", free trials, start date, non-renewing items, cancel link. One piece of work on the subscription model, and the largest gap against user demand.
2. **Memory made visible:** price timeline, price-change question on confirm, real spend per month and per subscription.
3. **Getting data in and out:** Wallos importer, CSV export, then the review queue for statements and screenshots (already in M2).
4. **Sharing money:** shares per member and "who owes whom".
5. **Planning:** monthly equivalent, "set aside", due before payday, card-expiry impact, iCal feed.
6. **Audit:** one tool and one screen for duplicates, price rises, trials ending and cards expiring.
7. **Reach:** OIDC, email, ntfy and webhook channels.
