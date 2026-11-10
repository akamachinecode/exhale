// The web UI is written in English and translated here, like the chat
// messages (ADR 0023): t() looks the English text up in the catalog of the
// chosen language and falls back to English. Languages are registered in
// internal/i18n/languages.json (shared with the server); each one has
// src/locales/<code>.json, and src/locales/server/<code>.json with the
// server's texts (written by go test ./internal/actions -update). Only the
// chosen language is downloaded. `pnpm i18n` checks every catalog.
import registry from "../../../internal/i18n/languages.json";

/** A registered language: its code and its own name. */
export type Language = { code: string; name: string; complete?: boolean };

/** Every language Exhale speaks, English first. */
export const languages: readonly Language[] = registry;

export type Lang = string;

/** A translation: a string, or one string per plural category. */
type Entry = string | Partial<Record<Intl.LDMLPluralRule, string>>;

const webCatalogs = import.meta.glob<Record<string, Entry>>("../locales/*.json", { import: "default" });
const serverCatalogs = import.meta.glob<Record<string, string>>("../locales/server/*.json", {
	import: "default",
});

const supported = (code: string | null | undefined): code is string =>
	!!code && languages.some((l) => l.code === code);

/** The first registered language among codes such as "pt-BR", or English. */
export function matchLanguage(codes: readonly (string | null | undefined)[]): Lang {
	for (const c of codes) {
		const base = c?.toLowerCase().split(/[-_]/)[0];
		if (supported(base)) return base;
	}
	return "en";
}

function detect(): Lang {
	try {
		const saved = localStorage.getItem("lang");
		if (supported(saved)) return saved;
	} catch {}
	// What the browser sends as Accept-Language, in order.
	return typeof navigator === "undefined"
		? "en"
		: matchLanguage(navigator.languages?.length ? navigator.languages : [navigator.language]);
}

/** The language of this page: the person's (once signed in, see
 * syncLanguage), else the one chosen in this browser, else the browser's. */
export const lang: Lang = detect();

/** Locale for dates and amounts: the browser's, if it matches the language. */
export const locale =
	typeof navigator !== "undefined" && navigator.language.toLowerCase().startsWith(lang)
		? navigator.language
		: lang;

async function load(l: Lang): Promise<Record<string, Entry>> {
	if (l === "en") return {};
	try {
		const [server, web] = await Promise.all([
			serverCatalogs[`../locales/server/${l}.json`]?.() ?? {},
			webCatalogs[`../locales/${l}.json`]?.() ?? {},
		]);
		// A text the web uses itself may be worded for its place on screen.
		return { ...server, ...web };
	} catch {
		return {}; // English rather than nothing
	}
}

const catalog = await load(lang);
const plurals = new Intl.PluralRules(lang);

/** Saves the language in this browser and reloads, so every text is
 * rendered again. */
export function setLang(l: Lang) {
	try {
		localStorage.setItem("lang", l);
	} catch {}
	window.location.reload();
}

/** Switches to the language the server chose for the signed-in person,
 * if it is not the one on screen. */
export function syncLanguage(l: Lang | null | undefined) {
	if (supported(l) && l !== lang) setLang(l);
}

type Arg = string | number | null | undefined;

const text = (e: Entry | undefined): string | undefined =>
	typeof e === "string" ? e || undefined : e?.other || undefined;

function fill(s: string, args: Arg[]): string {
	return args.length ? s.replace(/\{(\d+)\}/g, (_, i) => String(args[Number(i)] ?? "")) : s;
}

/** Translates text; {0}, {1}… are replaced by the arguments. */
export function t(key: string, ...args: Arg[]): string {
	return fill(text(catalog[key]) ?? key, args);
}

/** Picks the singular or plural text by count, with the plural rules of
 * the language; {n} is the count. A translation may give the plural as one
 * text per category ({"one": …, "few": …, "many": …, "other": …}). */
export function tn(n: number, one: string, many: string, ...args: Arg[]): string {
	const forms = catalog[many];
	let s: string | undefined;
	if (typeof forms === "object") {
		s = forms[plurals.select(n)] || forms.other;
	} else if (text(catalog[one]) || text(forms)) {
		s = plurals.select(n) === "one" ? text(catalog[one]) : text(forms);
	}
	// Not translated: the English text, counted in English.
	s ??= n === 1 ? one : many;
	return fill(s, args).replace(/\{n\}/g, String(n));
}

/** A sentence written by the server: an English key with {0}… and its
 * arguments, each a formatted value or another sentence (ADR 0023). */
export type ServerText = { key: string; args?: readonly unknown[] | null };

/** Renders a sentence of the server in this browser's language. */
export function say(text: ServerText | null | undefined): string {
	if (!text) return "";
	const args = (text.args ?? []).map((a) => (typeof a === "string" ? a : say(a as ServerText)));
	return t(text.key, ...args);
}

/** Upper-cases the first letter, for a sentence shown alone. */
export function capitalize(s: string): string {
	return s.charAt(0).toUpperCase() + s.slice(1);
}
