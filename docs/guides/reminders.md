# Reminders that end in an answer

A reminder in Telegram or Slack is meant to end with an answer: paid, keep it, cancel it, or "not now". This page explains what Exhale asks, when it asks again, and what it tells you without being asked. Email, ntfy and webhooks get the same messages as text with a link back to Exhale. Their buttons only work in a chat.

## Remind me later

Every reminder before a charge and every **keep or cancel?** question has two extra buttons:

- **💤 Remind me tomorrow**
- **💤 In 3 days**

The message comes back at your reminder time on that day. It never comes back too late:

- A reminder comes back on the charge date at the latest. If three days would be later than that, the button reads **On the day**.
- A **keep or cancel?** question comes back on the last day to cancel at the latest. If three days would be later, the button reads **On the last day**.
- If a reminder is already planned for that day, you get one message, not two.
- A button that could only come back too late is not shown. On the last day there is nothing left to snooze.

A snooze is yours alone. In Slack it is not posted again to the team channel.

## "Was it charged?" until someone answers

After a charge date Exhale asks **❓ Was it charged?**. If nobody answers, it asks again: by default **every day, up to 3 times**. It stops as soon as someone answers: **Paid**, **Failed**, **Skip**, a new date or another amount.

**⏳ Not yet** means the money has not left yet: the question comes back tomorrow at your reminder time, even after the last repeat.

Change how often it asks in **Settings → General** ("If nobody says whether a charge went through, ask…"), in the chat with **⚙️ Settings → 🔁 Ask again**, or through the API and AI agents (`update_household` with `confirmTimes` and `confirmEveryDays`). Owners can change it. It applies to every subscription still on the previous setting.

If Exhale was down when a repeat was due, that repeat is not sent late. The next one still comes.

## Paid twice?

When the same service shows up in more than one active subscription ("Spotify" and "Spotify Premium", yours and Ana's), the people who pay for them get one message:

> 👯 You and Ana both pay for Spotify
> 💶 2 × €10.99 a month

It has a button to open each subscription and **👍 That's fine**. "That's fine" means it is on purpose: the group leaves the check-up and nobody is told about it again. If a third Spotify joins the group, that is a new group and you hear about it once.

Private subscriptions stay private: you are only told about the subscriptions you can see.

## A card about to expire

Thirty days before a card expires, its owner gets a message with the subscriptions it pays. If the card has no owner, the people whose subscriptions use it get the message.

> 💳 Visa ••4421 expires in 25 days
> It pays 7 subscriptions

**🔀 Move them to another card** asks which card to use and moves them all in one step. **📅 It was renewed: new expiry** updates the date if the bank sent a new card with the same number. The web **Check-up** does the same thing next to each card, and so does a payment method's screen in the chat.

## What cancelling saved

The **Insights** page, the chat summary and the `spending_summary` tool show **You stopped paying X a year**. It adds up the subscriptions you cancelled, at their last price, converted into your display currency. Paused subscriptions do not count. A cancelled subscription whose service is still active under another name (you moved from "Spotify" to "Spotify Family") is a replacement, so it does not count either.
