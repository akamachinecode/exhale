import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ArrowLeft, Pause, Play, Trash2, XCircle } from "lucide-react";
import { toast } from "sonner";
import { ChargeActions } from "@/components/charge-actions";
import { ServiceTile } from "@/components/service-tile";
import { ChargeStatus, SubscriptionStatus } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import {
	type ChargeView,
	useDeleteSubscription,
	useGetSubscription,
	useSetSubscriptionStatus,
	useUndo,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { date, money } from "@/lib/utils";

export function SubscriptionDetail() {
	const { id } = useParams({ from: "/subscriptions/$id" });
	const q = useGetSubscription(id);
	const qc = useQueryClient();
	const navigate = useNavigate();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const onError = (e: unknown) => toast.error(e instanceof ApiError ? e.detail : "Something went wrong");
	const status = useSetSubscriptionStatus({ mutation: { onSuccess: () => qc.invalidateQueries(), onError } });
	const del = useDeleteSubscription({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				navigate({ to: "/subscriptions", search: {} });
				toast.success("Subscription deleted", {
					action: { label: "Undo", onClick: () => undo.mutate({ data: { auditId: r.auditId } }) },
				});
			},
			onError,
		},
	});
	if (q.isLoading) return <p className="text-sm text-muted">Loading…</p>;
	if (!q.data) return <p className="text-sm text-muted">Subscription not found.</p>;
	const { subscription: s, upcoming, history } = q.data;
	const act = (action: "pause" | "resume" | "cancel_at_period_end") =>
		status.mutate({ data: { id: s.id, action } });

	return (
		<>
			<Link
				to="/subscriptions"
				search={{}}
				className="mb-4 inline-flex items-center gap-1.5 text-[13px] text-muted hover:text-fg"
			>
				<ArrowLeft className="size-3.5" /> Subscriptions
			</Link>
			<div className="mb-6 flex flex-wrap items-center gap-4">
				<ServiceTile name={s.name} size="lg" />
				<div className="min-w-0 flex-1">
					<div className="flex flex-wrap items-center gap-2">
						<h1 className="truncate text-[22px] font-semibold tracking-tight">{s.name}</h1>
						<SubscriptionStatus status={s.status} />
					</div>
					<p className="mt-0.5 text-sm text-muted">
						<span className="tnum font-medium text-fg">{money(s.price)}</span> {s.cadence}
						{s.status === "cancelled" && s.serviceUntil && ` · service until ${date(s.serviceUntil, "long")}`}
					</p>
				</div>
				<div className="flex flex-wrap gap-2">
					{s.status === "active" && (
						<Button size="sm" onClick={() => act("pause")}>
							<Pause /> Pause
						</Button>
					)}
					{s.status !== "active" && (
						<Button size="sm" onClick={() => act("resume")}>
							<Play /> Resume
						</Button>
					)}
					{s.status === "active" && (
						<Button size="sm" onClick={() => act("cancel_at_period_end")}>
							<XCircle /> Cancel at period end
						</Button>
					)}
					<Button
						size="sm"
						variant="danger"
						aria-label="Delete"
						onClick={() => del.mutate({ data: { id: s.id } })}
					>
						<Trash2 />
					</Button>
				</div>
			</div>

			<div className="grid gap-6 lg:grid-cols-[1fr_300px]">
				<div className="grid content-start gap-6">
					<Card>
						<CardHeader
							title="Upcoming charges"
							description="Move one charge for a one-off delay, or the series from that charge on."
						/>
						<Charges charges={upcoming ?? []} empty="No upcoming charges." />
					</Card>
					<Card>
						<CardHeader title="History" />
						<Charges charges={history ?? []} empty="No past charges yet." />
					</Card>
				</div>
				<Card className="self-start">
					<CardHeader title="Details" />
					<dl className="grid gap-3 border-t border-line px-5 py-4 text-sm">
						<Detail label="Monthly cost" value={money(s.monthlyCost)} />
						<Detail label="Next charge" value={date(s.nextCharge, "long")} />
						<Detail label="Payment method" value={s.paymentMethod ?? "—"} />
						<Detail label="Category" value={s.categoryName ?? "—"} />
						<Detail label="Paid by" value={s.payerName ?? "—"} />
						<Detail
							label="Reminders"
							value={`${(s.reminders.daysBefore ?? []).map((d) => (d === 0 ? "same day" : `${d} days before`)).join(", ")} · ${String(s.reminders.hour).padStart(2, "0")}:${String(s.reminders.minute).padStart(2, "0")}`}
						/>
						<Detail
							label="Visibility"
							value={s.visibility === "private" ? "Private" : "Shared with household"}
						/>
					</dl>
				</Card>
			</div>
		</>
	);
}

function Detail({ label, value }: { label: string; value: string }) {
	return (
		<div className="grid gap-0.5">
			<dt className="text-xs text-muted">{label}</dt>
			<dd className="tnum">{value}</dd>
		</div>
	);
}

function Charges({ charges, empty }: { charges: ChargeView[]; empty: string }) {
	if (charges.length === 0)
		return <p className="border-t border-line px-5 py-8 text-center text-sm text-muted">{empty}</p>;
	return (
		<ul className="divide-y divide-line border-t border-line">
			{charges.map((c) => (
				<li key={c.id} className="flex items-center gap-3 px-5 py-3 text-sm">
					<div className="min-w-0 flex-1">
						<p className="font-medium">{date(c.dueDate, "long")}</p>
						{c.delayed && <p className="text-xs text-muted">moved from {date(c.originalDate)}</p>}
						{c.chargedAmount && c.chargedAmount.currency !== c.amount.currency && (
							<p className="text-xs text-muted">charged {money(c.chargedAmount)}</p>
						)}
					</div>
					<ChargeStatus charge={c} />
					<span className="tnum w-20 text-right font-medium">{money(c.amount)}</span>
					<ChargeActions charge={c} compact />
				</li>
			))}
		</ul>
	);
}
