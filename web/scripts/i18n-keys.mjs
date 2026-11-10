// Lists every text passed to t() or tn() in src/, for the i18n check.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const files = [];
const walk = (dir) => {
	for (const name of readdirSync(dir)) {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) {
			if (name !== "gen") walk(p);
		} else if (/\.(ts|tsx)$/.test(name) && name !== "es.ts") files.push(p);
	}
};
walk(new URL("../src", import.meta.url).pathname);

const str = String.raw`"((?:[^"\\]|\\.)*)"`;
export function keys() {
	const out = new Set();
	for (const f of files) {
		const src = readFileSync(f, "utf8");
		for (const m of src.matchAll(new RegExp(String.raw`\bt\(\s*${str}`, "g"))) out.add(JSON.parse(`"${m[1]}"`));
		for (const m of src.matchAll(new RegExp(String.raw`\btn\(\s*[^,]+?,\s*${str},\s*${str}`, "g"))) {
			out.add(JSON.parse(`"${m[1]}"`));
			out.add(JSON.parse(`"${m[2]}"`));
		}
	}
	return [...out];
}

if (process.argv[1] === new URL(import.meta.url).pathname) console.log(keys().map((k) => JSON.stringify(k)).join("\n"));
