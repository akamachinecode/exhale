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

## Options rejected

- **Authboss:** maintained and the closest thing to Better Auth in Go, but it covers only the easy parts, imposes its own flows and email-centric modules, sits outside the OpenAPI contract, and has no passkeys.
- **Ory Kratos:** a separate server, which breaks the single binary.
- **go-pkgz/auth:** JWT based.
- Newer 2026 libraries: too young and unaudited.

## Revisit when

Many users expose the app to the internet without a proxy (bring TOTP into v1), or we add native TOTP and recovery codes (re-evaluate Authboss).
