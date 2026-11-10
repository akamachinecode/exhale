import { lang } from "@/lib/i18n";

// Defaults read from the browser at sign-up, so nobody has to answer
// questions about currency or time zones before seeing the app. All of them
// can be changed later in Preferences.

export const currencies = [
	"EUR",
	"USD",
	"GBP",
	"CHF",
	"MXN",
	"ARS",
	"COP",
	"CLP",
	"BRL",
	"CAD",
	"AUD",
	"JPY",
	"SEK",
	"NOK",
	"DKK",
	"PLN",
];

export function browserTimezone() {
	try {
		return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
	} catch {
		return "UTC";
	}
}

/** The language of this page, the household's first language. */
export function browserLocale() {
	return lang;
}

/** A best guess of the display currency from the browser's region and zone. */
export function guessCurrency() {
	const region = (() => {
		try {
			return new Intl.Locale(navigator.language).maximize().region;
		} catch {
			return undefined;
		}
	})();
	const byRegion: Record<string, string> = {
		GB: "GBP",
		CH: "CHF",
		MX: "MXN",
		AR: "ARS",
		CO: "COP",
		CL: "CLP",
		BR: "BRL",
		CA: "CAD",
		AU: "AUD",
		JP: "JPY",
		SE: "SEK",
		NO: "NOK",
		DK: "DKK",
		PL: "PLN",
		US: "USD",
	};
	const tz = browserTimezone();
	if (tz.startsWith("Europe/") && region && !byRegion[region]) return "EUR";
	if (region && byRegion[region]) return byRegion[region];
	if (tz.startsWith("Europe/London")) return "GBP";
	if (tz.startsWith("Europe/")) return "EUR";
	if (tz.startsWith("America/")) return "USD";
	return "EUR";
}

export function timezones(): string[] {
	try {
		return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf("timeZone");
	} catch {
		return [browserTimezone()];
	}
}
