import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";
import type { ReactNode } from "react";
import { DuplicateIsFine, MoveSubscriptions } from "@/components/answers";
import { PageHeader } from "@/components/layout";
import { ServiceTile, useLogos } from "@/components/service-tile";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { type ChargeView, type SubscriptionRef, useAudit, useDecideRenewal } from "@/gen/api";
import { t, tn } from "@/lib/i18n";
import { date, money, relativeDays } from "@/lib/utils";

/** The audit as a page: facts worth acting on, each with a way to act. */
export function Checkup() {
	const q = useAudit();
	const a = q.data;
	const qc = useQueryClient();
	const keep = useDecideRenewal({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	if (!a) return <PageHeader title={t("Check-up")} description={t("Looking…")} />;
	return (
		<>
			<PageHeader
				title={t("Check-up")}
				description={
					a.findings === 0
						? t("Nothing to look at. Breathe out.")
						: tn(a.findings, "{n} thing worth a look.", "{n} things worth a look.")
				}
			/>
			<div className="grid max-w-[820px] gap-6">
				<Section
					title={t("Free trials about to charge")}
					hint={t("Cancel before the date or it starts charging.")}
					items={a.trialsEnding ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={t("ends {0}, then {1}", relativeDays(c.daysUntil), money(c.amount))}
						/>
					)}
				</Section>
				<Section
					title={t("Last days to cancel")}
					hint={t("These need notice: after the date they renew.")}
					items={a.cancelDeadlines ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={t(
								"cancel by {0} · renews {1} for {2}",
								date(c.cancelBy, "long"),
								date(c.dueDate, "long"),
								money(c.amount),
							)}
							action={
								<Button size="sm" onClick={() => keep.mutate({ data: { chargeId: c.id, decision: "keep" } })}>
									{t("Keep it")}
								</Button>
							}
						/>
					)}
				</Section>
				<Section
					title={t("About to expire")}
					hint={t("They do not renew by themselves.")}
					items={a.expiring ?? []}
				>
					{(c) => <ChargeLine key={c.id} charge={c} detail={t("expires {0}", date(c.dueDate, "long"))} />}
				</Section>
				<Section
					title={t("Big charges this month")}
					hint={t("Yearly bills are the ones that surprise.")}
					items={a.bigCharges ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={t("{0} on {1}", money(c.amount), date(c.dueDate, "long"))}
						/>
					)}
				</Section>
				<Section
					title={t("Cards about to expire")}
					hint={t("Move their subscriptions before charges fail.")}
					items={a.cardsExpiring ?? []}
				>
					{(c) => (
						<li key={c.paymentMethod.id} className="px-5 py-3 text-sm">
							<p className="font-medium">
								{c.paymentMethod.name}
								{c.paymentMethod.last4 && ` ••${c.paymentMethod.last4}`}{" "}
								<span className="font-normal text-muted">
									{t("expires")} {date(c.paymentMethod.expiresOn, "long")}
								</span>
							</p>
							<p className="mt-0.5 text-xs text-muted">
								{(c.subscriptions ?? []).length
									? t("Pays {0}.", (c.subscriptions ?? []).map((s) => s.name).join(", "))
									: t("No subscription uses it.")}{" "}
								<Link
									to="/settings"
									search={{ tab: "general" }}
									className="font-medium text-fg hover:underline"
								>
									{t("Manage payment methods")}
								</Link>
							</p>
							<MoveSubscriptions fromId={c.paymentMethod.id} count={(c.subscriptions ?? []).length} />
						</li>
					)}
				</Section>
				<Section
					title={t("Prices that went up")}
					hint={t("In the last twelve months.")}
					items={a.priceIncreases ?? []}
				>
					{(p) => (
						<SubLine
							key={p.subscription.id + p.since}
							sub={p.subscription}
							detail={t(
								"{0} → {1} since {2} ({3}%)",
								money(p.from),
								money(p.to),
								date(p.since, "long"),
								p.changePercent,
							)}
							value={t("+{0} a year", money(p.yearlyDelta))}
						/>
					)}
				</Section>
				<Section
					title={t("Paid twice?")}
					hint={t("The same service in more than one subscription.")}
					items={a.duplicates ?? []}
				>
					{(g) => (
						<li key={g.key} className="flex items-center gap-3 px-5 py-3 text-sm">
							<span className="min-w-0 flex-1">
								{(g.subscriptions ?? []).map((s, i) => (
									<span key={s.id}>
										{i > 0 && <span className="text-muted"> {t("and")} </span>}
										<Link
											to="/subscriptions/$id"
											params={{ id: s.id }}
											className="font-medium hover:underline"
										>
											{s.name}
										</Link>
										{s.payer && <span className="text-muted"> ({s.payer})</span>}
									</span>
								))}
							</span>
							<DuplicateIsFine groupKey={g.key} />
						</li>
					)}
				</Section>
				<Section
					title={t("Cheaper prices you recorded")}
					hint={t("The provider also offers these prices. Switching saves this much a year.")}
					items={a.cheaperPlans ?? []}
				>
					{(p) => (
						<SubLine
							key={p.subscription.id}
							sub={p.subscription}
							detail={t(
								"{0} {1} instead of {2} a year",
								money(p.other.price),
								p.other.unit === "month" ? t("per month") : t("per year"),
								money(p.currentYearly),
							)}
							value={t("{0} a year", money(p.yearlySaving))}
						/>
					)}
				</Section>
				<Section
					title={t("You said you don't use")}
					hint={t("What they cost if you keep them.")}
					items={a.notUsed ?? []}
				>
					{(u) => (
						<SubLine
							key={u.subscription.id}
							sub={u.subscription}
							detail={t("answered {0}", date(u.answeredOn, "long"))}
							value={t("{0} a year", money(u.yearly))}
						/>
					)}
				</Section>
				<Section
					title={t("Charges to confirm")}
					hint={t("Their date passed. Were they charged?")}
					items={a.unconfirmed ?? []}
				>
					{(c) => (
						<ChargeLine
							key={c.id}
							charge={c}
							detail={t("was due {0} · {1}", date(c.dueDate, "long"), money(c.amount))}
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
			<ServiceTile name={charge.subscriptionName} logo={charge.logo} size="sm" />
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
				aria-label={t("Open {0}", charge.subscriptionName)}
				className="text-muted hover:text-fg"
			>
				<ArrowRight className="size-4" />
			</Link>
		</li>
	);
}

function SubLine({ sub, detail, value }: { sub: SubscriptionRef; detail: string; value: string }) {
	const logos = useLogos();
	return (
		<li className="flex items-center gap-3 px-5 py-3 text-sm">
			<ServiceTile name={sub.name} logo={logos.get(sub.id)} size="sm" />
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
