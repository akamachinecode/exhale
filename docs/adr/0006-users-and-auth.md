# 0006. Users and authentication: household model, composed libraries, OIDC in v1

- Status: Accepted
- Date: 2026-11-06

## Context — what it has to do

- Protect financial data.
- Know who did what, from the web, Telegram or Slack.
- Route reminders to the person who pays.
- Give machines access without passwords.
- Make first run safe even if exposed to the internet.
- Recover access without email.

We design **as if the instance could be exposed to the internet**.

## Decision — user model

**Household:** one instance = one household, with roles **owner** and **member**. Each member links their own Telegram or Slack. Subscriptions have a visibility: **shared** (default) or **private** (only the creator sees it). Multi-tenant is consciously rejected: run another instance instead.

## Decision — mechanisms

| Piece | Choice |
|---|---|
| Password hashing | `alexedwards/argon2id` (on `x/crypto/argon2`) |
| Sessions | `alexedwards/scs` v2 with our own store on sqlc. Server-side and revocable. **No JWT** |
| CSRF | stdlib `http.CrossOriginProtection` (Go 1.25+) + `SameSite=Lax` cookies |
| Brute force | `x/time/rate` per IP and per user, with increasing delays (no account lockout) |
| API tokens | Own: 32 random bytes, `exhale_pat_` prefix (secret-scanning friendly), stored as SHA-256, read or write scope |
| Chat linking | Own: one-time code, 10 min expiry, hashed. Telegram deep link `t.me/<bot>?start=<code>`; Slack `/link <code>`. Unlinked users are ignored |
| Authorization | Own role checks in the use cases ⑦ |
| First run | One-time setup code printed in the logs, required by the setup wizard |
| Recovery | `exhale user reset-password` CLI |
| **OIDC (v1)** | `coreos/go-oidc` + `x/oauth2`: Authelia, Authentik, Pocket ID, Kanidm users get passkeys, 2FA and SSO from their IdP on day one |
| Later | Trusted proxy header auth (v1.x), TOTP with `pquerna/otp` (v1.x), passkeys with `go-webauthn` (v2) |

## Implementation notes (2026-11-09)

What shipped first, and the details the table above leaves open:

- **Sign-in is by email and password.** Emails are stored lowercase and unique. Nothing is ever sent to them; recovery is `exhale user reset-password --email …` on the server.
- **First run:** the web shows a two-step wizard (your account, then the household). The setup code is printed in the log on every start while nobody can sign in, and `exhale setup-code` prints a new one. **It is only asked when the wizard is opened from another machine**: from the server's own machine (loopback, no `X-Forwarded-For`) it is skipped. A household made with `exhale init` keeps its data and the wizard only creates the owner's login.
- **Sessions:** scs with our own SQLite store (the upstream `sqlite3store` needs cgo). 30-day lifetime, 14-day idle timeout, `HttpOnly`, `SameSite=Lax`, `Secure` with `--secure-cookies`. Each session stores the member's `session_version`; a password change or reset bumps it, which signs out every other session. The session that changed it is renewed.
- **CSRF:** `http.CrossOriginProtection` wraps the whole server. Bearer-token clients (agents, scripts) send no browser headers and are not affected.
- **Brute force:** 10 attempts at once, then one every 6 s, per IP and per email, on sign-in, setup, join and password change. Unknown emails take as long as wrong passwords (argon2id against a dummy hash). No lockout.
- **Passwords:** argon2id, 64 MiB, 1 pass, 2 lanes. At least 10 characters, no composition rules (NIST SP 800-63B).
- **Invites:** owners create a one-time link (`/join/<code>`, 7 days, hashed) for a member or a co-owner.
- **Tokens and chat links** are managed from Settings; the Telegram link is a `t.me/<bot>?start=<code>` deep link (the bot name comes from `getMe`).
- **`--trust-local` is now off by default.** It remains for development and scripts.
- The auth endpoints live under `/api/v1/auth` and are deliberately **not** in the op catalog: AI agents cannot create tokens, invite people or change passwords.
- Fixed on the way: an empty request on a fresh instance used to create a household with no name or currency. Migration 00003 removes such empty households.

Still pending from this ADR: OIDC (v1), member management (remove a member, change roles), TOTP and passkeys (later).

## Options rejected

- **Authboss:** maintained and the closest thing to Better Auth in Go, but it covers only the easy parts, imposes its own flows and email-centric modules, sits outside the OpenAPI contract, and has no passkeys.
- **Ory Kratos:** a separate server, which breaks the single binary.
- **go-pkgz/auth:** JWT based.
- Newer 2026 libraries: too young and unaudited.

## Revisit when

Many users expose the app to the internet without a proxy (bring TOTP into v1), or we add native TOTP and recovery codes (re-evaluate Authboss).
