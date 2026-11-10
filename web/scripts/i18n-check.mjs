// Fails when a text passed to t()/tn() has no Spanish translation, when a
// translation is no longer used, or when a translation takes different
// arguments than its English text. Run with `pnpm i18n`.
import { readFileSync } from "node:fs";
import { keys } from "./i18n-keys.mjs";

const src = readFileSync(new URL("../src/lib/es.ts", import.meta.url), "utf8");
// The catalog is a plain object literal: evaluate it without the TypeScript.
const es = new Function(`return ${src.slice(src.indexOf("= {") + 2).replace(/;\s*$/, "")}`)();

const used = keys();
const args = (s) => [...s.matchAll(/\{(\d+|n)\}/g)].map((m) => m[1]).sort().join(",");
const problems = [];
for (const k of used) {
	if (!(k in es)) problems.push(`no Spanish translation: ${JSON.stringify(k)}`);
	else if (args(k) !== args(es[k])) problems.push(`arguments differ: ${JSON.stringify(k)} → ${JSON.stringify(es[k])}`);
}
for (const k of Object.keys(es)) if (!used.includes(k)) problems.push(`translation no longer used: ${JSON.stringify(k)}`);

if (problems.length) {
	console.error(problems.join("\n"));
	console.error(`\n${problems.length} problem(s) in src/lib/es.ts`);
	process.exit(1);
}
console.log(`${used.length} texts, all translated`);
