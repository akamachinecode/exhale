# HTTPS and remote access

**Never expose Exhale's plain HTTP port to the internet.** Over `http://`, passwords and the session cookie travel unencrypted. On your own network that is an accepted risk; on the internet it is not.

You may not need any of this. Telegram reaches Exhale through outgoing connections, so reminders and buttons work from anywhere with nothing exposed. Remote access is about opening the web page away from home, and about cloud assistants (Claude.ai, ChatGPT, Grok), which need a public address: see [Claude.ai, ChatGPT and Grok](../guides/remote-agents.md).

| You want | Use |
|---|---|
| The web from your own devices, anywhere | [Tailscale](#tailscale-the-simple-private-option) |
| A public `https://` address | A reverse proxy: [Caddy](#caddy) or [nginx](#nginx) |
| Claude.ai, ChatGPT or Grok to reach Exhale | A public address (a reverse proxy, or `tailscale funnel`), then [Claude.ai, ChatGPT and Grok](../guides/remote-agents.md) |

## What Exhale needs from whatever sits in front

Exhale itself speaks plain HTTP. Something in front adds HTTPS, and four things must be right:

1. **Tell Exhale it is behind HTTPS:** `EXHALE_SECURE_COOKIES=true` (or `--secure-cookies`). The session cookie is then only sent over HTTPS. Exhale does not read `X-Forwarded-Proto`; this setting is how it knows. Do not set it while you still use plain `http://`: the browser would not send the cookie and nobody could sign in.
2. **The proxy must send `X-Forwarded-For`.** Exhale treats a request as coming from the server's own machine when it arrives from `127.0.0.1` *without* that header. Such requests skip the setup code in the first-run wizard. A proxy on the same machine that omits the header makes every visitor look local. Caddy and Tailscale Serve send it by default; nginx needs one line.
3. **The proxy must pass the `Host` header unchanged.** Exhale rejects cross-site writes by comparing the browser's `Origin` with `Host`.
4. **No buffering.** The web page receives live updates as a stream (`/api/v1/events`), and so do AI clients on `/mcp`.

Also:

- Keep Exhale listening on `127.0.0.1:8484` (the default), so only the proxy can reach it. In Docker, publish the port as `127.0.0.1:8484:8484`.
- Never use `--trust-local` on such a server.
- Exhale sees the proxy as the client, so its limit on sign-in attempts per address is shared by all visitors. The limit per email still applies. Reading the real address from a trusted proxy is planned.

## Tailscale: the simple private option

[Tailscale](https://tailscale.com) puts your devices in a private network. Nothing is exposed to the internet and there are no certificates to manage.

Install Tailscale on the server and on your phone and laptop. Then, on the server:

```sh
tailscale serve --bg 8484
```

Exhale is now at `https://<machine>.<your-tailnet>.ts.net`, with a real certificate, for your devices only. Set `EXHALE_SECURE_COOKIES=true` and restart Exhale.

`tailscale funnel` is different: it publishes the address to the whole internet. Treat it like a public reverse proxy and read [Before you go public](#before-you-go-public).

## Caddy

Caddy gets and renews the certificate by itself. Point your domain at the server, open ports 80 and 443, and use this `Caddyfile`:

```caddyfile
exhale.example.com {
	reverse_proxy 127.0.0.1:8484
}
```

Caddy passes `Host`, adds `X-Forwarded-For` and does not buffer streams. Nothing else is needed.

## nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name exhale.example.com;

    ssl_certificate     /etc/letsencrypt/live/exhale.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/exhale.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8484;
        proxy_http_version 1.1;

        # Required: tells Exhale the request did not start on this machine.
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        # Required: Exhale compares the browser's Origin with Host.
        # $http_host keeps the port if you serve on one other than 443.
        proxy_set_header Host $http_host;

        # Live updates (/api/v1/events) and MCP (/mcp) are streams.
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}

server {
    listen 80;
    server_name exhale.example.com;
    return 301 https://$host$request_uri;
}
```

Get the certificate with [certbot](https://certbot.eff.org) or your usual tool.

## Check it

After setting it up, from any computer:

```sh
curl -s https://exhale.example.com/healthz                  # ok
curl -s https://exhale.example.com/api/v1/auth/session
```

On an instance that has no account yet, the second answer must contain `"setupNeedsCode":true`. If it says `false`, the proxy is not sending `X-Forwarded-For`: fix that before going further.

Then sign in with a browser and look at the cookie `exhale_session`: it must be marked `Secure`.

## Before you go public

- Use long, unique passwords. Exhale requires at least 10 characters and slows down repeated sign-in attempts, but it has no second factor yet. Sign-in through your identity provider (OIDC) and two-factor codes are planned.
- If only your household or team uses it, prefer Tailscale or a VPN to a public address.
- Keep Exhale up to date.
