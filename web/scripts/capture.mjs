// Deterministic screenshots for the UI hill climb (.ui-climb/CLIMB.md).
// Usage: node scripts/capture.mjs <outDir> [baseURL] [--set working|holdout|all]
import { mkdirSync } from "node:fs";
import { chromium } from "playwright";

const [outDir, base = "http://127.0.0.1:18484", , set = "working"] = process.argv.slice(2);
mkdirSync(outDir, { recursive: true });

const api = async (p) => (await fetch(base + p)).json();
const subs = (await api("/api/v1/subscriptions")).subscriptions;
const netflix = subs.find((s) => s.name.startsWith("Netflix"))?.id;

const shots = {
	working: [
		["overview-1440-light", "/", 1440, "light"],
		["overview-1440-dark", "/", 1440, "dark"],
		["overview-390-light", "/", 390, "light"],
		["subscriptions-1440-light", "/subscriptions", 1440, "light"],
		["detail-1440-dark", `/subscriptions/${netflix}`, 1440, "dark"],
		["calendar-1440-light", "/calendar?month=2026-11", 1440, "light"],
	],
	holdout: [
		["agents-1440-light", "/agents", 1440, "light"],
		["activity-390-dark", "/activity", 390, "dark"],
	],
};
const list = set === "all" ? [...shots.working, ...shots.holdout] : shots[set];

const browser = await chromium.launch();
for (const [name, path, width, theme] of list) {
	const ctx = await browser.newContext({
		viewport: { width, height: width < 600 ? 844 : 900 },
		deviceScaleFactor: 1,
		colorScheme: theme,
		reducedMotion: "reduce",
		locale: "en-GB",
		timezoneId: "Europe/Madrid",
	});
	await ctx.addInitScript((t) => localStorage.setItem("theme", t), theme);
	const page = await ctx.newPage();
	await page.route("**/api/v1/events", (r) => r.abort());
	await page.goto(base + path, { waitUntil: "networkidle" });
	await page.addStyleTag({ content: "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}" });
	// Full-page captures draw fixed bars where the first viewport ends; pin the
	// mobile tab bar to the end of the page instead (what a user sees at the bottom).
	if (width < 768) await page.addStyleTag({ content: "body{position:relative} nav.fixed.bottom-0{position:absolute!important}" });
	await page.evaluate(() => document.fonts.ready);
	await page.waitForTimeout(300);
	await page.screenshot({ path: `${outDir}/${name}.png`, fullPage: true });
	await ctx.close();
	console.log(name);
}
await browser.close();
