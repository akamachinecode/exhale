# 0024. Remote MCP: Exhale is its own minimal OAuth 2.1 authorization server

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

Milestone 3 of ADR 0018: Claude.ai, ChatGPT and Grok call Exhale's `/mcp` from their own clouds. They do not take a pasted token; they follow the [MCP authorization spec](https://modelcontextprotocol.io/specification/draft/basic/authorization): discover the server from a 401, register, send the person to a consent page, get tokens. Exhale must be the authorization server, because there is no other one in a self-hosted single binary.

What it physically has to do:

- Serve **protected resource metadata** (RFC 9728) and a `WWW-Authenticate` header with `resource_metadata` on a 401 from `/mcp`.
- Serve **authorization server metadata** (RFC 8414), advertising PKCE `S256`.
- Let applications introduce themselves: **Client ID Metadata Documents** (the spec's SHOULD; Claude.ai and Grok use them) and **dynamic registration** (RFC 7591; deprecated in the spec, still used by ChatGPT and many clients).
- The **authorization code grant with PKCE**, a consent page for a signed-in member, **resource indicators** (RFC 8707) so tokens only work at `/mcp`, short access tokens, **rotating refresh tokens**, **revocation** (RFC 7009), and the `iss` response parameter (RFC 9207).
- Tokens act as the member who approved, like API tokens (ADR 0006), with read or write scope.

## Criteria

1. **Spec coverage of what MCP clients use** (above), with nothing extra to secure.
2. **Weight** (principle 3, light): binary size and modules added.
3. **Fit**: our store (sqlc), our sessions (scs), our actor model, opaque hashed tokens like `exhale_pat_`.
4. **Auditability**: a reviewer can read the whole thing.

## Options

Measured like ADR 0018's notes for the Anthropic SDK: the `exhale` binary at the parent commit, stripped static build (`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`), with each library linked through its main constructor (`op.NewProvider`, `compose.ComposeAllEnabled` with its handlers). "Linked" counts modules in `go version -m`; "graph" counts `go list -m all`.

| | Size | Δ size | Linked modules added | Module graph added |
|---|---|---|---|---|
| Exhale before | 24.69 MB | | (35) | (191) |
| + `zitadel/oidc/v3` v3.51.17 (`pkg/op`) | 26.46 MB | +1.77 MB (+7%) | +17 (chi, rs/cors, gorilla/securecookie, OpenTelemetry, logr…) | +16 |
| + `ory/fosite` v0.49.0 (`compose`) | 32.88 MB | +8.19 MB (+33%) | +47 (gRPC, protobuf, OpenTelemetry exporters, logrus, pop, ory/x…) | +296 |
| **Own (what shipped)** | 24.90 MB | **+0.20 MB (+0.8%)** | **0** | **0** |

What each library would and would not do for us:

- **zitadel/oidc (OP)** is an OpenID Provider. It needs a signing key and a JWKS (persisted keys to manage), a storage interface of about 16 methods, and it refuses an authorization request with no scope, which MCP clients may send. It has no dynamic registration endpoint, no Client ID Metadata Documents, no protected resource metadata and no resource indicators on the authorization request. We would write all of that anyway, plus adapters to its storage, around the part it does well (parsing requests, the token endpoint).
- **ory/fosite** covers more grants than we need, at a third of the binary again. It is also an SDK to build a server, not a server: storage, consent, registration and metadata are still ours.
- **Own minimal:** only the authorization code grant for one resource, public and confidential clients, opaque tokens. About 1 800 lines with comments in `internal/oauth` and `internal/httpapi/oauth.go`, plus the SQL, on the standard library and `x/time/rate`.

## Decision

**Own minimal implementation** in `internal/oauth` (rules, store) and `internal/httpapi/oauth.go` (endpoints). No JWT, no signing keys: tokens are random opaque strings stored as SHA-256 hashes, checked in one query.

| Endpoint | |
|---|---|
| `GET /.well-known/oauth-protected-resource[/mcp]` | resource `<public URL>/mcp`, authorization server, scopes `read write` |
| `GET /.well-known/oauth-authorization-server` | issuer `<public URL>`, `S256` only, `client_id_metadata_document_supported`, `authorization_response_iss_parameter_supported` |
| `POST /oauth/register` | RFC 7591 |
| `GET /oauth/authorize` | checks the request, then redirects to the web page `/authorize` |
| `POST /oauth/token` | `authorization_code`, `refresh_token` |
| `POST /oauth/revoke` | RFC 7009 |
| `/api/v1/auth/oauth/requests/{id}`, `/api/v1/auth/apps` | session only: the consent page and the connected apps |

The rules that matter:

- **Issuer and audience come from `--public-url`** (`EXHALE_PUBLIC_URL`), never from the `Host` header. Without it only the server's own machine works, like single sign-on (ADR 0006). The one resource is `<public URL>/mcp` (canonical: lowercase scheme and host, no default port, no trailing slash). A `resource` naming anything else is refused (`invalid_target`); a request without one gets ours.
- **Clients.** A `client_id` that is an https URL is a Client ID Metadata Document, fetched through netguard (no private addresses, no redirects, 32 KB, 5 s), checked (`client_id` equals its URL, a name, `none` auth, valid redirect URIs) and cached by `Cache-Control` within 5 minutes and 24 hours. Anything else is a registered id. Registration accepts public clients and, when asked, `client_secret_basic`/`client_secret_post` (a secret shown once, stored hashed); PKCE is required for both.
- **Redirect URIs:** https, or http on a loopback address; no fragment, no credentials, no other scheme. Compared exactly, except that the port of a loopback address may change (OAuth 2.1 section 8.4.2: a native app listens on a free port).
- **No automatic redirect before consent.** Any problem with an authorization request is shown on Exhale's own page, as a code the web translates. Sending errors to a redirect URI that anyone can register would make Exhale an open redirector (OAuth 2.1 section 7.12.2). Only the person's answer redirects: the code, or `access_denied`, always with `state` and `iss`.
- **Consent** is a page of the web app for a signed-in member. It shows the name, whether it is verified (a metadata document's host) or self-registered, the redirect host, and a warning for loopback redirects (MCP security considerations). The member picks read or read and change, never more than the application asked for, and never `trusted`: confirmations of destructive changes are the point. The endpoint needs a web session: a bearer token, even a trusted one, cannot approve. The pending request lives in memory for 10 minutes and is answered once.
- **Codes:** 5 minutes, single use, bound to client, redirect URI, PKCE challenge, scope, resource and the member's `session_version`. A code used twice ends the connection the first use created.
- **Tokens:** access 1 hour (`exhale_oat_`), refresh 90 days (`exhale_ort_`). **Every refresh rotates.** Presenting a used refresh token ends the whole connection, including newer tokens. There is no grace period: a lost response costs one reconnection, a copied token costs nothing more. A password change (which bumps `session_version`) ends every connection. Tokens only work at `/mcp`; at `/api/v1` they are refused.
- **Actor:** `ActorAgent`, the member's id, label the application's name, scope read or write. The MCP client's self-reported name is not prefixed, as it is for API tokens: the application already has a name the member approved.
- **Timeline:** "got access" and "lost access" are audit rows under the application's name (`app.connect`, `app.disconnect`, entity `connected_app`), with no undo. Changes it makes carry its name like any agent's.
- **Reconnecting** the same self-registered application (same name and redirect URIs) replaces the member's earlier connection of it: cloud assistants register again each time.
- **Limits per IP:** registration 10, then 1 a minute; authorization 20, then 1 every 3 s; token and revocation 30, then 1 every 2 s. At most 1 000 registered clients (the oldest never approved is dropped) and 10 000 pending requests. Expired tokens, codes, dead connections and idle registrations are cleaned up from the requests themselves, at most every 10 minutes.
- **CSRF:** `register`, `token` and `revoke` bypass the cross-origin check (they use no cookie, and browser-based clients such as MCP Inspector call them); the well-known documents and these endpoints send `Access-Control-Allow-Origin: *`. The consent endpoints keep the check.
- `offline_access` is listed in the server's `scopes_supported` only so that clients know refresh tokens exist (some, like ChatGPT, look for it); it is ignored in requests. It is not in the resource's scopes (MCP spec).

## Consequences — cost we accept

- We own a security-sensitive component. It is small on purpose, and the tests drive it end to end with the **official MCP Go SDK client** (discovery, registration, PKCE, `iss` check) and through every failure the spec names: wrong verifier, wrong resource, reused code, reused refresh token, expired code, access and refresh token, revocation, scope limits, password change. The **MCP Inspector** CLI (2.12.0) completed the flow against a local build.
- No `insufficient_scope` step-up (403 with `WWW-Authenticate`): a write with a read connection is a tool error that says it has read-only access. The person reconnects with more access. Doing it at the HTTP layer would mean reading JSON-RPC bodies before MCP does.
- No `private_key_jwt`, no pre-registered clients, no OpenID Connect (`openid`, ID tokens): no MCP client we target needs them.
- A restart forgets consent pages that are open (not tokens or codes).
- Behind a public URL, local OAuth clients must use the public address too; API tokens keep working everywhere.
- Not verified against Claude.ai, ChatGPT or Grok themselves: no public instance was available. The steps in [the guide](../guides/remote-agents.md) follow their current documentation.

## Revisit when

- The MCP spec changes its authorization model (for example makes `iss` or step-up mandatory, or drops dynamic registration).
- A target client needs something listed above as not done.
- We need tokens other servers verify without calling Exhale (then JWTs and keys, and a library becomes worth its weight).
