// Checks the catalog of every registered language (src/locales/<code>.json;
// languages are registered in internal/i18n/languages.json). It fails when
// a text passed to t()/tn() has no translation in a complete language, when
// a translation is no longer used, or when a translation takes different
// arguments than its English text. An incomplete language only gets the
// list of what is left. Run with `pnpm i18n`; `pnpm i18n --update` gives
// each incomplete language an empty entry per missing text and drops its
// unused ones. The server's texts are checked by the Go tests
// (go test ./internal/actions), which write src/locales/server/<code>.json.
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { keys, plurals } from "./i18n-keys.mjs";

const read = (url) => JSON.parse(readFileSync(url, "utf8"));
const languages = read(new URL("../../internal/i18n/languages.json", import.meta.url));
const update = process.argv.includes("--update");

const used = keys();
const pairs = plurals(); // singular → plural
const pluralKeys = new Set(pairs.values());
const args = (s) =>
	[...s.matchAll(/\{(\d+|n)\}/g)]
		.map((m) => m[1])
		.sort()
		.join(",");
const empty = (e) => (typeof e === "object" && e ? Object.values(e).every((s) => !s) : !e);

let failed = false;
for (const l of languages) {
	if (l.code === "en") continue;
	const file = new URL(`../src/locales/${l.code}.json`, import.meta.url);
	const path = `src/locales/${l.code}.json`;
	const cat = existsSync(file) ? read(file) : {};
	const categories = new Intl.PluralRules(l.code).resolvedOptions().pluralCategories;
	const missing = [];
	const stale = [];
	const problems = [];
	for (const k of used) {
		if (!empty(cat[k])) continue;
		// The singular is not needed when the plural gives every form, or
		// when the language has no singular.
		const many = pairs.get(k);
		if (many !== undefined && many !== k && (typeof cat[many] === "object" || !categories.includes("one")))
			continue;
		missing.push(k);
	}
	for (const [k, e] of Object.entries(cat)) {
		if (!used.includes(k)) {
			stale.push(k);
			continue;
		}
		if (typeof e === "string") {
			if (e && args(k) !== args(e)) problems.push(`arguments differ: ${JSON.stringify(k)} → ${JSON.stringify(e)}`);
			continue;
		}
		if (!pluralKeys.has(k)) problems.push(`${JSON.stringify(k)} is not the plural of a tn(): give one translation`);
		if (!e.other && !empty(e)) problems.push(`${JSON.stringify(k)}: plural forms need an "other" form`);
		for (const [c, s] of Object.entries(e)) {
			if (!categories.includes(c))
				problems.push(`${JSON.stringify(k)}: ${l.code} has no plural form "${c}" (it uses ${categories.join(", ")})`);
			if (s && args(k) !== args(s)) problems.push(`arguments differ: ${JSON.stringify(k)} → ${c} ${JSON.stringify(s)}`);
		}
	}

	if (problems.length) {
		failed = true;
		console.error(`${path}:\n  ${problems.join("\n  ")}`);
	}
	if (update && !l.complete) {
		const out = {};
		for (const [k, e] of Object.entries(cat)) if (!stale.includes(k)) out[k] = e;
		for (const k of missing) if (!(k in out)) out[k] = "";
		writeFileSync(file, `${JSON.stringify(out, null, "\t")}\n`);
		console.log(`${path}: ${missing.length} to translate`);
		continue;
	}
	const list = (title, ks) => ks.length && `${title}:\n  ${ks.map((k) => JSON.stringify(k)).join("\n  ")}`;
	const report = [list(`no ${l.name} translation in ${path}`, missing), list(`translations in ${path} no longer used`, stale)].filter(Boolean);
	if (!report.length) {
		console.log(`${l.name}: ${used.length} texts, all translated`);
	} else if (l.complete) {
		failed = true;
		console.error(report.join("\n"));
	} else {
		console.log(report.join("\n"));
		console.log(`${l.name} is incomplete: ${used.length - missing.length} of ${used.length} texts translated (pnpm i18n --update fills ${path})`);
	}
}
if (failed) process.exit(1);
