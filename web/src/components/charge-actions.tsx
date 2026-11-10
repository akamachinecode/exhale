import { Popover } from "@base-ui/react/popover";
import { useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Check, Coins, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import {
	type ChargeView,
	useChangeChargeDate,
	useSetChargeStatus,
	useUndo,
	useUpdateSubscription,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import { cn, decimal, money } from "@/lib/utils";

function useUndoToast() {
	const qc = useQueryClient();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	return (message: string, auditId?: string) =>
		toast.success(
			message,
			auditId
				? { action: { label: t("Undo"), onClick: () => undo.mutate({ data: { auditId } }) } }
				: undefined,
		);
}

/** A charge status as a word for a toast. */
function statusWord(status: string) {
	return (
		{ paid: t("marked paid"), failed: t("marked failed"), skipped: t("skipped"), pending: t("reopened") }[
			status
		] ?? status
	);
}

function errorToast(e: unknown) {
	toast.error(e instanceof ApiError ? e.detail : t("Something went wrong"));
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
				done(t("{0}: {1}", charge.subscriptionName, statusWord(r.charge.status)), r.auditId);
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
				{t("Reopen")}
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
					<Check className="text-ok" /> {t("Mark paid")}
				</Button>
				<Button
					size="sm"
					variant="ghost"
					title={t("It failed")}
					aria-label={t("Mark as failed")}
					onClick={() => status.mutate({ data: { chargeId: charge.id, status: "failed" } })}
				>
					<X /> {!compact && t("Failed")}
				</Button>
				<PaidAmount charge={charge} />
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
				title={t("Already paid")}
				aria-label={t("Mark {0} as paid", charge.subscriptionName)}
				onClick={markPaid}
			>
				<Check />
			</Button>
			<MoveCharge charge={charge} />
		</div>
	);
}

const iconTrigger =
	"inline-flex size-8 items-center justify-center rounded-lg text-sm text-muted transition-colors hover:bg-raised hover:text-fg";

/** Paid, but the bank took another amount: record it and offer to make it the new price. */
function PaidAmount({ charge }: { charge: ChargeView }) {
	const qc = useQueryClient();
	const [open, setOpen] = useState(false);
	const [amount, setAmount] = useState(decimal(charge.amount));
	const reprice = useUpdateSubscription({
		mutation: {
			onSuccess: () => {
				qc.invalidateQueries();
				toast.success(t("New price saved for the next charges"));
			},
			onError: errorToast,
		},
	});
	const pay = useSetChargeStatus({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				setOpen(false);
				const h = r.priceChange;
				if (!h) {
					toast.success(t("{0} marked paid", charge.subscriptionName));
					return;
				}
				toast(
					t("{0} took {1} instead of {2}", charge.subscriptionName, money(h.charged), money(h.expected)),
					{
						description: t("{0}% · {1} a year. Is it the new price?", h.changePercent, money(h.yearlyDelta)),
						duration: 20_000,
						action: {
							label: t("Yes, from now on"),
							onClick: () =>
								reprice.mutate({ data: { id: charge.subscriptionId, price: decimal(h.charged) } }),
						},
					},
				);
			},
			onError: errorToast,
		},
	});
	return (
		<Popover.Root open={open} onOpenChange={setOpen}>
			<Popover.Trigger
				className={iconTrigger}
				aria-label={t("{0} was paid at another amount", charge.subscriptionName)}
				title={t("Paid, another amount")}
			>
				<Coins className="size-4" />
			</Popover.Trigger>
			<Popover.Portal>
				<Popover.Positioner sideOffset={6} align="end">
					<Popover.Popup className="z-50 w-64 rounded-xl border border-line bg-surface p-4 shadow-xl">
						<p className="text-sm font-semibold">{t("How much did it take?")}</p>
						<p className="mt-0.5 text-xs text-muted">
							{t("Expected")} {money(charge.amount)}.
						</p>
						<form
							className="mt-3 grid gap-3"
							onSubmit={(e) => {
								e.preventDefault();
								pay.mutate({
									data: {
										chargeId: charge.id,
										status: "paid",
										chargedAmount: amount.trim().replace(",", "."),
									},
								});
							}}
						>
							<Input
								autoFocus
								inputMode="decimal"
								className="tnum"
								value={amount}
								onChange={(e) => setAmount(e.target.value)}
								aria-label={t("Amount charged")}
							/>
							<Button type="submit" variant="primary" disabled={pay.isPending}>
								{t("Mark paid")}
							</Button>
						</form>
					</Popover.Popup>
				</Popover.Positioner>
			</Popover.Portal>
		</Popover.Root>
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
				done(t("{0} moved", charge.subscriptionName), r.auditId);
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
				aria-label={t("Move {0}", charge.subscriptionName)}
				title={t("Move this charge")}
			>
				<CalendarClock className="size-4" />
				{label && t("Move")}
			</Popover.Trigger>
			<Popover.Portal>
				<Popover.Positioner sideOffset={6} align="end">
					<Popover.Popup className="z-50 w-72 rounded-xl border border-line bg-surface p-4 shadow-xl">
						<p className="text-sm font-semibold">
							{t("Move")} {charge.subscriptionName}
						</p>
						<div className="mt-3 grid gap-3">
							<Input
								type="date"
								value={date}
								onChange={(e) => setDate(e.target.value)}
								aria-label={t("New date")}
							/>
							<div
								className="grid grid-cols-2 gap-1 rounded-lg bg-raised p-1 text-xs"
								role="radiogroup"
								aria-label={t("Apply to")}
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
										{s === "this" ? t("Only this charge") : t("This and following")}
									</button>
								))}
							</div>
							<p className="text-xs text-muted">
								{scope === "this"
									? t("A one-off delay: later charges keep their date.")
									: t("The billing day changes from this charge on.")}
							</p>
							<Button
								variant="primary"
								disabled={move.isPending || date === charge.dueDate}
								onClick={() => move.mutate({ data: { chargeId: charge.id, newDate: date, scope } })}
							>
								{t("Move charge")}
							</Button>
						</div>
					</Popover.Popup>
				</Popover.Positioner>
			</Popover.Portal>
		</Popover.Root>
	);
}
