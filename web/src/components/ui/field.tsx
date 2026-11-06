import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

const control =
	"h-9 w-full rounded-lg border border-line bg-surface px-3 text-sm text-fg placeholder:text-faint focus:border-accent focus:outline-2 focus:outline-accent/25 disabled:opacity-60";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
	return <input className={cn(control, className)} {...props} />;
}

export function Select({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
	return (
		<select
			className={cn(
				control,
				"appearance-none pr-8 bg-[length:16px] bg-[right_0.6rem_center] bg-no-repeat",
				className,
			)}
			{...props}
		>
			{children}
		</select>
	);
}

export function Field({
	label,
	hint,
	error,
	children,
}: {
	label: string;
	hint?: ReactNode;
	error?: string;
	children: ReactNode;
}) {
	return (
		// biome-ignore lint/a11y/noLabelWithoutControl: the control is passed as children
		<label className="grid gap-1.5">
			<span className="text-[13px] font-medium text-fg">{label}</span>
			{children}
			{error ? (
				<span className="text-xs text-bad">{error}</span>
			) : hint ? (
				<span className="text-xs text-muted">{hint}</span>
			) : null}
		</label>
	);
}
