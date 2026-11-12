<div align="center">

<img src="site/favicon.svg" width="72" height="72" alt="">

# Exhale

### Breathe out. Your subscriptions are handled.

Exhale reminds you before every renewal and lets you fix it **from the reminder itself**:<br>
mark it paid, move the date, change the card, cancel. In Telegram, Slack or your browser.<br>
It runs on your own computer, so your data stays with you.

**[Download](https://akamachinecode.github.io/exhale/)** · **[Documentation](https://akamachinecode.github.io/exhale/docs/)** · **[Releases](https://github.com/akamachinecode/exhale/releases)**

[![Release](https://img.shields.io/github/v/release/akamachinecode/exhale?include_prereleases&sort=semver)](https://github.com/akamachinecode/exhale/releases)
[![CI](https://github.com/akamachinecode/exhale/actions/workflows/ci.yml/badge.svg)](https://github.com/akamachinecode/exhale/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)

Free and open source · macOS, Windows, Linux and Docker · English and Spanish

<br>

<img src="site/img/overview.webp" alt="Exhale's overview: monthly and yearly spend, charges that need you, the next 30 days and spend by category" width="900">

</div>

<br>

## The reminder is the app

Most tools tell you Netflix renews in three days and leave the rest to you. Exhale's reminder is where you deal with it. This is a real one, in Telegram:

```text
🔔 Spotify Family renews in 3 days
   💶 €17.99 · Sun, 8 Nov 2026
   💳 Revolut ••0937

   [ ✅ Already paid ]      [ 📅 Change date ]
   [ 💳 Payment method ]    [ ✏️ Amount ]
   [ ⏭ Skip this one ]      [ ❌ Cancel subscription ]
   [ 💤 Remind me tomorrow ] [ 💤 In 3 days ]
```

One tap and the message updates in place, with an undo. The web updates at the same moment.

## Why people switch to Exhale

|  |  |
|---|---|
| 🔔 **Reminders you can answer** | Already paid, a new date (this charge or from now on), another amount or card, skip, cancel, undo. Also by email, ntfy, web push and webhook. |
| 📱 **The whole app in a chat message** | A pinned Telegram message is Exhale itself: spend, upcoming charges, every subscription, an add wizard, payment methods and settings. It redraws when anything changes. |
| ⏳ **The dates that cost money** | Free trials before they charge, the last day to cancel, renewals that need you (domains, licences), fixed-term contracts and price rises announced ahead. |
| 📆 **Billing days as banks charge them** | Day 31 in short months, "the third Wednesday", weekends moved to a business day. Counted from the first charge, so dates never drift. |
| 🩺 **A check-up of facts, not tips** | The same service paid twice, prices that went up this year, trials about to charge, a card about to expire and everything that depends on it. |
| 💶 **Every currency, exact** | Totals in your currency at ECB rates, what each subscription has really cost you, and how much to set aside each month for the yearly bills. |
| 👥 **Shared, not mixed** | A partner or flatmates get their own account and reminders. Split a subscription and see who owes whom. Private ones stay private. |
| 🤖 **Ask it anything** | Plain language in chat, or Claude, ChatGPT and local models over MCP. Every change shows a preview first and can be undone. |
| 📥 **Bring everything with you** | Import from Wallos, a spreadsheet or your bank statement (CSV or PDF). Export it all as JSON, CSV or ZIP, or follow it in your calendar. |
| 🔒 **Yours, on your computer** | One program, one data folder, a backup every day. No cloud, no telemetry, no account with us, no paid tier. |

## Up and running in a minute

1. **Download and open it.** Get `Exhale.dmg` (macOS), `Exhale-Setup.exe` (Windows) or a `.deb`/`.rpm` (Linux) from the **[download page](https://akamachinecode.github.io/exhale/)**. Exhale opens in its own window and starts by itself with your computer.
2. **Create your account.** The home page walks you through the rest. Making your own Telegram bot takes a minute and is guided.
3. **Add your subscriptions.** By hand, from Wallos or a spreadsheet, or from your bank statement. Reminders start the same day.

No public address needed: Exhale talks to Telegram and Slack with outgoing connections only, so it works behind a home router.

### On a server or NAS

An always-on machine (a home server, a Raspberry Pi, a VPS) never misses a reminder:

```sh
docker run -d --name exhale --restart unless-stopped \
  -p 8484:8484 -v exhale-data:/data \
  ghcr.io/akamachinecode/exhale:latest
docker logs exhale   # shows the one-time setup code
```

Open `http://<server>:8484`, enter the setup code and follow the steps on the home page. The [install guide](docs/install/README.md) covers Linux packages with a hardened systemd service, [HTTPS and remote access](docs/install/https.md), [backups](docs/install/backups.md) and [single sign-on](docs/install/sso.md).

## Questions before installing

<details>
<summary><b>Does my computer have to be on?</b></summary>

Reminders go out while Exhale is running. On a laptop that sleeps, Exhale can keep it awake (Settings → This computer), and anything due while it slept is sent when it wakes. A home server or a Raspberry Pi that never sleeps is ideal.
</details>

<details>
<summary><b>Where is my data?</b></summary>

In one folder on your computer, backed up every day. Nothing is sent anywhere unless you set up a channel such as Telegram.
</details>

<details>
<summary><b>Can I use it on my phone?</b></summary>

Yes. Reminders reach your phone through Telegram or Slack, and the web works on a phone. Exhale itself runs on a computer.
</details>

<details>
<summary><b>Is it really free?</b></summary>

Yes. Exhale is free software under the AGPL-3.0, with no paid tier, no account with us and no telemetry.
</details>

<details>
<summary><b>I use Wallos. Can I move over?</b></summary>

Yes. Import your Wallos database and review every subscription before it is saved.
</details>

## Learn more

The **[documentation site](https://akamachinecode.github.io/exhale/docs/)** has every guide, with search, the REST API reference and [`llms.txt`](https://akamachinecode.github.io/exhale/docs/llms.txt) for language models. A few places to start:

- [Reminders that end in an answer](docs/guides/reminders.md) and [reminders by email, ntfy and webhook](docs/guides/notifications.md)
- [Slack](docs/guides/slack.md) and [Exhale for a team](docs/guides/teams.md)
- [The in-app assistant](docs/guides/assistant.md) and [connecting a remote AI agent](docs/guides/remote-agents.md)
- [Tools for many subscriptions](docs/guides/many-subscriptions.md)
- [Vision](docs/vision.md) and [architecture decisions](docs/adr/README.md)

> **Status:** Exhale is young (0.x). It is used every day by its author; read the [release notes](CHANGELOG.md) before upgrading.

## Contributing

Contributions are welcome, from a translation ([two JSON files](docs/guides/translating.md)) to a new feature. Read [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md), the map of the code for people and coding agents. Every technical decision is recorded as an [ADR](docs/adr/README.md).

```sh
git clone https://github.com/akamachinecode/exhale && cd exhale
mise install        # Go, Node, pnpm and the other tools
mise run build      # builds the web UI into the binary: bin/exhale
./bin/exhale serve  # http://127.0.0.1:8484
mise run test       # Go tests with the race detector
```

Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## Licence

[AGPL-3.0-or-later](LICENSE). Free to use, change and share.
