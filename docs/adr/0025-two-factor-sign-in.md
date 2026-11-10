# 0025. Two-factor sign-in: TOTP and backup codes, standard library, QR in the browser

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

ADR 0006 planned TOTP for later. Exhale is designed as if it could be reachable from the internet, and a password alone is one leaked password away from the household's financial data. People who sign in with a provider (OIDC) get a second factor from it; people who sign in with a password have none.

It has to: enrol with any authenticator app (QR code or a key to type), confirm with a first code, give backup codes for a lost phone, ask for the code after a correct password, refuse a code used once, and let someone who is locked out get back in without email.

## Criteria

- No new dependency unless it saves real code or risk (binary and web bundle size, supply chain).
- The secret never leaves the server after enrolment.
- Nothing changes for single sign-on, API tokens and OAuth/MCP tokens.

## Options

| Question | Option | Verdict |
|---|---|---|
| TOTP | `pquerna/otp` (named in ADR 0006) | Rejected. It is maintained, but RFC 6238 with the defaults every app uses (HMAC-SHA1, 6 digits, 30 s) is about 40 lines on `crypto/hmac`, and it pulls in `boombuler/barcode` for a QR we do not draw in Go. |
| | **Standard library** | **Chosen.** Tested against the RFC 6238 and RFC 4226 vectors. |
| QR code | In Go (`rsc.io/qr` or similar), served as SVG/PNG | Rejected: a new module for something the web can already do. |
| | **In the browser with `uqr`** | **Chosen.** It is already in the bundle for the Telegram link, so it costs nothing. The `otpauth://` URI is returned once, at enrolment. |
| Pending login | scs session with a "half signed in" flag | Rejected: every password check would write a 30-day session row, and a bug in one check would grant a session. |
| | **A token for a login waiting for a code, in the server's memory** | **Chosen.** 5 minutes, 3 wrong codes, then the password again. A restart only asks for the password again. |
| Secret at rest | Encrypted with a key stored next to the database | Rejected (ADR 0009): it protects nothing. |
| | **Like the other instance secrets** (Slack and Telegram tokens, assistant key) | **Chosen.** In the database, never returned by any endpoint after enrolment. When `EXHALE_SECRET_KEY` (ADR 0009) is built, it covers this secret too. Stealing the database is not enough to sign in: the password is still argon2id. |

## Decision

- **Tables** `member_totp` (secret, `enabled_at`, `last_step`) and `member_backup_codes` (SHA-256 of each code, `used_at`). An enrolment is pending until the first code confirms it, and expires after an hour.
- **Codes:** HMAC-SHA1, 6 digits, 30-second steps, ±1 step accepted. Each accepted step is stored; a code of that step or an older one is refused, so a code works once, even from two tabs at the same time (an atomic `UPDATE … WHERE last_step < ?`).
- **Backup codes:** 10, of 10 base32 characters (50 bits), shown once as `xxxxx-xxxxx`, stored hashed, each used once. Dashes, spaces and capitals are ignored when typed. New codes replace all old ones.
- **Sign-in:** `POST /auth/login` with a correct password for an account with two-factor on returns `twoFactor.token` and no session. `POST /auth/login/two-factor` with the token and a code (or a backup code) starts the session.
- **Throttling,** on top of the password's: codes per IP like sign-in (10 at once, then one every 6 s) and per member stricter (5 at once, then one every 30 s), whatever the pending login. No lockout, as in ADR 0006.
- **Turning it on** asks for the password, then a code, and signs out every other session of the account (a stolen session does not survive it). **New backup codes** ask for the password. **Turning it off** asks for the password and a code.
- **Recovery:** an owner turns it off for a member from Settings → Sharing; on the server, `exhale user reset-2fa --email …`. Every change (on, off, new codes, reset) is in the activity log and cannot be undone from there.
- **Not affected:** single sign-on (the provider handles its own second factor), API tokens, OAuth/MCP tokens. Accounts without a password cannot turn it on.
- Like the other account endpoints, none of this is in the op catalog: AI agents cannot turn it on or off.

## Consequences — cost we accept

- Own TOTP code to maintain. It is small and covered by the RFC vectors.
- A 6-digit code is guessable with enough time: with the throttle, about 0.9 % per day of continuous guessing for someone who already has the password.
- Pending logins live in one process's memory, which fits the single binary.

## Revisit when

- Passkeys (`go-webauthn`, ADR 0006) arrive: they may replace TOTP for most people.
- `EXHALE_SECRET_KEY` is built: encrypt the TOTP secret with it.
- Someone asks for "remember this device".
