// Lists every text passed to t() or tn() in src/, for the i18n check.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const files = [];
const walk = (dir) => {
	for (const name of readdirSync(dir)) {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) {
			if (name !== "gen") walk(p);
		} else if (/\.(ts|tsx)$/.test(name)) files.push(p);
	}
};
walk(new URL("../src", import.meta.url).pathname);

const str = String.raw`"((?:[^"\\]|\\.)*)"`;
/** Every text passed to t() or tn(). */
export function keys() {
	return [...scan().keys];
}

/** For each singular passed to tn(), its plural. */
export function plurals() {
	return scan().plurals;
}

function scan() {
	const out = new Set();
	const plurals = new Map();
	for (const f of files) {
		const src = readFileSync(f, "utf8");
		for (const m of src.matchAll(new RegExp(String.raw`\bt\(\s*${str}`, "g"))) out.add(JSON.parse(`"${m[1]}"`));
		for (const m of src.matchAll(new RegExp(String.raw`\btn\(\s*[^,]+?,\s*${str},\s*${str}`, "g"))) {
			const one = JSON.parse(`"${m[1]}"`);
			const many = JSON.parse(`"${m[2]}"`);
			out.add(one);
			out.add(many);
			plurals.set(one, many);
		}
	}
	return { keys: out, plurals };
}

if (process.argv[1] === new URL(import.meta.url).pathname) console.log(keys().map((k) => JSON.stringify(k)).join("\n"));
