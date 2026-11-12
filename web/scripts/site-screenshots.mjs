// The screenshots on the website and in the README (site/img), taken from the
// current code: a fresh server with demo data, desktop and phone, light and
// dark. Run through `mise run screenshots` (scripts/screenshots.sh), which
// builds and starts that server. Dates are relative to today, so the pictures
// never show a stale month.
//
// Usage: node scripts/site-screenshots.mjs <baseURL> <outDir>
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright";

const [base, out] = process.argv.slice(2);
if (!base || !out) throw new Error("usage: node scripts/site-screenshots.mjs <baseURL> <outDir>");

const day = (n) => new Date(Date.now() + n * 864e5).toISOString().slice(0, 10);
// The last day of the month after next: a card that expires soon.
const soon = (() => {
	const d = new Date();
	return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 2, 0)).toISOString().slice(0, 10);
})();

const browser = await chromium.launch();

// ---- demo data ----

const setup = await browser.newContext();
const api = async (method, path, data) => {
	const res = await setup.request.fetch(base + "/api/v1" + path, { method, data, headers: { origin: base } });
	if (!res.ok()) throw new Error(`${method} ${path}: ${res.status()} ${await res.text()}`);
	return res.json();
};
await api("POST", "/auth/setup", {
	name: "Alex", email: "alex@example.com", password: "a long demo password",
	householdName: "Home", currency: "EUR", timezone: "Europe/Madrid", locale: "en",
});
const method = async (m) => (await api("POST", "/payment-methods", m)).paymentMethod.id;
const revolut = await method({ name: "Revolut", kind: "card", last4: "0937" });
const visa = await method({ name: "Visa", kind: "card", last4: "4421", expiresOn: soon });
const paypal = await method({ name: "PayPal", kind: "paypal" });
const categories = Object.fromEntries((await api("GET", "/household")).categories.map((c) => [c.name, c.id]));

const subscriptions = [
	["Netflix Premium", "19.99", "EUR", 1, "month", 0, "Streaming", visa],
	["Spotify Family", "17.99", "EUR", 1, "month", -4, "Music", revolut],
	["iCloud+ 2 TB", "9.99", "EUR", 1, "month", 2, "Cloud & Dev", revolut],
	["GitHub Copilot", "10.00", "USD", 1, "month", 8, "Software", visa],
	["ChatGPT Plus", "20.00", "USD", 1, "month", 21, "AI", visa],
	["Amazon Prime", "49.90", "EUR", 1, "year", 10, "Streaming", revolut],
	["Hetzner Cloud", "4.51", "EUR", 1, "month", 13, "Cloud & Dev", paypal],
	["Basic-Fit", "29.99", "EUR", 4, "week", 5, "Health & Fitness", revolut],
	["1Password Families", "59.88", "USD", 1, "year", 52, "Software", visa],
];
const ids = {};
for (const [name, price, currency, interval, unit, first, category, paymentMethodId] of subscriptions) {
	const s = await api("POST", "/subscriptions", {
		name, price, currency, interval, unit, firstCharge: day(first), categoryId: categories[category], paymentMethodId,
	});
	ids[name] = s.subscription.id;
}
// A charge that was due nine days ago and is still unconfirmed.
const gpt = await api("GET", `/subscriptions/${ids["ChatGPT Plus"]}`);
await api("POST", "/charges/move", { chargeId: gpt.upcoming[0].id, newDate: day(-9), scope: "this" });
const session = await setup.storageState();

// ---- pictures ----

const shots = [
	["app", { width: 1440, height: 900 }, { width: 2000, height: 1250 }],
	["phone", { width: 390, height: 844 }, { width: 780, height: 1688 }],
];
for (const theme of ["light", "dark"]) {
	for (const [name, viewport, size] of shots) {
		const phone = name === "phone";
		const ctx = await browser.newContext({
			viewport, deviceScaleFactor: 2, colorScheme: theme, reducedMotion: "reduce",
			isMobile: phone, hasTouch: phone, locale: "en-US", storageState: session,
		});
		await ctx.addInitScript((t) => {
			localStorage.setItem("theme", t);
			localStorage.setItem("lang", "en");
			localStorage.setItem("exhale.getStarted.hidden", "1");
		}, theme);
		const page = await ctx.newPage();
		await page.goto(base + "/", { waitUntil: "networkidle" });
		await page.evaluate(() => document.fonts.ready);
		await page.waitForTimeout(800);
		const png = await page.screenshot();
		// The browser encodes WebP itself, so no image tool is needed.
		const webp = await page.evaluate(async ({ src, width, height }) => {
			const img = new Image();
			img.src = src;
			await img.decode();
			const canvas = document.createElement("canvas");
			canvas.width = width;
			canvas.height = height;
			const g = canvas.getContext("2d");
			g.imageSmoothingQuality = "high";
			g.drawImage(img, 0, 0, width, height);
			return canvas.toDataURL("image/webp", 0.82).split(",")[1];
		}, { src: "data:image/png;base64," + png.toString("base64"), ...size });
		const file = join(out, `${name}-${theme}.webp`);
		writeFileSync(file, Buffer.from(webp, "base64"));
		console.log("wrote", file);
		await ctx.close();
	}
}
await browser.close();
