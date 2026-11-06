import { cn, hue } from "@/lib/utils";

/** Monogram tile for a service: offline, no third-party logo requests (ADR 0014). */
export function ServiceTile({ name, size = "md" }: { name: string; size?: "sm" | "md" | "lg" }) {
	const h = hue(name);
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
				"inline-grid shrink-0 place-items-center rounded-lg font-semibold tracking-tight",
				size === "sm" && "size-7 text-[11px]",
				size === "md" && "size-9 text-xs",
				size === "lg" && "size-12 text-base",
			)}
			style={{ background: `oklch(0.93 0.05 ${h})`, color: `oklch(0.38 0.1 ${h})` }}
		>
			{letters || "?"}
		</span>
	);
}
