# The built-in assistant

Ask Exhale in plain language, in the web and in chat, and let it read a scanned statement or a screenshot into the import list.

It is **off by default**. You bring the model: Claude, OpenAI, or one running on your own machine. Nothing is sent anywhere until an owner turns it on.

> If you already use Claude, ChatGPT or another agent, you may not need this: connect it to Exhale over MCP instead (**AI agents** in the web). The built-in assistant is for asking from inside Exhale and from Telegram.

## What is sent, and where

While it is on, each message you type to it goes to the provider you chose, together with:

- a **briefing** of your household: today's date, the names of the people you share with, the subscriptions **you** can see (name, price, cycle, next charge, payment method, category), the next 30 days of charges and what needs attention;
- what Exhale **looks up** to answer (for example the history of one subscription, including its notes);
- the last few turns of the conversation, for half an hour.

When you import a **scanned PDF, a picture or pasted text** that Exhale cannot read by itself, that file is sent too. When you press **Read dates** on a document attached to a subscription, that document is sent, with the subscription's name. When you import a **bank export (CSV)** or a **PDF with text**, Exhale reads it by itself and sends only the merchant descriptions, to tidy the names: no amounts, no dates.

Never sent: passwords, API tokens, the bot token, the model's own API key, and subscriptions another member marked private.

Nothing goes to the makers of Exhale. There is no telemetry.

## Turn it on

**Settings → Built-in assistant** (owners only). Pick a provider, save, press **Test**.

### Claude

1. Create an API key at [console.anthropic.com](https://console.anthropic.com).
2. Provider: **Claude (Anthropic)**. Paste the key. Leave the model empty to use `claude-opus-5-5`, or type another, for example `claude-sonnet-5-5` or `claude-haiku-5-5` (cheaper and faster).
3. Save, then Test.

### OpenAI

1. Create an API key at [platform.openai.com](https://platform.openai.com).
2. Provider: **OpenAI-compatible**. Paste the key and type the model. Leave the address empty.

The same provider works for **OpenRouter** (address `https://openrouter.ai/api/v1`) and any other service that speaks the OpenAI chat completions protocol.

### A local model with Ollama

Nothing leaves your network.

1. Install [Ollama](https://ollama.com) and pull a model that supports **tools**, for example `ollama pull llama3.1` or `ollama pull qwen2.5`.
2. Provider: **OpenAI-compatible**. Address: `http://localhost:11434/v1` (from Docker: `http://host.docker.internal:11434/v1`). Model: the name you pulled. No key.
3. Press Test. If it says the model "did not use the test tool", pick a model with tool support: questions and changes need it.

**LM Studio** works the same way with the address it shows (usually `http://localhost:1234/v1`).

Small local models answer simple questions well and get complex requests wrong more often. Every change still waits for your confirmation, so a wrong one costs a tap on **Don't**.

### From the environment

For Docker or a service, the same settings as variables. Each one that is set wins over the web and shows there as "Set on the server".

```sh
EXHALE_ASSISTANT_PROVIDER=anthropic        # or openai
EXHALE_ASSISTANT_API_KEY=sk-ant-…
EXHALE_ASSISTANT_MODEL=claude-opus-5-5     # optional for anthropic
EXHALE_ASSISTANT_BASE_URL=                 # e.g. http://ollama:11434/v1
```

The on/off switch stays in the web.

## Ask

In the web: the **Ask Exhale** box on Overview, or **⌘K** / **Ctrl+K** on any page. In Telegram: just type.

Questions get an answer:

- What do I pay per month?
- What renews this week?
- How much have I paid Netflix in total?
- Which subscriptions are yearly?
- What could I cancel?

Requests get a **card** that says exactly what would change, with **Confirm** / **Discard** (in chat: **✅ Do it** / **✖ Don't**). Nothing changes until you confirm, and what you confirmed has **Undo**:

- Add Netflix, 13.99 on the 6th
- Netflix went up to 15.99
- Move this month's gym charge to the 20th
- Mark the Spotify charge as paid
- Cancel Disney+ at the end of the period
- Pause the gym

The card is written by Exhale, not by the model: if the model misunderstood, the card shows it. Cancelling here stops the reminders; cancel with the provider too.

It answers in the language you write in.

## Import a statement or a screenshot

**Settings → Data → Import**, or send the file to your Telegram bot.

| What | Needs the assistant | How it is read |
|---|---|---|
| Bank export of transactions (CSV) | No | Exhale reads it. Two or more months find the charges that repeat |
| Statement in PDF with text | No | Exhale reads the lines (a date, a description, an amount) |
| Lines copied from a statement | No | Same as a PDF: paste them in the box |
| Scanned PDF (no text) | Yes | The model reads it |
| Screenshot or photo (PNG, JPEG, WebP) | Yes | The model reads it: a subscriptions page in your bank's app, an App Store list, an email |
| Other pasted text (an email) | Yes | The model reads it |

The list has up to three parts:

- **Paid, according to the statement.** A payment that matches a charge Exhale expected: the same service, within a week of the date, an amount within 30%. Ticked lines are marked paid. If the bank took another amount, Exhale asks whether it is the new price.
- **Expected but not in this statement.** Charges due in the period that the statement does not show (only for the cards the statement covers). Worth a look: a failed payment, or a subscription that ended.
- **New subscriptions.** Charges that repeat on a regular cycle and are not in Exhale yet. Each line says what it is based on ("6 charges every month, last on 2026-11-06").

Tick what to apply. Nothing is saved before that, and the file itself is never stored.

**From Telegram:** send the file (as a document or a photo) to your bot. It answers with the same list; tap a number to skip a line, then **Accept all** or **Apply**. A list longer than eight lines waits in the web (Settings → Data → Import) for a day, with a link when `EXHALE_PUBLIC_URL` is set. Slack does not take files yet.

Limits: pictures up to 5 MB, PDFs and other files up to 20 MB, text sent to a model up to 200 KB. A scanned PDF needs a model that accepts files (Claude and OpenAI do; most local models do not: export a CSV or upload a screenshot instead). Dates must include the year.

Agents can do the same over MCP: `match_statement` with the payments, then `set_charge_status` for each match after showing the user.

## Read the dates of a contract

Attach the contract, the terms or an order confirmation to a subscription (**Documents** on its page), then press **Read dates**. The model reads it and suggests:

- the notice period (in days; a month counts as 30 days),
- the page where it is cancelled or managed,
- when it started, and when a free trial ends,
- the account or customer number it is under.

Each suggestion shows the words of the document it comes from, and what Exhale has now. Untick what is wrong. Exhale checks the rest as an edit, shows the last day to cancel it would give, and saves only when you press **Apply**. The model has one tool, to report what it found, and cannot change anything; whatever the document says, the worst it can do is suggest a wrong value for you to untick.

Limits: PDFs up to 10 MB (the size of an attachment), pictures up to 5 MB. Without the assistant, the button is not there; documents can still be attached, downloaded and deleted.

## Limits and costs

You pay your provider for what the assistant uses. To keep that bounded, one message calls the model at most 6 times, prepares at most 5 changes, and each member can send a burst of 12 messages and then one every 15 seconds. The server log shows the tokens of each answer (`assistant answered … input_tokens=… output_tokens=…`), never the words.

## When it does not work

| It says | Meaning |
|---|---|
| The model provider rejected the API key | The key is wrong, revoked or out of credit. Fix it in Settings |
| The model set in Settings does not exist… | A typo in the model name, or the key cannot use that model |
| The model did not answer | Timeout or the address cannot be reached. For Ollama: is it running, is the address right from where Exhale runs? |
| The model provider is asking to slow down | The provider's rate limit. Wait a minute |
| That is a lot of messages in a short time | Exhale's own limit per member |
| The model declined to answer that | The provider's safety filter refused the request |

## Check after setting it up

The assistant is tested with a scripted model; it has not been run against every real one. With a new provider, check these once:

1. **Test** in Settings says the model "can use Exhale's tools".
2. Ask "what do I pay per month?": the number matches Overview.
3. Ask to add a subscription: a card appears, and nothing exists until you confirm. Confirm, then Undo.
4. Ask to cancel one: the card is marked as destructive and names the right subscription. Discard it.
5. Import a screenshot: the list matches the picture.

Developers can run the eval set against a real model: `EXHALE_EVAL_API_KEY=… go test ./internal/assistant -run TestEval -v`.
