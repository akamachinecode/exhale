import { useEffect, useSyncExternalStore } from "react";
import type { Brand } from "./brand-table";

// Brand logos ship inside the app (simple-icons, CC0): no request to a
// third party ever reveals what you subscribe to (ADR 0014). The table is
// a chunk of its own, loaded once after the first screen, so it does not
// weigh on the start of the app; until it arrives a tile shows initials.

export type { Brand };

type Matcher = (name: string) => Brand | undefined;
let matcher: Matcher | undefined;
let loading: Promise<void> | undefined;
const listeners = new Set<() => void>();

function build(marks: Record<string, Brand>, keywords: [string, string][]): Matcher {
	const quote = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	// Longest keyword first: the first match wins, so "GitHub Copilot"
	// beats "GitHub" and "YouTube Music" beats "YouTube".
	const rules = [...keywords]
		.sort((a, b) => b[0].length - a[0].length)
		.map(([kw, slug]) => [new RegExp(`(^|[^a-z0-9])${quote(kw)}([^a-z0-9]|$)`), marks[slug]] as const);
	const cache = new Map<string, Brand | undefined>();
	return (name) => {
		const n = name.normalize("NFKD").replace(/\p{M}/gu, "").toLowerCase();
		if (!cache.has(n)) cache.set(n, rules.find(([re]) => re.test(n))?.[1]);
		return cache.get(n);
	};
}

/** Loads the brand table (once). */
export function loadBrands() {
	loading ??= import("./brand-table").then((m) => {
		matcher = build(m.marks, m.keywords);
		for (const l of listeners) l();
	});
	return loading;
}

function subscribe(l: () => void) {
	listeners.add(l);
	return () => listeners.delete(l);
}

/** The bundled brand mark for a subscription name, once the table is loaded. */
export function useBrand(name: string): Brand | undefined {
	const ready = useSyncExternalStore(subscribe, () => matcher !== undefined);
	useEffect(() => {
		void loadBrands();
	}, []);
	return ready ? matcher?.(name) : undefined;
}

/** Picks black or white for a glyph on the brand color. */
export function onBrand(hex: string) {
	const [r, g, b] = [0, 2, 4].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255);
	const l = 0.2126 * r + 0.7152 * g + 0.0722 * b;
	return l > 0.6 ? "#111" : "#fff";
}
