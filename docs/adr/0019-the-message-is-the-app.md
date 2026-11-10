# 0019. The message is the app

- Status: Accepted (milestone 1 ships the Telegram home message; Slack App Home and generated screens follow in milestone 2)
- Date: 2026-11-09

## Context

The vision says the notification is the interface: you fix a subscription from the reminder itself (ADR 0003, 0007). Using the bot every day made it clear this should go further. **A chat message can be a whole app**: you navigate it, act from it and it stays current, without opening the web.

Vercel's json-render + Chat SDK (November 2026) shows the same idea for business chat. An LLM writes a JSON spec against a typed catalog of components (Card, Fields, Table, Chart, Actions), the spec is validated, and each platform adapter renders it natively (Block Kit on Slack, Adaptive Cards on Teams, inline keyboards on Telegram). We want that idea, built into our own architecture.

What it has to do:

1. Telegram users manage everything from the chat: home, list, detail, upcoming charges, summary, and every action already in the reminders.
2. The same screens work on Slack (milestone 2) and the web, at the best fidelity each platform offers.
3. Screens can later be produced by the in-app assistant (ADR 0018) through the same path, so the assistant answers with an app instead of a paragraph.
4. Keep the hard constraints: one Go binary, outbound-only connections, no public URL.

## Criteria

- One description of a screen, many renderers. Adapters never decide content (ADR 0003).
- Buttons can only trigger operations that exist in the op catalog (ADR 0018). Nothing a model writes can invent an action.
- Callback data stays within Telegram's 64 bytes.
- No new runtime: no Node sidecar, no webview that needs HTTPS.

## Options

| Option | Verdict |
|---|---|
| **A) Adopt json-render + Chat SDK** | Rejected. TypeScript only, so it needs a Node process beside the Go binary (breaks ADR 0001/0002). Chat SDK is a full bot framework that would replace our adapters and action layer. Its Telegram adapter has no modals or ephemeral messages and leaves the 64-byte callback limit to the caller. |
| **B) Telegram Mini App** (a web view inside Telegram) | Rejected as the default. It needs a public HTTPS URL, which breaks outbound-only (ADR 0001). Could come back as an opt-in for people who already expose Exhale. |
| **C) Separate message per command** (`/upcoming` replies with text) | What we had. It fills the chat with stale snapshots and nothing is tappable. |
| **D) Own screen format in Go + a living message per chat** | **Chosen.** |

## Decision

### 1. One screen format in the action layer

`actions.Message` is the screen description: a title, lines, **sections** (a heading with lines or a small table), rows of buttons and a footer. It grows towards the json-render component set as renderers need it:

| Screen element | Telegram | Slack (M2) | Web |
|---|---|---|---|
| Title | bold first line | `header` block | page heading |
| Lines | text | `section` mrkdwn | text |
| Section heading + lines | bold heading + text | `section` with heading | card |
| Section table | `<pre>` aligned columns | `table` block (or code block) | table |
| Button rows | inline keyboard | `actions` block | buttons |
| Footer | italic line | `context` block | muted text |
| *(M2)* Chart | rendered image (`editMessageMedia`) | image | Recharts |
| *(M2)* Form fields | step flow with typed reply (`flows` table) | modal with inputs | form |

The fake text channel renders every element too, so conversation golden files cover all screens.

### 2. The home message is a living app

- `/start` (once linked), `/home`, `/subs`, `/upcoming` and `/summary` post **one app message** and pin it. Every tap **edits that same message**, so it behaves like screens of an app: home → subscriptions → one subscription → its next charge → back home.
- App screens: **Home** (monthly and yearly spend, what needs attention, the next 7 days as tappable charges), **Subscriptions** (paginated, tap to open), **Subscription** (details, next charge, pause/resume, cancel with confirmation), **Upcoming** (next 30 days), **Summary** (totals, by category, by payment method) and **Charge** (the same actions as a reminder).
- Inside the app, the charge actions are exactly the reminder actions: one code path, plus a 🏠 Home button.
- The message is **live**. When anything changes (web, another chat, an AI agent, the scheduler), the bus (⑫) re-renders the screen the app message is showing. An hourly pass keeps relative dates ("in 3 days") correct across midnight. If the user is in the middle of a step (calendar, scope question), the message is not refreshed under their finger.
- Only the most recent app message per chat stays live and pinned. Older ones keep working, because buttons carry their own state.
- The message records the screen it shows (`messages.kind = 'app'`, `messages.route`), so a refresh knows what to redraw.

### 3. Buttons bind to the op catalog

Callback data is `a|<screen>|<arg>` for navigation and the existing `verb|chargeID|…` for charge actions. Every write still goes through `app.Service` (audit log, events, undo). When the assistant generates screens in M2, its buttons will reference catalog operations by name with arguments stored in `flows`, so only a short id travels in the callback, and destructive operations always pass through a confirmation screen.

### 4. Generated screens (milestone 2)

The in-app assistant (ADR 0018) will answer with a screen: the model writes JSON against a JSON Schema generated from the Go screen types. The server validates it and returns **one readable error per problem** for the model to fix (the json-render `CardSpecError` idea). Per-channel limits either degrade the screen (a second table becomes a code block, a chart becomes an image) or reject it. Streaming edits are throttled to at most one every 500 ms per message.

### 5. Slack (milestone 2)

Slack is where "an app inside the chat" goes furthest, and it all works over Socket Mode (outbound): **App Home** is the home screen as a per-user tab, **modals** give real forms (date picker, dropdown, number field), and Block Kit messages carry the same screens into shared channels. That also makes Exhale useful to small teams that manage shared SaaS tools, at no extra design cost. Teams is left out: its bots need a public HTTPS endpoint.

## Consequences — cost we accept

- One more column pair on `messages` and a re-render on every relevant event. The household scale (tens of subscriptions, a few chats) makes this negligible; Telegram's "message is not modified" answer is treated as success.
- Telegram has no inputs inside a message: amounts and names are typed replies, dates use the button calendar.
- Screen text is English only until i18n reaches the bot (ADR 0014).
- Pinning needs the bot to be allowed to pin; if it is not, the app message still works unpinned.

## Revisit when

- Telegram adds input fields to messages, or Mini Apps stop needing a public URL.
- json-render ships a Go implementation or a language-neutral spec we could validate against directly.
- Users ask for Teams.

## Implementation notes: Slack (2026-11-10)

What section 5 promised, as built in `internal/channels/slack` (see also the notes in ADR 0007). Nothing has run against a real workspace yet.

| Screen element | Slack, as shipped |
|---|---|
| Title | `header` block |
| Lines | one `section` (mrkdwn), split when over 3000 characters |
| Section heading + lines | `section` with the heading in bold |
| Section table | monospace code block inside the section. Slack's `table` block exists for messages and the Home tab, but its availability and its limits per message are still moving, so aligned text stays the default |
| Button rows | one `actions` block per row; a link button for `Button.URL` |
| Footer | `context` block |
| Typed answers | a direct message to the app (`message.im`); modals are not built yet |

- **Direct message.** Reminders arrive there. `/exhale [screen]`, or a plain word typed in the direct message (`home`, `subs`, `add`, `upcoming`, …), posts the app message, pins it and retires the one before, exactly as in Telegram. Slack keeps the slash for its own commands, so screens that say "send /home" read "send /exhale home" on Slack: the adapter rewrites command names, nothing else.
- **Home tab.** It is the home screen, published with `views.publish` when the tab is opened and again on every button press. It is recorded as an **app surface**: an app message with the reserved id `@home` that `AppSent` never retires. That is the one extension to the action layer (`actions.SurfacePrefix`, `Engine.AppSurface`, about 15 lines in `app.go`): with it the Home tab shares flows with the direct message and is kept live by the same `refreshApps` pass as every app message, through `Channel.Edit`.
- **Toast.** Slack has none. A toast is whispered (an ephemeral message through the action's `response_url`) only when it is the only feedback, for example an error. When the message itself changed, the change is the feedback; whispering on every tap would fill the conversation with "only visible to you" lines. In the Home tab a toast becomes the footer of the view.
- **Shared channel.** With a team channel set, a reminder of a shared subscription is also posted there once (not once per member) by a wrapper around the scheduler's delivery step. Those messages are recorded without a member, so the action layer leaves them alone and the Slack manager re-edits them itself when their charge changes. A button pressed in the channel runs as the linked member who pressed it; an unlinked person gets a whisper. Private subscriptions are filtered out by their visibility before anything is rendered.
- **Linking and invites.** `/exhale link CODE` or the code alone in the direct message (link codes with channel `slack`); `/exhale join CODE` accepts a household invite and names the member after their Slack profile.

**Left for later**

- **Modals** for typed input (date picker, number field, dropdown), which would replace the typed replies and the button calendar on Slack, and keep a shared channel message from turning into a calendar for everyone while one person picks a date.
- Shared channel details: one channel per household (not per category or payer); the "already posted" memory is in process, so a restart between two members' deliveries of the same reminder can post it twice; no Undo button on a channel message redrawn by a change made elsewhere; a subscription that turns private later keeps its earlier channel messages.
- User text is sent as mrkdwn with `&`, `<` and `>` escaped; a name containing `*` or `_` can still pick up formatting.
- Charts and generated screens (sections 1 and 4), on every channel.
