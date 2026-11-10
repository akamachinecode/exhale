# Exhale for a team or small business

A team that shares SaaS tools has the same problem as a home: renewals nobody remembers, cards that expire, a tool still paid for after the person who needed it left. Exhale handles it the same way.

There is no business edition. A team runs the same Exhale as a home, with the same features ([ADR 0020](../adr/0020-one-binary-for-homes-and-teams.md)). It is free and stays free.

## How it maps

| In Exhale | For a team |
|---|---|
| One instance | One team. One server, one data folder |
| Household | The team. Name it after the team in the setup wizard or in Settings |
| Member | A person with their own account |
| Owner | Whoever administers Exhale for the team |
| Payer of a subscription | The person who gets its reminders |

Two teams that must not see each other's data run two instances (two containers, or two services with different data directories and ports). One instance never holds more than one team.

## Set it up

1. **Install it** on a machine that stays on: [Install Exhale](../install/README.md). For a team, the Linux package or Docker on a small server is the usual choice.
2. **Create the first account** in the web wizard. That person is the first owner.
3. **Give people access to the web page.** On the office network, plain `http://` is enough. For people outside it, use Tailscale or HTTPS: [HTTPS and remote access](../install/https.md).
4. **Add the team's Telegram bot** (Settings → Telegram). One bot per instance, created by you with @BotFather.
5. **Invite people.**

## Accounts and invites

An owner creates an invite in Settings → Sharing and chooses the role: member or owner. The invite is a one-time link, valid for 7 days:

- a **web link** (`/join/…`), for people who can open your Exhale page, or
- a **Telegram link**, which works from anywhere, once the bot is set up.

The person chooses their own name, email and password. Exhale never sends email: the address is only the sign-in name.

Each person then links their own Telegram account from Settings. Reminders for a subscription go to its payer, in their own chat, with the buttons to fix it there.

## Two-factor sign-in

Anyone who signs in with a password can add a second step: a 6-digit code from an authenticator app on their phone (1Password, Bitwarden, Google Authenticator, Aegis…).

1. Settings → You → Two-factor sign-in → **Turn on**, and enter your password.
2. Scan the QR code with the app, or type the key shown under it.
3. Enter the code the app shows. Two-factor sign-in is now on, and your other sessions are signed out.
4. **Save the 10 backup codes** shown next, in your password manager for example. Each one signs you in once, when the phone is not at hand. They are not shown again; **New backup codes** replaces them all.

From then on, sign-in asks for the password, then for a code. A code works only once, and many wrong codes in a row make Exhale wait before it accepts more. To turn it off, you need your password and a code (or a backup code).

It does not change anything else:

- **Single sign-on** never asks for it: your identity provider handles its own second factor.
- **API tokens** and connected AI agents keep working without codes.

Lost the phone and the backup codes? An owner can turn it off for you in Settings → Sharing (it shows in Activity), and then you sign in with the password alone. On the server, `exhale user reset-2fa --email …` does the same.

## Who is the owner

Owners can do what members cannot:

- invite people,
- change the team's settings (name, main currency, time zone, language, default reminder time),
- turn off someone's two-factor sign-in when they are locked out,
- set or remove the Telegram bot.

Everything else is open to every member: adding and editing subscriptions, confirming charges, payment methods, categories, their own Telegram link and their own API tokens.

**Have at least two owners.** Invite a second person as owner, so access does not depend on one account.

Whoever controls the server is above any owner: `exhale user reset-password --email …` on the server sets a new password for any account and signs it out everywhere. That is also the way back in when a password is lost. `exhale user reset-2fa --email …` turns off an account's two-factor sign-in, for a lost phone.

## What everyone sees

A subscription is **shared** by default: every member sees it. One marked **private** is visible only to the person who created it and to its payer.

The Activity page shows who changed what, from the web, Telegram or an AI agent, and each change can be undone.

## AI agents

Each member can create API tokens (Settings) for their own AI client or scripts, with a scope: read, write, or trusted. What an agent does is recorded under the name of its token. See the Agents page in the web for the connection details.

## Keep it safe

- **Backups:** on by default, daily, 14 kept. Put them on another disk: [Backups](../install/backups.md).
- **HTTPS** whenever the page is reachable from outside your network.
- **Upgrades:** see the guide for your install method. The database is copied before each upgrade that changes it.

## Not there yet

These are planned, not built. Do not rely on them today.

| Planned | Meanwhile |
|---|---|
| **Slack**: reminders and the same in-chat screens in Slack | Telegram, and the web |
| **Single sign-on (OIDC)** with your identity provider | Email and password per person |
| **Removing a member and changing roles** | Reset the person's password on the server, which signs them out of the web everywhere. Their Telegram link and API tokens keep working until removed from their own Settings, so do this together with them when they leave |
| **Splitting a cost between members** | One payer per subscription |
