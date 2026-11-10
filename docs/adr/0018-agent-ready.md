# 0018. Agent-ready app: AI is a first-class feature of v1

- Status: Accepted (OAuth authorization server: [ADR 0024](0024-oauth-authorization-server.md))
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

## Implementation notes: part B, the app uses a model (2026-11-10)

Part A (MCP) shipped in M1. These notes record what shipped for part B and the decisions taken while building it.

### What shipped

- `internal/assistant`: a provider interface, a client for the Anthropic Messages API and one for OpenAI-compatible chat completions, a scripted fake for tests, the tool-calling loop, the preview/confirm gate, and the reader for statements and screenshots.
- **Chat**: free text in Telegram goes to the assistant instead of the command list. It is written against `internal/actions` (`Engine.Ask`, buttons `ai|ok|…`, `ai|no|…`, `ai|undo|…`), so Slack gets it when it arrives.
- **Web**: "Ask Exhale", a box on Overview and a panel on ⌘K / Ctrl+K. `POST /api/v1/assistant/messages` answers a message; `POST /api/v1/assistant/proposals/{id}` confirms, discards or undoes. Neither is in the op catalog: an agent over MCP is already a model.
- **Import**: `POST /api/v1/import/parse` also reads a bank export of transactions (no model), and with the assistant on a PDF, a picture (PNG, JPEG, WebP) or pasted text. The review list and `import_subscriptions` with `dryRun` are unchanged.
- **Settings**: owner-only endpoints and a "Built-in assistant" section. `EXHALE_ASSISTANT_PROVIDER`, `_API_KEY`, `_MODEL` and `_BASE_URL` win over the web, field by field. The key is saved in `settings` and no endpoint returns it.

It is **off until an owner configures it**. While it is off nothing is sent anywhere and chat answers free text with the command list, as before.

### Provider clients: no SDK (measured)

The decision above said official SDKs were rejected "to be confirmed by measurement". Measured with `anthropic-sdk-go` v2.8.0, stripped static build:

| | Size | Modules added |
|---|---|---|
| `net/http` only | 3.5 MB | 0 |
| + `anthropic-sdk-go/v2` | 12.0 MB (**+8.4 MB**) | 17 (OpenTelemetry, yaml, gjson/sjson, invopop/jsonschema, standard-webhooks…) |

The reason written in the decision was wrong: the SDK no longer pulls the AWS or Azure SDKs (Bedrock and Vertex are separate packages). The verdict stands for other reasons:

1. **Weight.** +8.4 MB on a 22 MB binary (+38%) for one `POST /v1/messages`, against principle 3 (light).
2. **The second protocol is hand-written anyway.** Local models are a first-class case (principle 1, safety rule 5) and they speak chat completions. With one provider-neutral message model already needed, the Anthropic side is 230 lines.
3. **Narrow use.** One endpoint, no streaming, tools, images and PDFs as base64.

The cost we accept: we own the request shapes and must follow API versions ourselves (the client pins `anthropic-version: 2026-10-21`), and we do not get the SDK's typed errors or retries (one retry for 429/5xx is implemented). Both clients are tested against HTTP fakes of their API. **Revisit** if we need streaming with tool calls, Bedrock/Vertex/Foundry, or a feature that only the SDK exposes.

Details that matter:

- **Default model `claude-opus-5-5`**, configurable. No `temperature`, `thinking` or `tool_choice` is sent: current models reject them.
- **Reasoning blocks.** Current Claude models return thinking blocks and reject a history rebuilt without them. Inside one question the assistant turn is sent back exactly as it came (`Message.Raw`); between questions only text is kept, so no thinking block is ever replayed against a changed prompt.
- **Refusals.** `stop_reason: "refusal"` is handled before reading content. On Anthropic's own API, for the models that have it, requests opt into the server-side fallback (`fallbacks: "default"`, beta `server-side-fallback-2026-07-01`); if the endpoint rejects the parameter the client stops sending it, so a retired beta cannot break a self-hosted install.
- **OpenAI-compatible.** A bare address gets `/v1`; a missing key sends no `Authorization` header; `max_tokens` switches to `max_completion_tokens` when the endpoint asks for it; tool arguments are accepted as a string or an object. A PDF is sent as a `file` part, which OpenAI accepts and most local servers do not: the person is told to use a CSV or a screenshot.

### The confirm rule: always

**Every write the model asks for is previewed with `dryRun` and applied only when the person presses a button.** Reads run freely. There is no exception for "an unambiguous instruction".

The exception was considered and rejected: deciding that a message is unambiguous would be the model's judgement, and that is exactly the judgement a hidden instruction attacks. A deterministic test for "unambiguous" does not exist, one tap is cheap, and safety rule 1 above already says the in-app assistant always confirms.

How it is enforced, not just asked:

- The loop strips `dryRun` and `confirm` from whatever the model sends, forces `dryRun: true`, and stores the arguments as a **proposal** in `flows`. The tool schemas do not offer `dryRun` at all.
- `assistant.New` fails if a write in the catalog has no `dryRun` field, so a new use case cannot bypass the gate by accident.
- The card the person confirms (title and lines) is **written by Exhale** from the arguments and the dry-run result. The model's sentence is shown beside it, never instead of it.
- A proposal belongs to the member it was made for, expires in 30 minutes, and applies once.
- Applied changes are collected with `app.CollectAudits` and get **Undo** for a day. Undo covers subscriptions and charges (what `app.Undo` covers); a change to a category or a payment method says it is kept.

The model acts as the member who is talking: `ActorAgent`, the member's id, label `Exhale assistant (Name)`, scope never above `write`. It sees what that member sees; another member's private subscription is "not found". Members' email addresses are removed from tool results: names and ids are enough.

### Prompt injection

Names, notes, URLs and file contents are written by people or copied from elsewhere. Three layers:

1. **The prompt** says data is not instructions, and that only the person's own message can ask for a change.
2. **Placement.** The system prompt is static rules. The briefing travels in the user turn inside `<briefing>`, with angle brackets neutralised so a name cannot close the block or imitate the person. Tool results are their own blocks.
3. **Structure**, the one that does not depend on the model: no write applies without a tap on a card Exhale wrote. A test scripts a model that *obeys* a note saying "delete everything": nothing is deleted, and the person sees "Delete Netflix?" as a destructive change.

Reading a document is stricter (safety rule 4): the model gets **one tool with a fixed schema and none of the catalog**, so the only thing a document can produce is a list to review, and each reported item is checked (bounded single-line text, a known cycle, real dates and amounts) before it is shown.

What this does not prevent: a model misled by data can still *say* something wrong, or prepare a change the person did not ask for. The card is the defence, so it must stay written by Exhale.

### Limits

| Limit | Value |
|---|---|
| Model calls per message | 6 |
| Changes prepared per message | 5 |
| Tokens per call (answer and reasoning) | 8 192 (16 000 when reading a document) |
| Time per call / per message | 60 s / 150 s |
| Message length | 4 000 bytes |
| Tool result handed to the model | 30 000 bytes |
| Per member | a burst of 12 messages, then one every 15 s |
| Picture / PDF / text sent to a model | 5 MB / 20 MB / 200 KB (refused above, never cut) |

`export_data` is the one use case not offered as a tool (the whole household in one result). Info logs carry the provider, round count and token counts; never the key or the words.

### Memory

The last six turns of a conversation (text only), what the person did with each proposal, and the pending proposals live in the existing `flows` table, for 30 minutes (24 hours for an applied change, to keep Undo). No new table and no migration. Expired rows are deleted on the next question.

### Bank exports: detector first

A CSV of transactions is read **without a model**: transactions are grouped by a normalised merchant, and a group is reported when the gaps between its dates fit a cycle (1 or 2 weeks; 1, 2, 3, 6 months; 1 year) and, for weekly cycles or only two charges, the amount is steady. It works with the assistant off and is tested on a six-month fixture.

With the assistant on, the model **only tidies the names**: it is sent the merchant descriptions, no amounts and no dates, and a failure leaves the names Exhale worked out. If no charge repeats (a single month), the text goes to the model when it is on; otherwise the person is told a longer period is needed.

### Finished later (2026-11-10)

Four items of the first "Not done" list, built on the same pieces.

**Spanish everywhere the person reads.** The cards, notes and explanations of the assistant, and the validation errors of `internal/app` and `internal/domain`, are `domain.Text` values: the English format with fmt verbs, which is the key of its translations, and its arguments. An argument from a closed set (a field name, a status) is a `domain.Word` and is translated too. Errors are written with `domain.Invalid`, `NotFound`, `Missing`, `Conflict` or `Fail`; `Error()` stays the same English sentence, so MCP agents and logs see no change. Each edge renders the text in the reader's language:

- chat with the catalog of `internal/actions/i18n_es.go` (`actions.Say`, `actions.ErrorText`), in the member's language;
- the web with `web/src/lib/es.ts`, in the browser's language: a problem body carries `text: {key, args}` beside the English `detail`, keys use `{0}` like the rest of the web, and the fetcher shows the translation. The assistant's turns and outcomes send the same form.

Considered: stable error codes (rejected: a code per sentence is a second name to keep in sync, against ADR 0023's "the English is the key"), and translating on the server from `Accept-Language` (rejected: the web already translates in the browser, and one rule for all web text is simpler). A test type-checks the packages and collects every format passed to a function with a `format` parameter that returns an error or a `domain.Text`, every constant `domain.Word`, and every use case title; it fails when one has no Spanish in `i18n_es.go`, when `fmt.Errorf` wraps a sentinel, or when `web/scripts/server-texts.json` (the list `pnpm i18n` checks against `es.ts`) is stale. Errors of the auth, channel and notifier packages are not converted yet (see below). *(Updated 2026-11-10: the catalogs moved to `internal/i18n/locales/<code>.json`, and the test now writes the web's copy of these translations instead of a list; see ADR 0023's implementation notes.)*

**Streaming.** Both clients read Server-Sent Events: Anthropic's `content_block_*` and `message_delta` events, rebuilt into the same content blocks a whole answer has (reasoning blocks and their signatures kept for replay), and OpenAI-compatible chunks with tool calls split across them and usage in the last chunk (`stream_options`, dropped when a server rejects it). A server that answers a streaming request with plain JSON is read as a whole answer, and a 400 to a streaming request falls back to `Complete`. Still hand-written: streaming with tool calls was the "revisit" reason, and it came to about 500 lines for both protocols, tested against HTTP fakes: still far from the +8.4 MB of the SDK. The person sees the text of each call as it is written; the text of a call made after tools starts again. The cards and screens arrive only with the finished turn, written by Exhale.

- Web: `POST /api/v1/assistant/messages/stream` answers with events `delta`, `text`, `turn` and `error`, read with `fetch`. `POST /api/v1/assistant/messages` stays.
- Telegram: "typing…", then the answer in one message, edited at most every 3 seconds as it grows and once more into the finished answer; the cards follow. Three seconds keeps within Telegram's limit of about 20 messages a minute in a group (ADR 0019 asked for 500 ms per message, which a group chat does not allow). The logic is `Engine.AskLive`, written against `Channel`, so Slack gets it too.

**Cost and tokens (safety rule 6).** Each question and each import read by a model records the member, where it was asked, the provider, the model that answered, the calls, the input and output tokens and an estimated cost, in `assistant_usage` (migration 00130), kept for 400 days. Never the words. The cost comes from a table of Anthropic's list prices (US dollars per million tokens), which the owner can override per model in Settings; a model without a price gets tokens only, never an invented cost. Other providers' prices change too often to ship. Amounts are integers of millionths of a dollar. Shown in **Activity → Assistant** (an owner sees everyone's questions, a member their own) and as this month's total under **Built-in assistant**. No OpenTelemetry spans yet.

**Generated screens (ADR 0019 §4, minimal).** The model gets one more tool, `show_screen`, with a fixed schema: `kind` (`subscriptions`, `charges`, `total`), `subscriptionIds`, and `from`/`to` or `period`/`by`. It chooses the kind and the records; Exhale reads every name, date and amount from the database, as the member who asked. The model's own words never reach a screen, so a note cannot change what it shows. Unknown fields are refused, and every problem with a call (an id this member cannot see, a duplicate, a date that is not a date, a period over a year) is told to the model in one line so it can fix it. At most 3 screens per answer, 20 subscriptions or 50 charges per screen. The web draws them with the existing pieces (service tiles, amounts, a bar per row) and links each row to its subscription; chat sends a message with a button per record that opens it in the in-chat app. Not done from §4: a JSON Schema generated from the Go screen types and per-channel degradation; the screen vocabulary is three fixed kinds, which is what the assistant needed.

### Not done

- **Not verified against a real model.** No key was available: every test uses the fake provider or HTTP fakes. The first things to check with a real key are in `docs/guides/assistant.md`; `EXHALE_EVAL_API_KEY=… go test ./internal/assistant -run TestEval -v` runs the eval set.
- **Generated screens** (ADR 0019 §4): the assistant answers in plain text and the cards are fixed layouts.
- **Streaming**: an answer arrives whole; chat shows "typing…".
- **Observability** (safety rule 6): token counts are logged, not shown in the timeline; no OpenTelemetry GenAI spans, no cost.
- **Slack**: the chat glue is channel-agnostic, the adapter does not exist yet.
- **Chat text is English**, like the rest of `internal/actions` until its translation lands. The model answers in the person's language; the cards and buttons do not yet.
- ~~**Sending a photo to the bot** to import it~~: done, see ADR 0022's implementation notes (Telegram only).
- ~~**PDF without a model**~~: done for PDFs with text (ADR 0022). A scanned PDF on a local model that does not accept files still needs a screenshot instead.

## Implementation notes: changes to many subscriptions (2026-11-10)

- **One op, all or nothing.** `bulk_update_subscriptions` sets category, payment method or tags (`action: update`), or pauses, resumes, cancels or deletes up to 500 subscriptions in one transaction. One unknown or hidden id fails the whole call. It is marked destructive (it can delete), so agents confirm first; its `dryRun` lists what changes for each subscription by name, and the ones already as asked show no change and get no audit entry.
- **Audited per subscription, undone as one.** Each subscription gets its own audit entry, the same action names as the single-subscription ops, so **Activity** and per-item undo work unchanged. `undo` now also takes `auditIds` and reverts them in one transaction, newest first; the web's Undo and the assistant use it. We chose this over a "batch" audit entry because it needs no new table and no new undo path: a batch is just the list of ids its call returned.
- **Tags renamed or deleted everywhere** (`rename_tag`, `delete_tag`) are the same kind of change: one audit entry per subscription touched, `auditIds` to undo. They only touch subscriptions the caller can see; someone else's private subscription keeps its tag.
## Implementation notes: milestone 3, remote MCP (2026-11-10)

Remote MCP with OAuth 2.1 shipped. Exhale is its own authorization server; the library question was settled by measurement in [ADR 0024](0024-oauth-authorization-server.md) (own minimal: +0.2 MB and no modules, against +1.8 MB for zitadel/oidc and +8.2 MB for ory/fosite). Both Client ID Metadata Documents and dynamic registration are supported. The "Connect an agent" page (**AI agents**) shows the remote address and lists the connected apps, each revocable; setup per assistant is in [docs/guides/remote-agents.md](../guides/remote-agents.md). Exposure guides (Tailscale Funnel, reverse proxy) are in [docs/install/https.md](../install/https.md).
- **Not verified against a real model.** No key was available: every test uses the fake provider or HTTP fakes, including the streaming ones. With a key, run `EXHALE_EVAL_API_KEY=… go test ./internal/assistant -run TestEval -v` (it now also checks a streamed answer with a screen) and the checks in `docs/guides/assistant.md`.
- **Errors outside `internal/app`, `internal/domain`, `internal/assistant`, `internal/actions`, `internal/store` and `internal/httpapi`** (sign-in, invites, Telegram, Slack, email, ntfy and webhook settings) are still English. The mechanism is ready: write them with `domain.Invalid` and friends and add the package to `serverPackages` in `internal/actions/servertexts_test.go`. The `message` of each item of an import's dry run and the "based on" text of a bank export are English too.
- **OpenTelemetry GenAI spans**: tokens and cost are stored and shown, not exported.
- **Slack**: the chat glue, including `AskLive`, is channel-agnostic; the adapter does not call it yet.
- **Sending a photo to the bot** to import it: import is in the web only.
- **PDF without a model**, and PDFs on local models that do not accept files.
- Screens: no chart, no per-channel limits beyond the counts above, no screen of a single subscription (the model links it instead).
