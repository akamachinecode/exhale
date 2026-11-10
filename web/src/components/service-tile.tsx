import { useState } from "react";
import { useListSubscriptions } from "@/gen/api";
import { onBrand, useBrand } from "@/lib/brands";
import { cn, hue } from "@/lib/utils";

const sizes = {
	sm: "size-7 rounded-md text-[11px]",
	md: "size-9 rounded-lg text-xs",
	lg: "size-12 rounded-xl text-base",
};
const glyph = { sm: "size-3.5", md: "size-[18px]", lg: "size-6" };

/**
 * Service logo: the logo someone uploaded, else the brand mark when known
 * (bundled, offline), else a tinted monogram.
 */
export function ServiceTile({
	name,
	logo,
	size = "md",
}: {
	name: string;
	logo?: string;
	size?: "sm" | "md" | "lg";
}) {
	const brand = useBrand(name);
	const [broken, setBroken] = useState<string>();
	if (logo && broken !== logo) {
		return (
			<img
				src={logo}
				alt=""
				aria-hidden
				onError={() => setBroken(logo)}
				className={cn(
					"inline-block shrink-0 bg-white object-contain ring-1 ring-black/5 dark:ring-white/10",
					sizes[size],
				)}
			/>
		);
	}
	if (brand) {
		const fg = onBrand(brand.hex);
		return (
			<span
				aria-hidden
				className={cn(
					"inline-grid shrink-0 place-items-center ring-1 ring-black/5 dark:ring-white/10",
					sizes[size],
				)}
				style={{ background: `#${brand.hex}` }}
			>
				<svg viewBox="0 0 24 24" className={glyph[size]} fill={fg} aria-hidden>
					<path d={brand.path} />
				</svg>
			</span>
		);
	}
	const letters = name
		.replace(/[^\p{L}\p{N} ]/gu, "")
		.split(/\s+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((w) => w[0]?.toUpperCase())
		.join("");
	return (
		<span
			aria-hidden
			className={cn(
				"inline-grid shrink-0 place-items-center font-semibold tracking-tight",
				"bg-[oklch(0.94_0.045_var(--h))] text-[oklch(0.4_0.11_var(--h))] dark:bg-[oklch(0.32_0.06_var(--h))] dark:text-[oklch(0.86_0.08_var(--h))]",
				sizes[size],
			)}
			style={{ "--h": hue(name) } as React.CSSProperties}
		>
			{letters || "?"}
		</span>
	);
}

/** Uploaded logos by subscription id, for lists that only carry the id. */
export function useLogos() {
	const q = useListSubscriptions();
	return new Map((q.data?.subscriptions ?? []).filter((s) => s.logo).map((s) => [s.id, s.logo as string]));
}
