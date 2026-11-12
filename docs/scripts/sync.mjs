// Copies the Markdown that lives in docs/ (read on GitHub as it is) into
// Starlight's content folder, so the text is written once and published twice.
//
// For each page it:
// - takes the title from the first "# " line (Starlight wants it in frontmatter)
//   and the description from the first paragraph;
// - rewrites relative links: to another published page they become site URLs,
//   to anything else in the repository a link to it on GitHub;
// - points "Edit page" at the source file, not at this copy.
//
// astro.config.mjs runs it before every build and keeps the copy up to date
// while `astro dev` runs; `node scripts/sync.mjs` runs it alone.

import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, watch, writeFileSync } from "node:fs";
import { dirname, join, posix, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const docs = resolve(here, ".."); // docs/
const repo = resolve(docs, ".."); // repository root
const out = join(docs, "src/content/docs");

export const base = "/exhale/docs";
const github = "https://github.com/akamachinecode/exhale";

// Source (relative to docs/) → page slug. A README is the index of its folder.
const sources = ["vision.md", "install", "guides", "adr"];
// Files published as they are, next to the pages.
const assets = ["architecture-diagram.html"];

function list() {
	const pages = new Map();
	for (const s of sources) {
		const abs = join(docs, s);
		const files = s.endsWith(".md")
			? [s]
			: readdir(abs).filter((f) => f.endsWith(".md")).map((f) => posix.join(s, f));
		for (const f of files) {
			const name = posix.basename(f, ".md");
			const dir = posix.dirname(f);
			const slug = name === "README" ? dir : posix.join(dir === "." ? "" : dir, name);
			pages.set(f, slug);
		}
	}
	return pages;
}

function readdir(dir) {
	return existsSync(dir) ? readdirSync(dir) : [];
}

function url(slug, hash = "") {
	return `${base}/${slug}${slug ? "/" : ""}${hash}`;
}

// Rewrites a link target found in `file` (relative to docs/).
function rewrite(target, file, pages) {
	if (/^([a-z]+:|#|\/)/i.test(target)) return target;
	const [path, hash = ""] = target.split(/(?=#)/);
	const fromRepo = posix.normalize(posix.join("docs", posix.dirname(file), path));
	const inDocs = posix.relative("docs", fromRepo);
	if (pages.has(inDocs)) return url(pages.get(inDocs), hash.toLowerCase());
	if (assets.includes(inDocs)) return `${base}/${inDocs}${hash}`;
	const abs = join(repo, fromRepo);
	if (!existsSync(abs)) throw new Error(`${file}: broken link to ${target}`);
	const kind = statSync(abs).isDirectory() ? "tree" : "blob";
	return `${github}/${kind}/main/${fromRepo}${hash}`;
}

function convert(file, pages) {
	const src = readFileSync(join(docs, file), "utf8").replace(/\r\n/g, "\n");
	const lines = src.split("\n");
	const h1 = lines.findIndex((l) => l.startsWith("# "));
	if (h1 < 0) throw new Error(`${file}: no "# Title" line`);
	const title = lines[h1].slice(2).trim();
	const body = lines.slice(h1 + 1).join("\n").replace(/^\n+/, "");

	// Rewrite links outside fenced code.
	let fenced = false;
	const text = body
		.split("\n")
		.map((line) => {
			if (/^\s*(```|~~~)/.test(line)) fenced = !fenced;
			if (fenced) return line;
			return line.replace(/(\]\()([^)\s]+)(\))/g, (_, a, t, b) => a + rewrite(t, file, pages) + b);
		})
		.join("\n");

	const frontmatter = {
		title,
		description: describe(body),
		editUrl: `${github}/edit/main/docs/${file}`,
		sidebar: sidebar(file, title),
	};
	const yaml = Object.entries(frontmatter)
		.filter(([, v]) => v)
		.map(([k, v]) => `${k}: ${JSON.stringify(v)}`)
		.join("\n");
	return `---\n${yaml}\n---\n\n<!-- Generated from docs/${file} by scripts/sync.mjs. Edit that file. -->\n\n${text}`;
}

// ADRs are listed by number and the short name of the decision
// ("0001 Distribution"); their index is linked from the sidebar by hand.
function sidebar(file, title) {
	if (!file.startsWith("adr/")) return undefined;
	if (file === "adr/README.md") return { hidden: true };
	const m = title.match(/^(\d+)\.\s*([^:—(]+)/);
	return m ? { label: `${m[1]} ${m[2].trim()}` } : undefined;
}

// The first paragraph of prose, as plain text, for search engines and llms.txt.
function describe(body) {
	const para = body
		.split(/\n\s*\n/)
		.find((p) => /^[A-Za-z*_[]/.test(p.trim()) && !p.trim().startsWith("- "));
	if (!para) return "";
	const plain = para
		.replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
		.replace(/[*_`]/g, "")
		.replace(/\s+/g, " ")
		.trim();
	if (plain.length <= 200) return plain;
	const cut = plain.slice(0, 200);
	return `${cut.slice(0, cut.lastIndexOf(" "))}…`;
}

export function sync() {
	const pages = list();
	// Remove only what an earlier run generated; index.mdx and other pages
	// written for the site live next to them.
	for (const dir of ["install", "guides", "adr"]) rmSync(join(out, dir), { recursive: true, force: true });
	rmSync(join(out, "vision.md"), { force: true });
	for (const [file, slug] of pages) {
		const index = posix.basename(file) === "README.md";
		const dest = join(out, `${slug}${index ? "/index" : ""}.md`);
		mkdirSync(dirname(dest), { recursive: true });
		writeFileSync(dest, convert(file, pages));
	}
	// The API reference: web/openapi.json (kept current by CI), with the
	// released version instead of the "dev" of a development build.
	const api = JSON.parse(readFileSync(join(repo, "web/openapi.json"), "utf8"));
	api.servers ??= [{ url: "http://127.0.0.1:8484", description: "Exhale on this computer" }];
	api.info.version = JSON.parse(readFileSync(join(repo, ".release-please-manifest.json"), "utf8"))["."];
	writeFileSync(join(docs, "src/openapi.json"), `${JSON.stringify(api, null, "\t")}\n`);

	for (const a of assets) {
		mkdirSync(join(docs, "public"), { recursive: true });
		writeFileSync(join(docs, "public", a), readFileSync(join(docs, a)));
	}
	return pages;
}

// Copies again whenever a source changes.
export function watchSources(onChange = () => {}) {
	let timer;
	for (const s of sources) {
		watch(join(docs, s), { recursive: true }, () => {
			clearTimeout(timer);
			timer = setTimeout(() => {
				try {
					sync();
					onChange();
				} catch (e) {
					console.error(e.message);
				}
			}, 100);
		});
	}
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const n = sync().size;
	console.log(`docs: ${n} pages from docs/ → ${relative(repo, out)}`);
}
