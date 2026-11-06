// Captures the fixed reference screenshots once (.ui-climb/anchors). Never re-run.
import { chromium } from "playwright";
const out = process.argv[2];
const refs = [
	["linear", "https://linear.app"],
	["copilot-money", "https://www.copilot.money"],
	["stripe-billing", "https://stripe.com/billing"],
];
const browser = await chromium.launch();
for (const [name, url] of refs) {
	const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
	try {
		await page.goto(url, { waitUntil: "load", timeout: 45000 });
		await page.waitForTimeout(2500);
		for (const label of ["Accept", "Accept all", "Allow all", "I agree", "OK"]) {
			const b = page.getByRole("button", { name: label, exact: false }).first();
			if (await b.isVisible().catch(() => false)) { await b.click().catch(() => {}); break; }
		}
		await page.mouse.wheel(0, 500);
		await page.waitForTimeout(1500);
		await page.screenshot({ path: `${out}/${name}.png` });
		console.log("ok", name);
	} catch (e) {
		console.log("fail", name, e.message.split("\n")[0]);
	}
	await page.close();
}
await browser.close();
