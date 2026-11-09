import { cva, type VariantProps } from "class-variance-authority";
import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

const badge = cva(
	"inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap",
	{
		variants: {
			tone: {
				neutral: "bg-raised text-muted",
				accent: "bg-accent-soft text-accent",
				ok: "bg-ok-soft text-ok",
				warn: "bg-warn-soft text-warn",
				bad: "bg-bad-soft text-bad",
			},
		},
		defaultVariants: { tone: "neutral" },
	},
);

export function Badge({
	className,
	tone,
	...props
}: HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badge>) {
	return <span className={cn(badge({ tone }), className)} {...props} />;
}
