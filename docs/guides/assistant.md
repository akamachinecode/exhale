# The built-in assistant

Ask Exhale in plain language, in the web and in chat, and let it read a bank statement or a screenshot into the import list.

It is **off by default**. You bring the model: Claude, OpenAI, or one running on your own machine. Nothing is sent anywhere until an owner turns it on.

> If you already use Claude, ChatGPT or another agent, you may not need this: connect it to Exhale over MCP instead (**AI agents** in the web). The built-in assistant is for asking from inside Exhale and from Telegram.

## What is sent, and where

While it is on, each message you type to it goes to the provider you chose, together with:

- a **briefing** of your household: today's date, the names of the people you share with, the subscriptions **you** can see (name, price, cycle, next charge, payment method, category), the next 30 days of charges and what needs attention;
- what Exhale **looks up** to answer (for example the history of one subscription, including its notes);
- the last few turns of the conversation, for half an hour.

When you import a **PDF, a picture or pasted text**, that file is sent too. When you press **Read dates** on a document attached to a subscription, that document is sent, with the subscription's name. When you import a **bank export (CSV)**, Exhale finds the recurring charges by itself and sends only the merchant descriptions, to tidy the names: no amounts, no dates.

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

**Settings → Import.**

| What | Needs the assistant | How it is read |
|---|---|---|
| Bank export of transactions (CSV) | No | Exhale finds charges that repeat on a regular cycle. Two or more months of data work best |
| Statement (PDF) | Yes | The model reads it |
| Screenshot or photo (PNG, JPEG, WebP) | Yes | The model reads it: a subscriptions page in your bank's app, an App Store list, an email |
| Pasted text | Yes | The model reads it |

Each line of the list says what it is based on ("6 charges every month, last on 2026-11-06"). Tick what to import. Nothing is saved before that, and the file itself is never stored.

Limits: pictures up to 5 MB, PDFs up to 20 MB, text up to 200 KB. A PDF needs a model that accepts files (Claude and OpenAI do; most local models do not: export a CSV or upload a screenshot instead).

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
