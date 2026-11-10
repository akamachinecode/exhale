# Reminders by push, email, ntfy and webhook

Telegram (and Slack) are the channels where a reminder can be answered in place. Email, ntfy and webhooks are **one-way**: the message says what is due and links back to Exhale, where it can be fixed. Push notifications sit in between: one button works from the notification itself. Use them next to a chat, or alone if you only want to be told.

Email, ntfy and webhooks are set up in the web: **Settings → Channels → Other channels**. Owners configure the instance (the mail server, the ntfy server); each person adds their own destinations and can send a test message. Push notifications have their own section, below.

## The link back to Exhale

Messages link to the subscription they are about. For that Exhale must know its own address:

```
EXHALE_PUBLIC_URL=https://exhale.example.com
```

Without it the messages are sent without a link.

## Push notifications

Exhale can send reminders as notifications of the browser or of the installed app, on a phone or a computer. No other app is needed.

Turn them on in **Settings → Channels → Push notifications → Turn on for this device**, on each device you want them on. The browser asks for permission. The same section lists your devices; each has **Send test** and a remove button.

A reminder about a charge carries a button to mark it paid ("Already paid", or "Paid" when Exhale asks whether it was charged). It works without opening Exhale; the notification then says what changed. Tapping the notification opens the subscription. The button stops working after 3 days, or when the device is removed; then it opens Exhale instead.

What it needs:

- **HTTPS.** Browsers only offer push on `https://` addresses, and on `localhost` for testing. Put Exhale behind a reverse proxy with a certificate.
- **iPhone and iPad:** add Exhale to the Home Screen first (Share → Add to Home Screen), open it from there and turn notifications on inside it. iOS 16.4 or later. iOS does not show notification buttons: a tap opens the charge.
- Nothing to set up on the server: the keys are made the first time someone turns push on, and kept in the database. Do not lose them: a new key would turn push off on every device. They are in your backups like everything else.

The notification travels through the push service of the browser's vendor (Google, Mozilla, Apple…), encrypted so that only that browser can read it. When a push service says a device no longer exists (the person cleared the browser's data or removed the app), Exhale removes the device.

## Email

An owner enters the SMTP server: host, port, user, password, sender address and how the connection is secured (STARTTLS, usually port 587; TLS, port 465; or none, for a relay on the same machine). The same settings can come from the environment, and then the web shows them as read-only:

| Variable | Meaning |
|---|---|
| `EXHALE_SMTP_HOST` | Mail server |
| `EXHALE_SMTP_PORT` | Port (default 587 with STARTTLS, 465 with TLS, 25 with none) |
| `EXHALE_SMTP_USERNAME`, `EXHALE_SMTP_PASSWORD` | Credentials |
| `EXHALE_SMTP_FROM` | Sender address |
| `EXHALE_SMTP_TLS` | `starttls` (default), `tls` or `none` |

Then each person switches email on for their address. The email is plain text plus a small HTML version with one button; it follows the mail client's light or dark theme.

## ntfy

[ntfy](https://ntfy.sh) pushes notifications to a phone. An owner can set the server (`https://ntfy.sh` by default, or your own) and an access token if the server needs one to publish (`EXHALE_NTFY_URL`, `EXHALE_NTFY_TOKEN`). Each person enters a **topic**: subscribe to the same topic in the ntfy app.

On ntfy.sh a topic is public to anyone who knows its name. Pick a long random one.

## Webhook

A webhook sends each reminder as one HTTP `POST` with a JSON body to an address you choose. A response outside the 200 range counts as a failure and is retried later.

The default body:

```json
{
  "event": "reminder",
  "title": "Netflix renews in 3 days",
  "message": "€13.99 · Sun, 8 Nov 2026\nVisa ••4421",
  "subscription": "Netflix",
  "amount": "13.99",
  "currency": "EUR",
  "dueDate": "2026-11-08",
  "daysUntil": 3,
  "paymentMethod": "Visa ••4421",
  "category": "Streaming",
  "link": "https://exhale.example.com/subscriptions/sub_…"
}
```

`event` is `reminder` (before a charge, a trial ending or an expiry), `decision` (the last day to cancel is near), `confirm` (the date passed: was it charged?), `failed` (a charge marked as failed), `duplicate` (the same service paid more than once), `card_expiring` (a card that pays subscriptions expires soon; `paymentMethod`, `dueDate` and `daysUntil` say which and when) or `test` (sent from Settings). See [reminders that end in an answer](reminders.md).

### Body templates

Services expect their own JSON. Give the destination a **body template** and Exhale fills in `{{name}}` variables, escaped for JSON:

| Variable | Value |
|---|---|
| `{{event}}` | `reminder`, `decision`, `confirm`, `failed`, `duplicate`, `card_expiring` or `test` |
| `{{title}}` | The headline without its emoji |
| `{{emoji}}` | The emoji of the headline |
| `{{message}}` | The details under the headline |
| `{{text}}` | Everything as plain text, ending with the link |
| `{{subscription}}`, `{{amount}}`, `{{currency}}` | What is due |
| `{{dueDate}}`, `{{daysUntil}}` | When |
| `{{paymentMethod}}`, `{{category}}` | Where it is charged and its category |
| `{{link}}` | The page in Exhale |

Discord:

```json
{ "content": "{{emoji}} **{{title}}**\n{{message}}\n{{link}}" }
```

Slack incoming webhook:

```json
{ "text": "{{emoji}} *{{title}}*\n{{message}}\n<{{link}}|Open in Exhale>" }
```

Home Assistant (a webhook trigger; use the fields in your automation):

```json
{ "title": "{{title}}", "message": "{{message}}", "subscription": "{{subscription}}", "days": {{daysUntil}} }
```

## Addresses Exhale will not call

Anyone who shares an Exhale can add a destination, so Exhale refuses addresses that would let them reach the server's own machine or network: only `http` and `https`; never the machine itself, link-local addresses or cloud metadata services. **Private network addresses** (a ntfy server or Home Assistant on your LAN) are refused too unless an owner allows them in Settings, or with `EXHALE_ALLOW_PRIVATE_DESTINATIONS=true`. The check is made on the address actually dialed, so a redirect or a changing DNS answer cannot get around it.

## Why was a reminder not delivered?

**Activity → Reminders** lists every reminder with what happened to it: sent, scheduled, retrying with the last error, or not sent because the person had no chat or channel linked (or their only device was gone).
