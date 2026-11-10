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

## Implementation notes: OIDC (2026-11-10)

Sign-in with an OpenID Connect provider shipped. How to configure it: [docs/install/sso.md](../install/sso.md).

- **Configuration is environment and flags only** (`EXHALE_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET` or `_CLIENT_SECRET_FILE`, `_NAME`, `_SCOPES`, `EXHALE_DISABLE_PASSWORD_LOGIN`, `EXHALE_PUBLIC_URL`). One provider per instance. Discovery is lazy and retried, so Exhale starts while the provider is down.
- **Flow:** authorization code with PKCE (S256), `state` and `nonce`. `GET /api/v1/auth/oidc/start` redirects to the provider; `GET /api/v1/auth/oidc/callback` checks the state, exchanges the code, verifies the ID token (signature, issuer, audience, expiry, nonce) and starts the same scs session as a password login. Like the other auth routes they are outside the op catalog; they are plain redirects, so they are not in the OpenAPI document either.
- **An attempt lives in the server's memory for 10 minutes and works once.** The browser only holds a random id in an `HttpOnly`, `SameSite=Lax` cookie scoped to `/api/v1/auth/oidc/`; state, nonce and the PKCE verifier never leave the server. Rejected: keeping them in the scs session (every anonymous click would write a 30-day session row) and a signed cookie (needs a persisted key for no gain in a single process). A restart only asks people to click again.
- **The redirect URI comes from `--public-url`**, never from the request. Without it only the server's own machine works (loopback address, loopback `Host`, no `X-Forwarded-For`). The page to land on afterwards (`next`) must be a path on this site.
- **Account linking, the security core.** A member is linked to one identity, the pair `(issuer, sub)`, in two columns of `members` (migration 00020). In order:
  1. An identity already linked is that member, whatever its email is today. Exhale never copies later email changes from the provider.
  2. While nobody can sign in (no member has a password or an identity), the identity becomes the owner: of a new household, or of the one made with `exhale init`. From another machine it needs the setup code, checked when the flow starts and again when it comes back.
  3. An identity whose email is a member's email is linked to that member only if the ID token says `email_verified: true`, and only if the member is not already linked to another identity of the same issuer. Not verified: refused.
  4. Anyone else needs a valid invite carried through the flow. Otherwise: "Ask the owner for an invite". **Authenticating at the provider never creates a member.**
- **Moving providers:** a link to an issuer that is no longer the configured one is dead, so such a member is matched again by rule 3. A link to the configured issuer is never replaced.
- **Emails of accounts made through the provider:** with an invite or the setup code the email claim is stored even if unverified, as long as no other member has it. It is safe because an email only ever matches a member that has no identity of the current issuer, and these members have one.
- **Members without password:** `SessionActor` now accepts a member with a password *or* an identity. `session_version` works the same: `exhale user reset-password` signs an OIDC member out everywhere and gives it a password.
- **`--disable-password-login`** refuses password sign-in, sign-up and join, and hides the forms. It is only honoured when a provider is configured. Break-glass: remove the flag, restart, `exhale user reset-password`.
- **Brute force:** the start route has its own per-IP throttle (same numbers as sign-in). The callback does nothing without an attempt started from the same browser.
- **Logs** carry the reason of every refusal and the provider's error codes; never the client secret, tokens, codes or ID token contents.
- **Not verified against a real provider yet.** Tests run against a fake provider (discovery, JWKS, RS256, PKCE, userinfo).

Still pending from this ADR: member management (remove a member, change roles, unlink an identity), linking an identity to an existing account from Settings (needed where the provider does not verify emails, e.g. Entra), setting a password on an account made through the provider from the web, TOTP and passkeys (later), trusted proxy header auth (v1.x).

## Implementation notes: two-factor (2026-11-10)

TOTP with backup codes shipped for password sign-in, with the standard library instead of `pquerna/otp`: [ADR 0025](0025-two-factor-sign-in.md). Single sign-on and tokens are not affected. Recovery: an owner in Settings → Sharing, or `exhale user reset-2fa --email …` on the server.

## Options rejected

- **Authboss:** maintained and the closest thing to Better Auth in Go, but it covers only the easy parts, imposes its own flows and email-centric modules, sits outside the OpenAPI contract, and has no passkeys.
- **Ory Kratos:** a separate server, which breaks the single binary.
- **go-pkgz/auth:** JWT based.
- Newer 2026 libraries: too young and unaudited.

## Revisit when

Many users expose the app to the internet without a proxy (bring TOTP into v1), or we add native TOTP and recovery codes (re-evaluate Authboss).
