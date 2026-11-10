# Exhale

> Breathe out. Your subscriptions are handled.

Exhale is an open-source, self-hosted subscription manager where **the reminder is the interface**. The message that tells you Netflix renews in three days lets you fix it right there: mark it paid, move the date, change the card or the amount, cancel. The message updates in place, and so does the web.

It is one program with one data folder. No cloud, no account with us, no telemetry, no paid tier.

**Status:** pre-release. It works and is used daily by its author; the first tagged version (0.1.0) is not out yet, so for now you build it from source.

## What it does

- **Reminders you can answer.** In Telegram (and Slack), each reminder carries the buttons that fit: already paid, new date (this charge or from now on), another amount, payment method, skip, cancel, undo. Also by email, ntfy and webhook as one-way notifications.
- **The whole app inside a chat message.** A pinned message in Telegram is Exhale itself: spend, upcoming charges, every subscription, an add wizard, payment methods, settings and a check-up. It redraws when anything changes.
- **The dates that actually cost money.** Free trials (reminds before the first charge), notice periods (reminds before the last day to cancel and asks "keep or cancel?"), things that expire unless renewed (domains, licences), fixed-term contracts, and price changes announced in advance.
- **Billing days as banks charge them.** Day 31 falls on the last day of shorter months and comes back on the 31st; "the third Wednesday" or "the last Friday"; charges that fall on a weekend moved to the next or previous business day. Dates are always counted from the first charge, so they never drift.
- **Memory.** Every charge is a row with its own state, so Exhale knows what you really paid, when the price went up and by how much, and what each subscription has cost since you track it.
- **A check-up of facts, not tips.** The same service paid twice, prices that rose in the last year, trials about to charge, deadlines, big yearly charges coming, a card about to expire and everything that depends on it, subscriptions you said you no longer use.
- **Sharing.** Other people get their own account and their own reminders. Split the cost of a subscription and see who owes whom each month. Private subscriptions stay private.
- **Money done properly.** Any currency, exact amounts, totals converted with ECB reference rates, how much to put away each month for yearly bills, and what is due before payday.
- **For AI agents.** Every operation is a tool over MCP, with a live briefing, previews before any change, confirmation for destructive ones and undo for everything. Claude, ChatGPT or a local model can read and manage your subscriptions safely.
- **Tools for many subscriptions.** Search by name, notes, account, category or tag; free tags; duplicate a subscription; change many at once (category, payment method, tags, pause, cancel, delete) with a preview and one undo; and, when you record a plan's other price (say, yearly), what switching saves. See [the guide](docs/guides/many-subscriptions.md).
- **Your data goes in and out.** Import a Wallos database, a CSV or an Exhale export through a review list; export everything as JSON, CSV or a ZIP with your attached documents; subscribe to a private calendar feed.
- **Easy to run.** A single binary, a container image, `.deb`/`.rpm` packages with a hardened systemd service, automatic daily backups, sign-in with a password or your identity provider (OpenID Connect). English and Spanish in chat.

## Quick start

Until 0.1.0 is published, build it:

```sh
git clone https://github.com/akamachinecode/exhale && cd exhale
mise install        # Go, Node, pnpm and the other tools
mise run build      # builds the web UI into the binary: bin/exhale
./bin/exhale serve  # http://127.0.0.1:8484
```

Open the address, create your account and follow the steps on the home page: create your own Telegram bot (guided), link your chat, add your subscriptions or import them, and connect your AI agent if you use one.

Exhale needs no public address: it talks to Telegram and Slack with outgoing connections only, so it works behind a home router.

Once releases exist, the [install guide](docs/install/README.md) covers Docker, Linux packages, macOS, HTTPS and backups.

## Documentation

- [Install](docs/install/README.md): Docker, Linux packages and service, macOS, [HTTPS and remote access](docs/install/https.md), [backups](docs/install/backups.md), [single sign-on](docs/install/sso.md)
- [Exhale for a team](docs/guides/teams.md)
- [Reminders by email, ntfy and webhook](docs/guides/notifications.md)
- [Reminders that end in an answer](docs/guides/reminders.md): snooze, asking again, duplicates, cards about to expire
- [Vision](docs/vision.md) and [architecture decisions](docs/adr/README.md)
- [What other tools do and what we took from them](docs/research/2026-11-10-feature-scan.md)

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md) (the map of the code, for people and coding agents). Commits follow Conventional Commits and are signed off (DCO). Every technical decision is recorded as an ADR.

```sh
mise run test   # Go tests with the race detector
mise run lint   # golangci-lint
mise run gen    # after changing SQL or the API
```

## Security

Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## Licence

[AGPL-3.0-or-later](LICENSE).
