# 0018. Agent-ready app: AI is a first-class feature of v1

- Status: Accepted (OAuth authorization-server library to be evaluated before milestone 3)
- Date: 2026-11-06

## Context

AI agents (Claude, ChatGPT, Grok, Cursor, LM Studio, local models…) must be able to **connect easily**, get **ready-made context**, and **read and manage everything**. This is a primary feature of v1, not an add-on.

"Agents" means two directions:

- **A) The agent uses the app.** The app exposes tools, resources and prompts over **MCP**.
- **B) The app uses a model.** Natural language in Telegram, Slack and the web; importing statements; insights.

The architecture already fits: ⑥ is channel-agnostic and ⑦ holds every business operation. An MCP server is one more adapter over ⑦, and the in-app assistant is one more client of the same tool registry.

## Decision

### New piece ⑰ Agents (`internal/agents`)

| Part | What |
|---|---|
| **Tool registry** | Generated from the ⑦ use-case metadata: typed input/output schemas, descriptions, scopes, MCP annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`) |
| **MCP server** | Official `modelcontextprotocol/go-sdk`. Transports: **stdio** (`exhale mcp`) and **Streamable HTTP** (`/mcp`) |
| **Context** | An `exhale://context` resource: a live household briefing (currency, time zone, members, active subscriptions, upcoming charges, items needing attention, how dates and series work, glossary). Also the MCP server `instructions`, one resource per entity (`exhale://subscriptions/{id}`), and change notifications driven by the bus ⑫ |
| **Prompts** | Monthly review, find savings, audit price increases, import a statement, clean up categories |
| **LLM gateway** | An own thin client for **two protocols**: OpenAI-compatible (OpenAI, xAI Grok, OpenRouter, Ollama, LM Studio, vLLM, Mistral, Gemini-compat) and Anthropic Messages. Official SDKs are rejected because they pull the Azure and AWS SDKs; to be confirmed by measurement |
| **In-app assistant** | A tool-calling loop (~150 lines, no agent framework) over the same registry. Used from Telegram and Slack (free text), the web (⌘K "Ask") and imports |

### Parity rule

**Anything the web can do, an agent can do, and an agent can read everything.** A CI test fails if a ⑦ use case is neither exposed as a tool nor explicitly excluded with a reason.

Excluded: password changes, creating tokens or OAuth clients, deleting the household, reading secrets.

### Safety rules

1. **Models propose, humans confirm.**
   - Writes have a **preview** mode.
   - Without a trusted token, destructive or financial writes need confirmation: MCP elicitation where the client supports it, otherwise a `preview → confirm(confirmation_id)` pair.
   - The in-app assistant always confirms through ⑥ buttons.
2. **Scoped tokens:** `read`, `write`, and `trusted` (skips confirmation for non-destructive writes).
3. **Every agent change is visible and reversible.** It appears in the audit log (⑯) with the agent's name and token, and the user gets a chat message with an **Undo** button. Undo is a first-class use case: every write records its inverse.
4. **Documents are data, never instructions.** Imports use fixed extraction schemas, and extracted text cannot trigger tools.
5. **Local-first and opt-in.** Ollama and LM Studio are suggested first. Cloud providers show clearly which data leaves and where to. There is still no telemetry.
6. **Observable.** OpenTelemetry GenAI conventions: model calls, tool calls, tokens and cost per provider, all shown in the reminder timeline.

### Easy connection

- An **"Connect an agent"** page generates ready-to-paste configs: Claude Desktop/Code, Cursor, LM Studio, a generic MCP client, and a remote URL for ChatGPT, Claude.ai and Grok. It also shows a token QR code and a test button.
- **Remote MCP** (cloud assistants call from their own cloud) needs a public HTTPS URL and **OAuth 2.1** per the MCP authorization spec:
  - Protected resource metadata, PKCE, Client ID Metadata Documents or dynamic client registration.
  - The app acts as its own authorization server. Library to evaluate: `zitadel/oidc` (OP package) vs `ory/fosite` vs a minimal own implementation.
  - Exposure guides: Tailscale Funnel, Cloudflare Tunnel, reverse proxy.

### Agent-ready repository too

- `AGENTS.md` (and `CLAUDE.md`) with build, test and architecture rules, so coding agents can contribute well.
- `llms.txt` on the docs site.

## Milestones (updated)

1. **M1:** core (⑧⑨⑩⑬⑦) + web + Telegram + observability basics + **local MCP (stdio + LAN HTTP with tokens), context resource, full read and write tools with preview/confirm**.
2. **M2:** Slack + **in-app assistant** (free text in chat and web) + **statement import** (CSV/PDF/screenshot).
3. **M3 (before 1.0):** **remote MCP with OAuth 2.1** for ChatGPT, Claude.ai and Grok.

## Testing

- Deterministic conversation tests with a fake model.
- An optional eval set run against real models with the user's key.
- MCP tests with the go-sdk in-memory transport, plus a check with MCP Inspector.

## Revisit when

The MCP spec changes its authorization model, or a provider can only be reached through its official SDK.
