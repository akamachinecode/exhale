import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";
import type { ReactNode } from "react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { type ChargeView, type SubscriptionRef, useAudit, useDecideRenewal } from "@/gen/api";
import { date, money, relativeDays } from "@/lib/utils";

/** The audit as a page: facts worth acting on, each with a way to act. */
export function Checkup() {
	const q = useAudit();
	const a = q.data;
	const qc = useQueryClient();
	const keep = useDecideRenewal({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	if (!a) return <PageHeader title="Check-up" description="Looking…" />;
	return (
		<>
			<PageHeader
				title="Check-up"
				description={
					a.findings === 0
						? "Nothing to look at. Breathe out."
						: `${a.findings} thing${a.findings === 1 ? "" : "s"} worth a look.`
				}
			/>
			<div className="grid max-w-[820px] gap-6">
				<Section
					title="Free trials about to charge"
					hint="Cancel before the date or it starts charging."
					items={a.trialsEnding ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={`ends ${relativeDays(c.daysUntil)}, then ${money(c.amount)}`}
						/>
					)}
				</Section>
				<Section
					title="Last days to cancel"
					hint="These need notice: after the date they renew."
					items={a.cancelDeadlines ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={`cancel by ${date(c.cancelBy, "long")} · renews ${date(c.dueDate, "long")} for ${money(c.amount)}`}
							action={
								<Button size="sm" onClick={() => keep.mutate({ data: { chargeId: c.id, decision: "keep" } })}>
									Keep it
								</Button>
							}
						/>
					)}
				</Section>
				<Section title="About to expire" hint="They do not renew by themselves." items={a.expiring ?? []}>
					{(c) => <ChargeLine key={c.id} charge={c} detail={`expires ${date(c.dueDate, "long")}`} />}
				</Section>
				<Section
					title="Big charges this month"
					hint="Yearly bills are the ones that surprise."
					items={a.bigCharges ?? []}
				>
					{(c) => (
						<ChargeLine key={c.id} charge={c} detail={`${money(c.amount)} on ${date(c.dueDate, "long")}`} />
					)}
				</Section>
				<Section
					title="Cards about to expire"
					hint="Move their subscriptions before charges fail."
					items={a.cardsExpiring ?? []}
				>
					{(c) => (
						<li key={c.paymentMethod.id} className="px-5 py-3 text-sm">
							<p className="font-medium">
								{c.paymentMethod.name}
								{c.paymentMethod.last4 && ` ••${c.paymentMethod.last4}`}{" "}
								<span className="font-normal text-muted">
									expires {date(c.paymentMethod.expiresOn, "long")}
								</span>
							</p>
							<p className="mt-0.5 text-xs text-muted">
								{(c.subscriptions ?? []).length
									? `Pays ${(c.subscriptions ?? []).map((s) => s.name).join(", ")}.`
									: "No subscription uses it."}{" "}
								<Link to="/settings" className="font-medium text-fg hover:underline">
									Manage payment methods
								</Link>
							</p>
						</li>
					)}
				</Section>
				<Section title="Prices that went up" hint="In the last twelve months." items={a.priceIncreases ?? []}>
					{(p) => (
						<SubLine
							key={p.subscription.id + p.since}
							sub={p.subscription}
							detail={`${money(p.from)} → ${money(p.to)} since ${date(p.since, "long")} (${p.changePercent}%)`}
							value={`+${money(p.yearlyDelta)} a year`}
						/>
					)}
				</Section>
				<Section
					title="Paid twice?"
					hint="The same service in more than one subscription."
					items={a.duplicates ?? []}
				>
					{(g) => (
						<li key={g.service} className="px-5 py-3 text-sm">
							{(g.subscriptions ?? []).map((s, i) => (
								<span key={s.id}>
									{i > 0 && <span className="text-muted"> and </span>}
									<Link to="/subscriptions/$id" params={{ id: s.id }} className="font-medium hover:underline">
										{s.name}
									</Link>
									{s.payer && <span className="text-muted"> ({s.payer})</span>}
								</span>
							))}
						</li>
					)}
				</Section>
				<Section
					title="You said you don't use"
					hint="What they cost if you keep them."
					items={a.notUsed ?? []}
				>
					{(u) => (
						<SubLine
							key={u.subscription.id}
							sub={u.subscription}
							detail={`answered ${date(u.answeredOn, "long")}`}
							value={`${money(u.yearly)} a year`}
						/>
					)}
				</Section>
				<Section
					title="Charges to confirm"
					hint="Their date passed. Were they charged?"
					items={a.unconfirmed ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={`was due ${date(c.dueDate, "long")} · ${money(c.amount)}`}
						/>
					)}
				</Section>
			</div>
		</>
	);
}

function Section<T>({
	title,
	hint,
	items,
	children,
}: {
	title: string;
	hint: string;
	items: T[];
	children: (item: T) => ReactNode;
}) {
	if (items.length === 0) return null;
	return (
		<Card>
			<CardHeader title={title} description={hint} />
			<ul className="divide-y divide-line border-t border-line">{items.map(children)}</ul>
		</Card>
	);
}

function ChargeLine({ charge, detail, action }: { charge: ChargeView; detail: string; action?: ReactNode }) {
	return (
		<li className="flex items-center gap-3 px-5 py-3 text-sm">
			<ServiceTile name={charge.subscriptionName} size="sm" />
			<span className="min-w-0 flex-1">
				<Link
					to="/subscriptions/$id"
					params={{ id: charge.subscriptionId }}
					className="block truncate font-medium hover:underline"
				>
					{charge.subscriptionName}
				</Link>
				<span className="block text-xs text-muted">{detail}</span>
			</span>
			{action}
			<Link
				to="/subscriptions/$id"
				params={{ id: charge.subscriptionId }}
				aria-label={`Open ${charge.subscriptionName}`}
				className="text-muted hover:text-fg"
			>
				<ArrowRight className="size-4" />
			</Link>
		</li>
	);
}

function SubLine({ sub, detail, value }: { sub: SubscriptionRef; detail: string; value: string }) {
	return (
		<li className="flex items-center gap-3 px-5 py-3 text-sm">
			<ServiceTile name={sub.name} size="sm" />
			<span className="min-w-0 flex-1">
				<Link
					to="/subscriptions/$id"
					params={{ id: sub.id }}
					className="block truncate font-medium hover:underline"
				>
					{sub.name}
				</Link>
				<span className="block text-xs text-muted">{detail}</span>
			</span>
			<span className="tnum text-right font-medium">{value}</span>
		</li>
	);
}
