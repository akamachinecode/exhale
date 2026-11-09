import { Eye, EyeOff } from "lucide-react";
import { type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, useState } from "react";
import { cn } from "@/lib/utils";

const control =
	"h-9 w-full rounded-lg border border-line bg-surface px-3 text-sm text-fg placeholder:text-faint focus:border-accent focus:outline-2 focus:outline-accent/25 disabled:opacity-60";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
	return <input className={cn(control, className)} {...props} />;
}

/** A password input with a button to show or hide what was typed. */
export function PasswordInput({ className, ...props }: Omit<InputHTMLAttributes<HTMLInputElement>, "type">) {
	const [visible, setVisible] = useState(false);
	return (
		<span className="relative block">
			<input type={visible ? "text" : "password"} className={cn(control, "pr-10", className)} {...props} />
			<button
				type="button"
				onClick={() => setVisible(!visible)}
				aria-label={visible ? "Hide password" : "Show password"}
				aria-pressed={visible}
				className="absolute inset-y-0 right-0 grid w-10 place-items-center rounded-r-lg text-faint transition-colors hover:text-fg"
			>
				{visible ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
			</button>
		</span>
	);
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
		<label className="grid content-start gap-1.5">
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
