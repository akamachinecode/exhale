# Single sign-on (OpenID Connect)

Exhale can sign people in with any OpenID Connect provider: Authentik, Keycloak, Authelia, Pocket ID, Kanidm, Google, Microsoft Entra… Passwords keep working next to it unless you turn them off.

The provider answers one question: *who is this?* Whether that person has an account in your Exhale is decided by Exhale ([the rules](#who-gets-in)). Signing in at the provider never creates an account by itself.

## Configure

Everything is set with environment variables (or the matching `exhale serve` flags). Nothing is stored in the database or editable from the web.

| Variable | Flag | |
|---|---|---|
| `EXHALE_PUBLIC_URL` | `--public-url` | Address people open Exhale at, e.g. `https://exhale.example.com`. **Required** unless you only use Exhale from the server's own machine. |
| `EXHALE_OIDC_ISSUER` | `--oidc-issuer` | Issuer URL of the provider: the one that serves `/.well-known/openid-configuration`. |
| `EXHALE_OIDC_CLIENT_ID` | `--oidc-client-id` | Client id registered at the provider. |
| `EXHALE_OIDC_CLIENT_SECRET` | `--oidc-client-secret` | Client secret. Leave it empty for a public client (PKCE only). |
| `EXHALE_OIDC_CLIENT_SECRET_FILE` | `--oidc-client-secret-file` | File holding the secret, for Docker secrets. Use it instead of the variable above. |
| `EXHALE_OIDC_NAME` | `--oidc-name` | What the button says: "Continue with *name*". Default: `Single sign-on`. |
| `EXHALE_OIDC_SCOPES` | `--oidc-scopes` | Scopes to ask for, comma separated. Default: `openid,email,profile`. |
| `EXHALE_DISABLE_PASSWORD_LOGIN` | `--disable-password-login` | Only allow single sign-on. Ignored while no provider is configured. |

Single sign-on is on when both the issuer and the client id are set.

```yaml
# compose.yaml
services:
  exhale:
    environment:
      EXHALE_PUBLIC_URL: "https://exhale.example.com"
      EXHALE_SECURE_COOKIES: "true"
      EXHALE_OIDC_ISSUER: "https://auth.example.com/application/o/exhale/"
      EXHALE_OIDC_CLIENT_ID: "exhale"
      EXHALE_OIDC_CLIENT_SECRET_FILE: "/run/secrets/exhale_oidc"
      EXHALE_OIDC_NAME: "Authentik"
```

### The redirect URI to register

```
<EXHALE_PUBLIC_URL>/api/v1/auth/oidc/callback
```

For example `https://exhale.example.com/api/v1/auth/oidc/callback`. Register it exactly; providers compare it character by character.

Exhale builds this address from `EXHALE_PUBLIC_URL` and never from the request, so a forged `Host` header cannot redirect a sign-in elsewhere. Without `EXHALE_PUBLIC_URL`, single sign-on only works when you open Exhale on the server's own machine (`http://localhost:8484/api/v1/auth/oidc/callback`), and the sign-in page says so to everyone else.

Open Exhale at the public URL when you sign in: the browser must come back to the same address it left from.

### What Exhale asks of the provider

- Authorization code flow with PKCE (S256). A confidential client (with a secret) is the usual choice; a public client works too.
- ID tokens signed with an asymmetric key (RS256, ES256…). Tokens signed with the client secret (HS256) are not accepted.
- The `email` and `email_verified` claims, in the ID token or from the userinfo endpoint, and `name` if you want accounts named after people.
- Use `https` for the issuer. Exhale starts with an `http` issuer but warns: anyone between Exhale and the provider could then forge sign-ins.

If the provider is down when Exhale starts, Exhale starts anyway and tries again on the next sign-in.

## Who gets in

1. **Someone who signed in this way before** is the same account again. The link is the provider's own id for the person (`issuer` + `sub`), so it survives a change of email or name at the provider.
2. **On a fresh instance** (nobody can sign in yet), the first person through the provider becomes the owner. As with the password wizard, opening it from another machine asks for the setup code printed in the server log (`exhale setup-code` prints a new one).
3. **Someone whose email already has an account** (made with a password, or with `exhale init`) is linked to that account, **only if the provider says the email is verified** (`email_verified: true`). Otherwise the sign-in is refused: an unverified email proves nothing.
4. **Anyone else needs an invite.** Owners create one in Settings → Sharing; on the invite page the person clicks "Continue with …". Without an invite the answer is "Ask the owner for an invite".

An account made through the provider has no password. Its email is shown in Settings; Exhale does not update it when it changes at the provider.

If you later move to **another provider** (a different issuer), people are matched again by verified email, rule 3. Changing only the client id or secret changes nothing.

## Examples

### Authentik

1. *Applications → Applications → Create with provider*, type **OAuth2/OpenID**.
2. Client type **Confidential**. Redirect URI (strict): `https://exhale.example.com/api/v1/auth/oidc/callback`.
3. Choose a **signing key** (the self-signed certificate is fine). Without one Authentik signs with the client secret, which Exhale refuses.
4. Scopes: `openid`, `email`, `profile`.

```sh
EXHALE_OIDC_ISSUER=https://auth.example.com/application/o/<application-slug>/
EXHALE_OIDC_CLIENT_ID=<client id>
EXHALE_OIDC_CLIENT_SECRET=<client secret>
EXHALE_OIDC_NAME=Authentik
```

Depending on the version, Authentik's default `email` scope mapping sends `email_verified: false`. Invited people are not affected. To link accounts that already exist in Exhale (rule 3), change that mapping in Authentik so it reports what you trust, or invite those people again.

### Keycloak

1. *Clients → Create client*: type **OpenID Connect**, client id `exhale`.
2. **Client authentication** on, **Standard flow** on, everything else off.
3. Valid redirect URIs: `https://exhale.example.com/api/v1/auth/oidc/callback`.
4. Copy the secret from the *Credentials* tab. Optionally, under *Advanced*, set the PKCE code challenge method to `S256` to make it mandatory.

```sh
EXHALE_OIDC_ISSUER=https://keycloak.example.com/realms/<realm>
EXHALE_OIDC_CLIENT_ID=exhale
EXHALE_OIDC_CLIENT_SECRET=<client secret>
EXHALE_OIDC_NAME=Keycloak
```

Keycloak sends `email_verified` from the user's *Email verified* switch.

### Google

1. Google Cloud console → *APIs & Services → Credentials → Create credentials → OAuth client ID*, type **Web application**.
2. Authorized redirect URIs: `https://exhale.example.com/api/v1/auth/oidc/callback` (Google wants `https`, except for `localhost`).

```sh
EXHALE_OIDC_ISSUER=https://accounts.google.com
EXHALE_OIDC_CLIENT_ID=<…>.apps.googleusercontent.com
EXHALE_OIDC_CLIENT_SECRET=<client secret>
EXHALE_OIDC_NAME=Google
```

Everybody has a Google account, and that is fine: only people with an account in your Exhale or an invite get in.

### Others

- **Microsoft Entra:** issuer `https://login.microsoftonline.com/<tenant-id>/v2.0`. Entra does not send `email_verified`, so accounts are not matched by email: set Exhale up through Entra from the start, or invite people.
- **Authelia, Pocket ID, Kanidm:** register a confidential client with the redirect URI above and `client_secret_basic` or `client_secret_post`; the issuer is the provider's base URL shown in its OpenID Connect settings.

## Only single sign-on

Set `EXHALE_DISABLE_PASSWORD_LOGIN=true` once you have signed in through the provider. The password forms disappear from sign-in, sign-up and invites, and the server refuses passwords.

## Break-glass: the provider is down or misconfigured

Passwords are the way back in, from the server's shell:

1. Remove `EXHALE_DISABLE_PASSWORD_LOGIN` (if you set it) and restart Exhale.
2. `exhale user reset-password --email you@example.com` sets a password for that account, also for one made through the provider, and signs it out everywhere.
3. Sign in with the email and the new password.

The reset finds the account by the email shown in its Settings. An account whose provider sent no email has none; reset another owner's instead.

## Troubleshooting

The sign-in page shows a sentence; the server log has the reason (never the tokens or the secret).

| The page says | Look at |
|---|---|
| Single sign-on is not set up for this address | `EXHALE_PUBLIC_URL` is missing. |
| The sign-in provider is not answering | The issuer URL (open `<issuer>/.well-known/openid-configuration`), and that the server can reach it. |
| The sign-in did not complete | The log: a wrong client secret, a redirect URI that is not registered, HS256 tokens, a clock that is off. |
| That sign-in took too long | More than 10 minutes at the provider, a second tab that started over, or Exhale opened at an address other than the public URL. |
| Your provider has not verified your email | Rule 3 above. |

Developing with the Vite dev server: set `EXHALE_PUBLIC_URL=http://localhost:5173` and register that callback, so the browser returns to the address it left from.
