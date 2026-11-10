# Many subscriptions: search, tags, bulk changes

Tools for when the list gets long. Everything here is also an operation for AI agents (MCP and the built-in assistant), so you can ask for it in words.

## Search and filters

The search box on **Subscriptions** looks in the name, the notes, the account, the category and the tags. Every word must match, so `work figma` finds Figma tagged *work*. It keeps the other filters (status, category, payment method, tag), and all of them live in the address, so a reload or a shared link shows the same list.

Agents use `list_subscriptions` with `query` (the same search) and `tag`.

## Tags

A tag is a free label: *work*, *family*, *to review*, a client's name. A subscription can have many; a category stays one per subscription, so totals by category never count anything twice.

- **Add them** in the form: type and press Enter or a comma. Tags you already use are suggested.
- **See them** as small labels in the list and on the subscription page; filter the list by one.
- **Rename or delete one everywhere** in **Settings → General → Tags**. Renaming to a tag that already exists merges the two. Both can be undone from the message that appears, or from **Activity**.
- **In chat** (Telegram and Slack): open a subscription, **✏️ Edit → 🔖 Tags**, and type them separated by commas. `-` removes them all.
- Case does not matter (*Work* and *work* are the same tag). Commas and semicolons are not allowed in a tag: they separate tags in CSV files and in chat. At most 20 tags of 40 characters each.

Tags travel in the JSON and CSV exports (column `tags`, separated by commas) and come back in on import.

## Duplicate

**Duplicate** on a subscription's page opens the form filled in with the same terms and the name "… (copy)". Change what differs and save: it is a new subscription with no charges and no history.

## Change many at once

Tick subscriptions in the list (the box in the header ticks every one shown). A bar appears with **Category**, **Payment method**, **Tags**, **Pause**, **Resume**, **Cancel** and **Delete**.

Each opens a preview first: what changes for each one, and which ones are already as asked. Nothing is saved until you press the button. The change is all or nothing, each subscription gets its own entry in **Activity**, and the **Undo** in the message that follows reverts the whole change at once.

Cancelling or deleting here only stops Exhale tracking them: cancel them with the provider too.

Agents use `bulk_update_subscriptions` (always with `dryRun` first) and pass the `auditIds` it returns to `undo` to revert it.

## "Also offered at": what switching saves

Many services sell the same plan monthly and yearly. In the form, under **More details → Also offered at**, write the other price and whether it is per month or per year. Only then Exhale shows:

- on the subscription page, how much a year switching would save (or how much more it would cost);
- in **Check-up** and **Insights**, the subscriptions where the other price is cheaper, with the total.

Exhale never guesses prices: without a price you wrote, there is no hint. It is in the exports as `other price` and `other price per`.
