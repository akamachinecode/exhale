import { brandFor, onBrand } from "@/lib/brands";
import { cn, hue } from "@/lib/utils";

const sizes = {
	sm: "size-7 rounded-md text-[11px]",
	md: "size-9 rounded-lg text-xs",
	lg: "size-12 rounded-xl text-base",
};
const glyph = { sm: "size-3.5", md: "size-[18px]", lg: "size-6" };

/** Service logo: the brand mark when known (bundled, offline), otherwise a tinted monogram. */
export function ServiceTile({ name, size = "md" }: { name: string; size?: "sm" | "md" | "lg" }) {
	const brand = brandFor(name);
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
				<svg viewBox="0 0 24 24" className={glyph[size]} fill={fg}>
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
