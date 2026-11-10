import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Money } from "@/gen/api";
import { locale, t } from "@/lib/i18n";

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

function fractionDigits(currency: string) {
	return (
		new Intl.NumberFormat("en", { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2
	);
}

/** Formats an exact amount (integer minor units + ISO currency). */
export function money(m?: Money | null, opts: { compact?: boolean } = {}) {
	if (!m) return "—";
	const digits = fractionDigits(m.currency);
	const value = m.minor / 10 ** digits;
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency: m.currency,
		minimumFractionDigits: opts.compact && Number.isInteger(value) ? 0 : digits,
	}).format(value);
}

/** Decimal string for inputs ("12.99"). */
export function decimal(m: Money) {
	const digits = fractionDigits(m.currency);
	return (m.minor / 10 ** digits).toFixed(digits);
}

/** Civil dates (YYYY-MM-DD) are formatted without time zones. */
export function date(d?: string | null, style: "short" | "long" | "day" = "short") {
	if (!d) return "—";
	const [y, mo, da] = d.split("-").map(Number);
	const dt = new Date(Date.UTC(y, mo - 1, da, 12));
	const opts: Intl.DateTimeFormatOptions =
		style === "long"
			? { weekday: "short", day: "numeric", month: "short", year: "numeric", timeZone: "UTC" }
			: style === "day"
				? { weekday: "short", day: "numeric", month: "short", timeZone: "UTC" }
				: { day: "numeric", month: "short", timeZone: "UTC" };
	return new Intl.DateTimeFormat(locale, opts).format(dt);
}

export function relativeDays(days: number) {
	if (days === 0) return t("today");
	if (days === 1) return t("tomorrow");
	if (days === -1) return t("yesterday");
	return days > 0 ? t("in {0} days", days) : t("{0} days ago", -days);
}

const unitNames: Record<string, [string, string]> = {
	day: [t("Day"), t("Days")],
	week: [t("Week"), t("Weeks")],
	month: [t("Month"), t("Months")],
	year: [t("Year"), t("Years")],
};

/** "Month" or "Months" for a billing unit. */
export function unitLabel(unit: string, many = false) {
	return unitNames[unit]?.[many ? 1 : 0] ?? unit;
}

const everyOne: Record<string, string> = {
	day: t("every day"),
	week: t("every week"),
	month: t("every month"),
	year: t("every year"),
};
const everyMany: Record<string, (n: number) => string> = {
	day: (n) => t("every {0} days", n),
	week: (n) => t("every {0} weeks", n),
	month: (n) => t("every {0} months", n),
	year: (n) => t("every {0} years", n),
};

/** A billing cycle in words: "every month", "every 3 months". */
export function every(interval: number, unit: string) {
	return interval > 1 ? (everyMany[unit]?.(interval) ?? `${interval} ${unit}`) : (everyOne[unit] ?? unit);
}

const cadences: Record<string, string> = {
	daily: t("daily"),
	weekly: t("weekly"),
	monthly: t("monthly"),
	yearly: t("yearly"),
};

/** Translates the server's description of a billing cycle ("monthly", "every 3 months"). */
export function cadenceText(cadence: string) {
	const m = /^every (\d+) (day|week|month|year)s$/.exec(cadence);
	return m ? every(Number(m[1]), m[2]) : (cadences[cadence] ?? cadence);
}

// --- billing rules: weekday of the month, weekend shift ---------------------

export type DayRule = "day" | "weekday" | "last-weekday";
export type WeekendShift = "none" | "next" | "previous";

/**
 * What a civil date offers as a billing day: its day number, its week of the
 * month (1–4; 0 for a fifth weekday) and whether it is the last such weekday.
 * This only describes the date; the engine computes the charges.
 */
export function dayFacts(d: string) {
	const [y, mo, da] = d.split("-").map(Number);
	const daysInMonth = new Date(Date.UTC(y, mo, 0)).getUTCDate();
	const weekday = new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" }).format(
		new Date(Date.UTC(y, mo - 1, da, 12)),
	);
	return { day: da, week: da <= 28 ? Math.ceil(da / 7) : 0, last: da + 7 > daysInMonth, weekday };
}

/** "Day 15", "The third Wednesday", "The last Friday", for a rule anchored on d. */
export function billingDayText(rule: string | undefined, d: string) {
	const f = dayFacts(d);
	if (rule === "last-weekday") return t("The last {0}", f.weekday);
	if (rule === "weekday") {
		if (f.week === 1) return t("The first {0}", f.weekday);
		if (f.week === 2) return t("The second {0}", f.weekday);
		if (f.week === 3) return t("The third {0}", f.weekday);
		if (f.week === 4) return t("The fourth {0}", f.weekday);
	}
	return t("Day {0}", f.day);
}

/** What happens to a charge that falls on a weekend. */
export function weekendText(shift: string | undefined) {
	if (shift === "next") return t("Moved to the next business day");
	if (shift === "previous") return t("Moved to the previous business day");
	return t("Kept on the weekend");
}

export function todayISO() {
	const d = new Date();
	return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export function addDaysISO(iso: string, n: number) {
	const [y, m, d] = iso.split("-").map(Number);
	const dt = new Date(Date.UTC(y, m - 1, d + n));
	return dt.toISOString().slice(0, 10);
}

/** A stable hue per service name for monogram tiles. */
export function hue(name: string) {
	let h = 0;
	for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
	return h;
}

/** Converts for display using the summary's ECB rates (units of main per unit). */
export function toMain(m: Money, rates: Record<string, string> | undefined, main: string): Money | undefined {
	if (m.currency === main) return m;
	const r = rates?.[m.currency];
	if (!r) return undefined;
	const from = 10 ** fractionDigits(m.currency);
	const to = 10 ** fractionDigits(main);
	return { currency: main, minor: Math.round((m.minor / from) * Number(r) * to) };
}

/** Sums amounts into the main currency; returns undefined if a rate is missing. */
export function sumMain(
	items: Money[],
	rates: Record<string, string> | undefined,
	main: string,
): Money | undefined {
	let minor = 0;
	for (const m of items) {
		const c = toMain(m, rates, main);
		if (!c) return undefined;
		minor += c.minor;
	}
	return { currency: main, minor };
}

/** A categorical palette with well separated hues, assigned by rank. */
export const palette = [182, 262, 28, 85, 145, 320, 222, 52].map((h) => `oklch(0.66 0.13 ${h})`);
