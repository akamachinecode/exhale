import { Popover } from "@base-ui/react/popover";
import { useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Check, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { type ChargeView, useChangeChargeDate, useSetChargeStatus, useUndo } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { cn } from "@/lib/utils";

function useUndoToast() {
	const qc = useQueryClient();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	return (message: string, auditId?: string) =>
		toast.success(
			message,
			auditId ? { action: { label: "Undo", onClick: () => undo.mutate({ data: { auditId } }) } } : undefined,
		);
}

function errorToast(e: unknown) {
	toast.error(e instanceof ApiError ? e.detail : "Something went wrong");
}

export function ChargeActions({
	charge,
	compact = false,
	persistent = false,
}: {
	charge: ChargeView;
	compact?: boolean;
	persistent?: boolean;
}) {
	const qc = useQueryClient();
	const done = useUndoToast();
	const status = useSetChargeStatus({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				done(`${charge.subscriptionName} marked ${r.charge.status}`, r.auditId);
			},
			onError: errorToast,
		},
	});
	if (charge.status !== "pending" && charge.status !== "failed") {
		return (
			<Button
				size="sm"
				variant="ghost"
				onClick={() => status.mutate({ data: { chargeId: charge.id, status: "pending" } })}
			>
				Reopen
			</Button>
		);
	}
	const due = charge.daysUntil <= 0 || charge.status === "failed";
	const markPaid = () => status.mutate({ data: { chargeId: charge.id, status: "paid" } });
	if (due) {
		// A due or overdue charge needs an answer: labeled actions, always visible.
		return (
			<div className="flex items-center justify-end gap-1.5">
				<Button size="sm" variant="secondary" onClick={markPaid}>
					<Check className="text-ok" /> Mark paid
				</Button>
				<Button
					size="sm"
					variant="ghost"
					title="It failed"
					aria-label="Mark as failed"
					onClick={() => status.mutate({ data: { chargeId: charge.id, status: "failed" } })}
				>
					<X /> {!compact && "Failed"}
				</Button>
				<MoveCharge charge={charge} />
			</div>
		);
	}
	// Future charges: quiet actions that appear on hover or keyboard focus.
	return (
		<div
			className={cn(
				"flex items-center justify-end gap-0.5 transition-opacity",
				!persistent && "md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100",
			)}
		>
			<Button
				size="icon"
				variant="ghost"
				title="Already paid"
				aria-label={`Mark ${charge.subscriptionName} as paid`}
				onClick={markPaid}
			>
				<Check />
			</Button>
			<MoveCharge charge={charge} />
		</div>
	);
}

function MoveCharge({ charge, label = false }: { charge: ChargeView; label?: boolean }) {
	const qc = useQueryClient();
	const done = useUndoToast();
	const [open, setOpen] = useState(false);
	const [date, setDate] = useState(charge.dueDate);
	const [scope, setScope] = useState<"this" | "following">("this");
	const move = useChangeChargeDate({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				setOpen(false);
				done(`${charge.subscriptionName} moved`, r.auditId);
			},
			onError: errorToast,
		},
	});
	return (
		<Popover.Root open={open} onOpenChange={setOpen}>
			<Popover.Trigger
				className={cn(
					"inline-flex h-8 items-center justify-center gap-1.5 rounded-lg text-sm text-muted transition-colors hover:bg-raised hover:text-fg",
					label ? "px-3" : "w-8",
				)}
				aria-label={`Move ${charge.subscriptionName}`}
				title="Move this charge"
			>
				<CalendarClock className="size-4" />
				{label && "Move"}
			</Popover.Trigger>
			<Popover.Portal>
				<Popover.Positioner sideOffset={6} align="end">
					<Popover.Popup className="z-50 w-72 rounded-xl border border-line bg-surface p-4 shadow-xl">
						<p className="text-sm font-semibold">Move {charge.subscriptionName}</p>
						<div className="mt-3 grid gap-3">
							<Input
								type="date"
								value={date}
								onChange={(e) => setDate(e.target.value)}
								aria-label="New date"
							/>
							<div
								className="grid grid-cols-2 gap-1 rounded-lg bg-raised p-1 text-xs"
								role="radiogroup"
								aria-label="Apply to"
							>
								{(["this", "following"] as const).map((s) => (
									<button
										key={s}
										type="button"
										aria-pressed={scope === s}
										onClick={() => setScope(s)}
										className={cn(
											"rounded-md px-2 py-1.5 font-medium text-muted",
											scope === s && "bg-surface text-fg shadow-sm",
										)}
									>
										{s === "this" ? "Only this charge" : "This and following"}
									</button>
								))}
							</div>
							<p className="text-xs text-muted">
								{scope === "this"
									? "A one-off delay: later charges keep their date."
									: "The billing day changes from this charge on."}
							</p>
							<Button
								variant="primary"
								disabled={move.isPending || date === charge.dueDate}
								onClick={() => move.mutate({ data: { chargeId: charge.id, newDate: date, scope } })}
							>
								Move charge
							</Button>
						</div>
					</Popover.Popup>
				</Popover.Positioner>
			</Popover.Portal>
		</Popover.Root>
	);
}
