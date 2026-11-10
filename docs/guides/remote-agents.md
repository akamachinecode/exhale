# Claude.ai, ChatGPT and Grok

Cloud assistants run on their makers' servers, so they reach Exhale over the internet. You give them one address, `https://<your Exhale>/mcp`, and approve them on a page of your Exhale. No token to copy.

They can then read everything you can see and, if you allow it, change things, with the same previews, confirmations and undo as any other agent.

> For Claude Code, Claude Desktop, Cursor or LM Studio on your own machine you do not need any of this: see **AI agents** in the web.

## What you need

1. **A public `https://` address for Exhale.** The assistant's servers must reach it. Set it up with [HTTPS and remote access](../install/https.md): a reverse proxy (Caddy, nginx) or `tailscale funnel`. Plain `tailscale serve` is not enough: it is private to your own devices.
2. **Tell Exhale its address**, and that it is behind HTTPS, then restart it:

   ```sh
   EXHALE_PUBLIC_URL=https://exhale.example.com
   EXHALE_SECURE_COOKIES=true
   ```

   Only the origin: no path, no trailing `/mcp`.
3. **Outgoing HTTPS from the server.** Claude.ai and Grok identify themselves with a document on their own site, which Exhale downloads.

Check it from any computer:

```sh
curl -s https://exhale.example.com/.well-known/oauth-protected-resource/mcp
```

The answer must show `"resource":"https://exhale.example.com/mcp"`. The same address appears in **AI agents → Claude.ai, ChatGPT and Grok**; if that card says a public address is needed, `EXHALE_PUBLIC_URL` is not set.

## Claude.ai

1. **Settings → Connectors → Add custom connector.** On Team and Enterprise plans an owner of the organization adds it; each person then connects with their own account.
2. Name: `Exhale`. URL: `https://exhale.example.com/mcp`. Leave the advanced OAuth fields empty.
3. **Add**, then **Connect**. Your Exhale opens: sign in if asked, choose the access and press **Allow**.

Claude also works this way in the desktop and mobile apps once the connector is added.

## ChatGPT

Custom connectors need **developer mode** (paid plans; on Business and Enterprise an admin may have to allow it first).

1. **Settings → Apps → Advanced settings**, turn on **Developer mode**.
2. Back in **Apps**, press **Create**. Name: `Exhale`. MCP server URL: `https://exhale.example.com/mcp`. Authentication: **OAuth**. Tick that you trust the app and press **Create**.
3. Your Exhale opens: sign in if asked, choose the access and press **Allow**.
4. In a chat, pick Exhale from the tools menu, or mention it by name.

## Grok

1. Open [grok.com/connectors](https://grok.com/connectors), press **New Connector** and choose **Custom**.
2. Name: `Exhale`. URL: `https://exhale.example.com/mcp`. Press **Add Connector**.
3. A window opens on your Exhale (allow pop-ups from grok.com): sign in if asked, choose the access and press **Allow**. Exhale then shows as connected.

## The page you approve on

Exhale shows:

- **Who asks.** A green badge with an address (`claude.ai`, `grok.com`) means Exhale downloaded the application's description from that site. **Unverified** means the application registered itself and its name cannot be confirmed: approve only if you just started the connection.
- **Where you go back to** after answering. A warning appears when that is a program on your own computer.
- **The access:**
  - **Read only:** sees subscriptions, charges, spending and settings; changes nothing.
  - **Read and change:** can also make changes. Deleting and other destructive changes still ask for a confirmation, and every change can be undone in **Activity**.

The application acts **as you**: it sees what you see, not other people's private subscriptions. It never gets more than read and change (no "change without asking", unlike a trusted token).

## Disconnect

**AI agents → Connected apps** lists every application you approved, with when it was last used. The bin button disconnects it at once.

Changing your password disconnects every application. So does the application itself when you remove the connector on its side.

In **Activity** you see "ChatGPT got access", every change it made under its name, and "ChatGPT lost access".

## Good to know

- Access lasts one hour and the application renews it in the background. A connection nobody uses for 90 days ends by itself.
- These connections only work on `/mcp`. They cannot open the web or the REST API.
- Once `EXHALE_PUBLIC_URL` is set, use the public address in every application that connects this way, also on the server's own machine. API tokens keep working at any address.
- Each person connects their own assistant. Members of your household see their own connected apps.

## Problems

| You see | Fix |
|---|---|
| "Remote connections are not set up on this server" | Set `EXHALE_PUBLIC_URL` and restart. |
| "Exhale could not identify the application" | The server cannot download the application's description: check that it can make outgoing HTTPS requests. The log line `oauth: authorization request refused` says why. |
| "The application sent a request Exhale cannot accept" | Usually the address: it must be exactly the one in **AI agents**, ending in `/mcp`. The log says which part was refused. |
| The assistant says it cannot reach the server | Check the `curl` above from outside your network. The proxy must pass the `Authorization` header and must not buffer `/mcp`. |
| The assistant suddenly asks to connect again | It presented a renewal token that was already used. Exhale treats that as a copied token and ends the connection (log: `refresh token reused`). Connect again; if it keeps happening, open an issue. |
| "This request has expired or was already answered" | The approval page is valid for 10 minutes. Start again from the assistant. |

How it works, for the curious: [ADR 0024](../adr/0024-oauth-authorization-server.md).
