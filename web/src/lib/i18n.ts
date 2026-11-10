// The web UI is written in English and translated here, like the chat
// messages (ADR 0023): the t function looks the English text up in the catalog of
// the chosen language and falls back to English. `pnpm i18n` fails when a
// text has no translation.
import { es } from "@/lib/es";

export type Lang = "en" | "es";

const catalogs: Record<Lang, Record<string, string>> = { en: {}, es };

function detect(): Lang {
	try {
		const saved = localStorage.getItem("lang");
		if (saved === "en" || saved === "es") return saved;
	} catch {}
	return typeof navigator !== "undefined" && navigator.language.toLowerCase().startsWith("es") ? "es" : "en";
}

/** The language of this browser: the saved choice, else the browser's own. */
export const lang: Lang = detect();

/** Locale for dates and amounts: the browser's, if it matches the language. */
export const locale =
	typeof navigator !== "undefined" && navigator.language.toLowerCase().startsWith(lang)
		? navigator.language
		: lang;

/** Saves the language and reloads, so every text is rendered again. */
export function setLang(l: Lang) {
	try {
		localStorage.setItem("lang", l);
	} catch {}
	window.location.reload();
}

type Arg = string | number | null | undefined;

/** Translates text; {0}, {1}… are replaced by the arguments. */
export function t(text: string, ...args: Arg[]): string {
	const s = catalogs[lang][text] ?? text;
	return args.length ? s.replace(/\{(\d+)\}/g, (_, i) => String(args[Number(i)] ?? "")) : s;
}

/** Picks the singular or plural text by count; {n} is the count. */
export function tn(n: number, one: string, many: string, ...args: Arg[]): string {
	return t(n === 1 ? one : many, ...args).replace(/\{n\}/g, String(n));
}
