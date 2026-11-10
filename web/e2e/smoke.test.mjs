// End-to-end smoke test: a real browser against a real server with an empty
// database. Run with `mise run test:e2e` (it builds and starts the server).
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { after, before, test } from "node:test";
import { chromium } from "playwright";

const base = process.env.EXHALE_E2E_URL;
if (!base) throw new Error("EXHALE_E2E_URL is not set: run this through `mise run test:e2e`");

let browser;
let page;
const errors = [];

const iso = (days) => new Date(Date.now() + days * 864e5).toISOString().slice(0, 10);

before(async () => {
	browser = await chromium.launch();
	page = await (await browser.newContext({ viewport: { width: 1280, height: 900 } })).newPage();
	page.on("pageerror", (e) => errors.push(String(e)));
});

after(async () => {
	await browser?.close();
});

test("sign up on a fresh instance", async () => {
	await page.goto(`${base}/`);
	await page.getByText("Welcome to Exhale").waitFor();
	await page.getByRole("textbox", { name: "Name" }).fill("Alex");
	await page.locator("input[type=email]").fill("alex@example.com");
	await page.locator("input[type=password]").fill("a long test password");
	await page.getByRole("button", { name: "Create account" }).click();
	await page.getByText("Welcome, Alex").waitFor();
});

test("add a payment method in Settings", async () => {
	await page.goto(`${base}/settings?tab=general`);
	await page.getByRole("button", { name: "Add a payment method" }).click();
	await page.getByPlaceholder("Revolut, Visa…").fill("Visa");
	await page.getByRole("button", { name: "Add", exact: true }).first().click();
	await page.getByText("Card · 0 subscriptions").waitFor();
});

test("add a subscription with a notice period", async () => {
	await page.goto(`${base}/subscriptions`);
	await page.getByRole("button", { name: "New subscription" }).click();
	await page.getByPlaceholder("Netflix, Spotify Family…").fill("Gym");
	await page.getByPlaceholder("12.99").fill("39");
	await page.getByLabel("Next charge").fill(iso(40));
	await page.getByRole("dialog").getByLabel("Payment method").selectOption({ label: "Visa" });
	await page.getByRole("button", { name: "Needs notice to cancel" }).click();
	await page.getByRole("button", { name: "Add subscription" }).click();
	await page.getByRole("heading", { name: "Gym" }).waitFor();
	await page.getByText("Cancel by").first().waitFor();
});

test("edit it: the price changes from the next charge", async () => {
	await page.getByRole("button", { name: "Edit" }).click();
	await page.getByPlaceholder("12.99").fill("42.50");
	await page.getByRole("button", { name: "Save" }).click();
	await page.getByText("Saved").waitFor();
	await page.getByText(/42[.,]50/).first().waitFor();
});

test("the check-up shows the deadline, and keeping it clears it", async () => {
	await page.goto(`${base}/checkup`);
	await page.getByText("Last days to cancel").waitFor();
	await page.getByRole("button", { name: "Keep it" }).click();
	await page.getByText("Nothing to look at. Breathe out.").waitFor();
});

test("import a spreadsheet through the review list", async () => {
	await page.goto(`${base}/settings`);
	await page.getByRole("tab", { name: "Data" }).click();
	const csv = `Service,Price,Cycle,Next payment,Category\nNetflix,13.99 EUR,Monthly,${iso(5)},Streaming\nGym,42.50,Monthly,${iso(40)},\nBroken,abc,Monthly,${iso(5)},\n`;
	await page.getByLabel("File to import").setInputFiles({ name: "subs.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
	await page.getByText("Spreadsheet (CSV)").waitFor();
	await page.getByText("Problem", { exact: true }).waitFor();
	await page.getByRole("button", { name: "Import 1 subscription" }).click();
	await page.getByText("1 subscription imported").waitFor();
});

test("mark a charge paid from the overview", async () => {
	await page.goto(`${base}/`);
	const row = page.getByRole("listitem").filter({ hasText: "Netflix" }).first();
	await row.hover();
	await row.getByRole("button", { name: "Mark Netflix as paid" }).click();
	await page.getByText("Netflix: marked paid").waitFor();
});

test("insights, calendar and activity render", async () => {
	for (const [path, text] of [
		["/insights", "Month by month"],
		["/calendar", "Mon"],
		["/activity", "Created a subscription".toLowerCase()],
	]) {
		await page.goto(`${base}${path}`);
		await page.getByText(text, { exact: false }).first().waitFor();
	}
	await page.getByRole("tab", { name: "Reminders" }).click();
	await page.getByText(/Scheduled|No reminders yet/).first().waitFor();
});

test("exports download", async () => {
	for (const path of ["/api/v1/export/subscriptions.csv", "/api/v1/export/charges.csv", "/api/v1/export"]) {
		const res = await page.request.get(`${base}${path}`);
		assert.equal(res.status(), 200, path);
		assert.match(await res.text(), /Netflix/, path);
	}
});

// Two-factor sign-in (ADR 0025): what an authenticator app would show.
function totp(secret, offset = 0) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
	let bits = "";
	for (const ch of secret.replace(/=+$/, "")) bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
	const key = Buffer.from(bits.match(/.{8}/g).map((b) => Number.parseInt(b, 2)));
	const msg = Buffer.alloc(8);
	msg.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000) + offset));
	const sum = createHmac("sha1", key).update(msg).digest();
	const off = sum[19] & 0x0f;
	return String((sum.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

async function signInWithCode(code) {
	await page.getByRole("button", { name: "Sign out" }).first().click();
	await page.getByRole("heading", { name: "Sign in" }).waitFor();
	await page.locator("input[type=email]").fill("alex@example.com");
	await page.locator("input[type=password]").fill("a long test password");
	await page.getByRole("button", { name: "Sign in" }).click();
	await page.getByText("Enter your code").waitFor();
	await page.getByLabel("Code").fill(code);
	await page.getByRole("button", { name: "Sign in" }).click();
}

test("two-factor sign-in: turn it on, sign in with a code and a backup code, turn it off", async () => {
	// Codes of the previous and current step are used: stay inside one.
	const left = 30 - (Math.floor(Date.now() / 1000) % 30);
	if (left < 8) await new Promise((r) => setTimeout(r, left * 1000 + 200));
	await page.goto(`${base}/settings`);
	await page.getByRole("button", { name: "Turn on" }).click();
	await page.getByLabel("Your password").fill("a long test password");
	await page.getByRole("button", { name: "Continue" }).click();
	await page.getByRole("img", { name: "QR code for your authenticator app" }).waitFor();
	const secret = (await page.locator("pre").first().textContent()).trim();
	await page.getByLabel("Code from the app").fill(totp(secret, -1));
	await page.getByRole("button", { name: "Turn on" }).click();
	await page.getByText("Save your backup codes").waitFor();
	const backups = (await page.locator("pre").first().textContent()).trim().split("\n");
	assert.equal(backups.length, 10);
	await page.getByRole("button", { name: "I saved them" }).click();
	await page.getByText("10 backup codes left").waitFor();

	await signInWithCode("000000");
	await page.getByText("That code did not work").waitFor();
	await page.getByLabel("Code").fill(totp(secret));
	await page.getByRole("button", { name: "Sign in" }).click();
	await page.getByRole("heading", { name: "Settings" }).waitFor();

	await signInWithCode(backups[0]);
	await page.getByRole("heading", { name: "Settings" }).waitFor();
	await page.getByText("9 backup codes left").waitFor();

	await page.getByRole("button", { name: "Turn off" }).click();
	await page.getByLabel("Your password").fill("a long test password");
	await page.getByLabel("Code or backup code").fill(backups[1]);
	await page.getByRole("button", { name: "Turn off" }).click();
	await page.getByText("Two-factor sign-in is off").first().waitFor();
	await page.getByRole("button", { name: "Turn on" }).waitFor();
});

test("duplicate a subscription with a tag and another price", async () => {
	await page.goto(`${base}/subscriptions`);
	await page.getByRole("link", { name: /^Gym/ }).first().click();
	await page.getByRole("button", { name: "Duplicate" }).click();
	const dialog = page.getByRole("dialog");
	await dialog.getByText("Duplicate Gym").waitFor();
	assert.equal(await dialog.getByPlaceholder("Netflix, Spotify Family…").inputValue(), "Gym (copy)");
	await dialog.getByLabel("Add a tag").fill("home");
	await dialog.getByLabel("Add a tag").press("Enter");
	await dialog.getByLabel("Other price", { exact: true }).fill("400");
	await dialog.getByRole("button", { name: "Add subscription" }).click();
	await page.getByRole("heading", { name: "Gym (copy)" }).waitFor();
	await page.getByText(/Switching to .*400.* per year saves .*110/).waitFor();
	await page.getByText("No past charges yet.").waitFor();
});

test("search finds tags, and the tag filter survives a reload", async () => {
	await page.goto(`${base}/subscriptions?q=home`);
	await page.getByText("1 shown").waitFor();
	await page.getByRole("link", { name: /Gym \(copy\)/ }).waitFor();
	await page.goto(`${base}/subscriptions?tag=home`);
	assert.equal(await page.getByLabel("Filter by tag").inputValue(), "home");
	await page.getByText("1 shown").waitFor();
});

test("bulk edit: tag every subscription, then delete one and undo it", async () => {
	await page.goto(`${base}/subscriptions`);
	await page.getByLabel("Select all shown").check();
	const bar = page.getByRole("toolbar", { name: "Change the selected subscriptions" });
	await bar.getByText("3 selected").waitFor();
	await bar.getByRole("button", { name: "Tags" }).click();
	const dialog = page.getByRole("dialog");
	await dialog.getByLabel("Add a tag").first().fill("family");
	await dialog.getByLabel("Add a tag").first().press("Enter");
	await dialog.getByText("3 will change").waitFor();
	await dialog.getByRole("button", { name: "Change 3 subscriptions" }).click();
	await page.getByText("3 subscriptions changed").waitFor();
	await page.goto(`${base}/subscriptions?tag=family`);
	await page.getByText("3 shown").waitFor();

	await page.getByLabel("Select Netflix").check();
	await bar.getByRole("button", { name: "Delete" }).click();
	await dialog.getByText("Deleted").waitFor();
	await dialog.getByRole("button", { name: "Delete 1 subscription" }).click();
	await page.getByText("2 shown").waitFor();
	await page.getByRole("button", { name: "Undo" }).click();
	await page.getByText("3 shown").waitFor();
	await page.getByRole("link", { name: /Netflix/ }).waitFor();
});

test("a subscription on a weekday of the month, moved off weekends", async () => {
	await page.goto(`${base}/subscriptions`);
	await page.getByRole("button", { name: "New subscription" }).click();
	await page.getByPlaceholder("Netflix, Spotify Family…").fill("Club");
	await page.getByPlaceholder("12.99").fill("12");
	await page.getByLabel("Next charge").fill(iso(40));
	// Every date offers a weekday of the month (the nth or the last).
	const rule = page.getByLabel("Charged on");
	await rule.selectOption({ index: 1 });
	const words = await rule.locator("option:checked").innerText();
	assert.match(words, /^The (first|second|third|fourth|last) /);
	await page.getByLabel("On weekends").selectOption("next");
	await page.getByRole("button", { name: "Add subscription" }).click();
	await page.getByRole("heading", { name: "Club" }).waitFor();
	await page.getByText(words, { exact: true }).waitFor();
	await page.getByText("Moved to the next business day").waitFor();
});

test("no JavaScript errors along the way", () => {
	assert.deepEqual(errors, []);
});
