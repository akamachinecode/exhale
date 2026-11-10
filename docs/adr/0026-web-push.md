# 0026. Web Push from the installed app, on the standard library

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

The web is installable (PWA). The [feature scan](../research/2026-11-10-feature-scan.md) and the vision list "web push with action buttons" as a reminder channel: a reminder arrives on the phone or computer where Exhale is installed, with no Telegram, no Slack and no third-party app, and a button in the notification marks the charge paid without opening the app.

Physically that is:

- **Per instance**, one VAPID key pair (RFC 8292), made once and kept: the browsers subscribe with its public key, and a new key would cut every one of them off.
- **Per device**, the browser's `PushSubscription`: an endpoint at the browser vendor's push service (Google, Mozilla, Apple…) and two keys.
- **Per reminder**, one `POST` to that endpoint: the payload encrypted for that browser (RFC 8291, `aes128gcm`), with a short JWT signed with the VAPID key. 201 is delivered; 404 and 410 mean the browser dropped the subscription; 429 and 5xx are worth retrying.
- **A service worker** that shows the notification and, for a button, calls Exhale without a tab open and without a session.

## Criteria

1. **Correctness of the crypto**, checked against the RFC's own example, not only against itself.
2. **Weight** (principle 3, light): binary size and modules added, measured like ADR 0018.
3. **Maintenance**: who keeps it working as push services change.
4. **Fit with the outbox**: retries, per-destination delivery records and error messages the same as email, ntfy and webhooks (ADR 0007).

## Options

Measured on this repository at `aae6b92`, stripped static build (`CGO_ENABLED=0 go build -ldflags="-s -w"`), with each option called from `main` so the linker keeps it:

| | Size | Modules linked into the binary |
|---|---|---|
| Exhale without push | 24,764,576 B | 35 |
| + **`SherClockHolmes/webpush-go` v1.4.0** | 24,862,880 B (**+96 KB**) | 37: + `webpush-go`, `golang-jwt/jwt/v5` |
| + **own code on the standard library** (encryption, VAPID, action tokens) | 24,805,536 B (**+40 KB**) | 35 |
| The whole feature as shipped (channel, devices, endpoints) | 24,883,360 B (+116 KB) | 35 |

**A) `webpush-go`.** About 600 lines, MIT, the usual Go choice. Last release v1.4.0 on 2025-01-02. It pulls `golang-jwt` for one ES256 token and uses `x/crypto/hkdf`, which the standard library now has. It makes the salt and the key pair inside `SendNotification`, so the RFC 8291 example cannot be run against it; it pads every message to 4096 octets; and it returns the raw response, so the 404/410 and retry handling is ours either way.

**B) Own code on the standard library.** Go has had `crypto/ecdh` since 1.20 and `crypto/hkdf` since 1.24, and `crypto/ecdsa` reads and writes raw P-256 keys since 1.25. What the libraries existed to fill is now in the standard library. The encryption is about 110 lines, VAPID about 110, with comments.

## Decision

**B, own code on the standard library** (`internal/channels/webpush`).

- The encryption is tested against RFC 8291's example byte for byte: the intermediate key and nonce of appendix A, and the 145-octet body of section 5. A fake browser in `pushtest` decrypts what the channel sends, and a fake push service checks the VAPID signature, audience and expiry the way real ones do.
- It was also checked by hand against a real push service: Chromium subscribed through Google's, received a reminder, and its button marked the charge paid.
- Size is not what decides it (40 KB against 96 KB, both small); the testability against the RFC and not adding an unmaintained-looking dependency for about 200 lines of code are.

How it fits:

- **A device is a destination.** One `channel_links` row per browser (channel `webpush`, external id = the endpoint), its keys in `push_subscriptions` (migration 00090), deleted with the link. The outbox, retries, "already delivered to this destination" records, the reminder log and **Send test** work as for email, ntfy and webhooks. The endpoint is never returned by the API: Settings shows a device name.
- **404 and 410 remove the device** and the delivery returns `actions.ErrGone`, which is skipped rather than retried; a reminder whose only device is gone is logged as "nowhere to send it". 429, 5xx and other errors are retried by the outbox.
- **Endpoints are typed by browsers, so by anyone signed in**: only `https`, and the request goes through `netguard` like webhooks (no loopback, link-local or metadata addresses).
- **Keys.** The VAPID private key and a 32-byte HMAC key for action tokens are made on first use and kept in `settings`. The VAPID contact (`sub`) is the instance's `--public-url` when it is https, else the project's address; no email address is sent to push services.
- **Buttons.** A notification offers the message's own buttons whose verb is in `actions.PushVerbs` (today `paid`; browsers show two at most), with the same label as in chat. Each carries a token signed with HMAC-SHA256 that names the device, the member, the charge and the verb, and expires after 72 hours. The service worker posts it to `POST /api/v1/push/action`, which needs no session; the server checks the token and that the device is still the member's (removing a device revokes its buttons), then runs `Engine.Handle` with the same button data as a chat press: same use case, same audit entry, same toast. The answer replaces the notification ("✅ Netflix paid · Marked as paid ✅"). If the token is refused or the network fails, the charge opens in the app instead.
- **The service worker caches nothing.** It has no `fetch` handler, so the app, the API and Vite's dev server behave as without it. `sw.js` is served with `Cache-Control: no-cache`.

## Consequences — cost we accept

- We own the crypto. It is small and pinned by the RFC example, but a mistake would be ours.
- Notifications are not re-edited when the charge changes elsewhere (Web Push cannot edit a delivered notification); a newer notification about the same charge replaces the older one (same `tag`).
- iPhone and iPad only offer push to the app added to the Home Screen (iOS 16.4+), and they show no action buttons: a tap opens the charge.
- Push needs https outside `localhost`; an instance reached over plain http on the LAN cannot use it.
- "Remind me tomorrow" is not offered yet: the snooze use case does not exist on this branch. Adding its verb to `actions.PushVerbs` is the only change needed.

## Revisit when

- A push service needs something new (a header, a claim) that a maintained library handles and we do not.
- We want encrypted payloads larger than one record, or the declarative Web Push format Safari is adding.
