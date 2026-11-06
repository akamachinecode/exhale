import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Money } from "@/gen/api";

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

const locale = typeof navigator === "undefined" ? "en" : navigator.language;

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
	if (days === 0) return "today";
	if (days === 1) return "tomorrow";
	if (days === -1) return "yesterday";
	return days > 0 ? `in ${days} days` : `${-days} days ago`;
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
