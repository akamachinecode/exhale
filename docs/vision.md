# Vision

> **Exhale** — the feeling when your subscriptions are finally handled. Binary `exhale`, images `ghcr.io/akamachinecode/exhale`, env prefix `EXHALE_`.

## The problem

Managing subscriptions today is spread across several tools — a self-hosted tracker (Wallos), Apple Reminders and Calendar, and the bank's own subscription view (Revolut). None of them is good enough on its own.

The most common failure is a reminder that is **already wrong when it arrives**: "Revolut renews in 3 days", but the subscription was cancelled, the charge was delayed, the date moved, or the payment method changed. Fixing it means leaving the notification, opening another app and finding the record.

## The idea

**The notification is the interface.** The reminder that reaches you on Telegram or Slack lets you fix everything right there: mark it paid, change the date (this charge only or the whole series), change the amount or payment method, cancel, pause, or edit the reminder itself. The message updates in place, and the web dashboard reflects the change instantly.

**Agent-ready from v1.** Any AI agent (Claude, ChatGPT, Grok, local models…) connects in a minute, gets full context and can read and manage everything — safely, with confirmations and undo. The app also understands plain language in chat and can import bank statements.

Around that core, a modern, beautiful and powerful dashboard: multi-currency, calendar, statistics, households, and everything Wallos offers — each part done better.

## Principles

1. **Self-hosted, nothing central.** No cloud relay, no infrastructure we run, no accounts with us, no paid tier.
2. **It just works, all the time.** Reminders are never lost or duplicated, a failing piece never takes down the others, and the app recovers on its own after a restart.
3. **Light.** One binary, one process, one data folder. Tens of MB of RAM, not hundreds.
4. **Zero telemetry, ever.** Nothing leaves the machine unless the user configures it.
5. **Observable.** Users can see what the app did and why ("why didn't I get that reminder?"), and get alerted when something is wrong.
6. **A well-run open-source project.** Public from day one, clear versions, commits and PRs, every technical decision recorded as an ADR.

## Non-goals

- WhatsApp (its API is paid).
- A hosted or SaaS version, or several isolated households on one instance (run another instance instead).
- Monitoring the whole host machine (Beszel, Netdata and node_exporter already do this well).
- Full personal-finance or accounting features (Firefly III, Actual Budget cover that).

## Channels

- **v1.0:** web, Telegram and Slack (interactive), and email, ntfy and templated webhooks (reminders only). The webhook with a body template covers Discord, Gotify, Pushover-style services and Home Assistant (ADR 0007).
- **Later:** interactive Discord, Telegram Mini App (requires HTTPS), PWA web push with action buttons, Signal (text only).
