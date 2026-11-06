import { Dialog as Base } from "@base-ui/react/dialog";
import { X } from "lucide-react";
import type { ReactNode } from "react";

export function Dialog({
	open,
	onOpenChange,
	title,
	description,
	children,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	title: string;
	description?: string;
	children: ReactNode;
}) {
	return (
		<Base.Root open={open} onOpenChange={onOpenChange}>
			<Base.Portal>
				<Base.Backdrop className="fixed inset-0 bg-black/40 backdrop-blur-[2px] transition-opacity data-[ending-style]:opacity-0 data-[starting-style]:opacity-0" />
				<Base.Popup className="fixed top-1/2 left-1/2 z-50 max-h-[90dvh] w-[min(560px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-line bg-surface p-6 shadow-2xl transition-all data-[ending-style]:scale-95 data-[ending-style]:opacity-0 data-[starting-style]:scale-95 data-[starting-style]:opacity-0">
					<div className="mb-5 flex items-start justify-between gap-4">
						<div>
							<Base.Title className="text-base font-semibold">{title}</Base.Title>
							{description && (
								<Base.Description className="mt-1 text-sm text-muted">{description}</Base.Description>
							)}
						</div>
						<Base.Close
							className="rounded-md p-1 text-muted hover:bg-raised hover:text-fg"
							aria-label="Close"
						>
							<X className="size-4" />
						</Base.Close>
					</div>
					{children}
				</Base.Popup>
			</Base.Portal>
		</Base.Root>
	);
}
