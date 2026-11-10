// End-to-end smoke test: a real browser against a real server with an empty
// database. Run with `mise run test:e2e` (it builds and starts the server).
import assert from "node:assert/strict";
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

test("attach a contract and an own logo, then delete the contract", async () => {
	await page.getByRole("button", { name: "Attach", exact: true }).click();
	const dialog = page.getByRole("dialog");
	await dialog.getByLabel("File").setInputFiles({
		name: "contrato.pdf",
		mimeType: "application/pdf",
		buffer: Buffer.from("%PDF-1.7\n%%EOF\n"),
	});
	await dialog.getByRole("button", { name: "Attach", exact: true }).click();
	await page.getByText("Document attached").waitFor();
	await page.getByRole("link", { name: "contrato.pdf" }).waitFor();
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==";
	await page
		.locator('input[type=file][accept="image/png,image/jpeg,image/webp"]')
		.setInputFiles({ name: "logo.png", mimeType: "image/png", buffer: Buffer.from(png, "base64") });
	await page.getByText("Logo updated").waitFor();
	await page.locator('img[src*="/logo?v="]').first().waitFor();
	await page.getByRole("button", { name: "Delete contrato.pdf" }).click();
	await page.getByText("No documents yet.").waitFor();
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
	for (const path of [
		"/api/v1/export/subscriptions.csv",
		"/api/v1/export/charges.csv",
		"/api/v1/export",
		"/api/v1/export/archive.zip",
	]) {
		const res = await page.request.get(`${base}${path}`);
		assert.equal(res.status(), 200, path);
		// The archive is compressed: it names its files in clear.
		const want = path.endsWith(".zip") ? /exhale-export\.json[\s\S]*-logo\.png|-logo\.png[\s\S]*exhale-export\.json/ : /Netflix/;
		assert.match((await res.body()).toString("latin1"), want, path);
	}
});

test("no JavaScript errors along the way", () => {
	assert.deepEqual(errors, []);
});
