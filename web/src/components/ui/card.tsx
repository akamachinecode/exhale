import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
	return <div className={cn("rounded-xl border border-line bg-surface", className)} {...props} />;
}

export function CardHeader({
	title,
	description,
	action,
}: {
	title: ReactNode;
	description?: ReactNode;
	action?: ReactNode;
}) {
	return (
		<div className="flex items-start justify-between gap-4 px-5 pt-4 pb-3">
			<div className="min-w-0">
				<h2 className="text-sm font-semibold text-fg">{title}</h2>
				{description && <p className="mt-0.5 text-[13px] text-muted">{description}</p>}
			</div>
			{action}
		</div>
	);
}
