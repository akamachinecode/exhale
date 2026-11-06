import { cva, type VariantProps } from "class-variance-authority";
import type { ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

const button = cva(
	"inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0",
	{
		variants: {
			variant: {
				primary: "bg-accent text-accent-fg hover:brightness-105 shadow-sm",
				secondary: "border border-line bg-surface text-fg hover:bg-raised",
				ghost: "text-muted hover:bg-raised hover:text-fg",
				danger: "border border-line bg-surface text-bad hover:bg-bad-soft",
			},
			size: { sm: "h-8 px-3", md: "h-9 px-4", icon: "size-8" },
		},
		defaultVariants: { variant: "secondary", size: "md" },
	},
);

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & VariantProps<typeof button>;

export function Button({ className, variant, size, type = "button", ...props }: ButtonProps) {
	return <button type={type} className={cn(button({ variant, size }), className)} {...props} />;
}
