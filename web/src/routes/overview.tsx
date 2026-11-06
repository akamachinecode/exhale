import { Link } from "@tanstack/react-router";
import { AlertTriangle, ArrowRight, CreditCard, Plus } from "lucide-react";
import { useState } from "react";
import { ChargeActions } from "@/components/charge-actions";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { ChargeStatus } from "@/components/status";
import { NewSubscription } from "@/components/subscription-form";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import {
	type ChargeView,
	type Money,
	useListCharges,
	useListSubscriptions,
	useNeedsAttention,
	useSpendingSummary,
} from "@/gen/api";
import { addDaysISO, date, hue, money, todayISO } from "@/lib/utils";

function sum(charges: ChargeView[], currency: string): Money {
	return {
		currency,
		minor: charges.filter((c) => c.amount.currency === currency).reduce((a, c) => a + c.amount.minor, 0),
	};
}

export function Overview() {
	const [creating, setCreating] = useState(false);
	const summary = useSpendingSummary();
	const attention = useNeedsAttention();
	const today = todayISO();
	const upcoming = useListCharges({ from: today, to: addDaysISO(today, 30) });
	const s = summary.data;
	const main = s?.mainCurrency ?? "EUR";
	const monthly = s?.monthlyByCurrency ?? [];
	const yearly = s?.yearlyByCurrency ?? [];
	const next7 = s?.next7Days ?? [];
	const unconfirmed = attention.data?.unconfirmed ?? [];
	const expiring = attention.data?.expiringPaymentMethods ?? [];
	const charges = upcoming.data?.charges ?? [];
	const byCat = s?.byCategory ?? [];
	const catTotal = Math.max(
		1,
		byCat.reduce((a, c) => a + c.monthly.minor, 0),
	);
	const subs = useListSubscriptions({ status: "active" }).data?.subscriptions ?? [];
	const byMethod = new Map<string, Map<string, number>>();
	for (const sub of subs) {
		const key = sub.paymentMethod ?? "No payment method";
		const m = byMethod.get(key) ?? new Map<string, number>();
		m.set(sub.monthlyCost.currency, (m.get(sub.monthlyCost.currency) ?? 0) + sub.monthlyCost.minor);
		byMethod.set(key, m);
	}
	const methods = [...byMethod].map(([name, m]) => ({
		name,
		count: subs.filter((x) => (x.paymentMethod ?? "No payment method") === name).length,
		total: [...m].map(([currency, minor]) => money({ currency, minor })).join(" + "),
	}));
	const mainMonthly = monthly.find((m) => m.currency === main) ?? monthly[0];
	const mainYearly = yearly.find((m) => m.currency === main) ?? yearly[0];
	const empty = s && s.activeSubscriptions === 0;

	return (
		<>
			<PageHeader
				title="Overview"
				description={new Intl.DateTimeFormat(undefined, {
					weekday: "long",
					day: "numeric",
					month: "long",
				}).format(new Date())}
				action={
					<Button variant="primary" onClick={() => setCreating(true)}>
						<Plus /> New subscription
					</Button>
				}
			/>
			<NewSubscription open={creating} onOpenChange={setCreating} />

			<div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
				<Kpi
					label="Monthly"
					value={money(mainMonthly)}
					hint={
						monthly.length > 1
							? `+ ${monthly.length - 1} other currencies`
							: `${s?.activeSubscriptions ?? 0} active subscriptions`
					}
				/>
				<Kpi label="Yearly" value={money(mainYearly)} hint="at current prices" />
				<Kpi
					label="Next 7 days"
					value={money(sum(next7, main))}
					hint={`${next7.length} charge${next7.length === 1 ? "" : "s"}`}
				/>
				<Kpi
					label="Needs attention"
					value={String(unconfirmed.length + expiring.length)}
					hint={unconfirmed.length ? "unconfirmed charges" : "all clear"}
					tone={unconfirmed.length + expiring.length > 0 ? "warn" : undefined}
				/>
			</div>

			{empty ? (
				<Card className="mt-6 grid place-items-center px-6 py-16 text-center">
					<p className="text-base font-semibold">Breathe out. Add your first subscription.</p>
					<p className="mt-1 max-w-md text-sm text-muted">
						Exhale reminds you before every charge and lets you fix it right from the notification.
					</p>
					<Button className="mt-5" variant="primary" onClick={() => setCreating(true)}>
						<Plus /> New subscription
					</Button>
				</Card>
			) : (
				<div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_340px]">
					<div className="grid min-w-0 content-start gap-6">
						{(unconfirmed.length > 0 || expiring.length > 0) && (
							<Card className="border-warn/40">
								<CardHeader
									title={
										<span className="flex items-center gap-2">
											<AlertTriangle className="size-4 text-warn" />
											Needs attention
										</span>
									}
									description="Charges whose date passed without confirmation."
								/>
								<ul className="divide-y divide-line border-t border-line">
									{unconfirmed.map((c) => (
										<ChargeRow key={c.id} charge={c} />
									))}
									{expiring.map((p) => (
										<li key={p.id} className="flex items-center gap-3 px-5 py-3 text-sm">
											<CreditCard className="size-4 text-warn" />
											<span className="flex-1">
												{p.name} {p.last4 && `••${p.last4}`} expires {date(p.expiresOn, "long")}
											</span>
										</li>
									))}
								</ul>
							</Card>
						)}
						<Card>
							<CardHeader
								title="Next 30 days"
								description={
									charges.length ? `${charges.length} charges · ${money(sum(charges, main))}` : undefined
								}
								action={
									<Link
										to="/calendar"
										className="inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg"
									>
										Calendar <ArrowRight className="size-3.5" />
									</Link>
								}
							/>
							{charges.length === 0 ? (
								<p className="border-t border-line px-5 py-8 text-center text-sm text-muted">
									Nothing due in the next 30 days.
								</p>
							) : (
								<ul className="divide-y divide-line border-t border-line">
									{charges.map((c) => (
										<ChargeRow key={c.id} charge={c} />
									))}
								</ul>
							)}
						</Card>
					</div>
					<div className="grid content-start gap-6">
						<Card>
							<CardHeader title="By category" description="Share of monthly spend" />
							<div className="border-t border-line px-5 py-4">
								<div className="flex h-2.5 overflow-hidden rounded-full bg-raised" aria-hidden>
									{byCat.map((c) => (
										<div
											key={`${c.category}${c.monthly.currency}`}
											className="h-full first:rounded-l-full last:rounded-r-full"
											style={{
												width: `${(c.monthly.minor / catTotal) * 100}%`,
												background: `oklch(0.68 0.12 ${hue(c.category)})`,
											}}
										/>
									))}
								</div>
								<ul className="mt-4 grid gap-2.5">
									{byCat.length === 0 && <li className="text-sm text-muted">No data yet.</li>}
									{byCat.map((c) => (
										<li
											key={`${c.category}${c.monthly.currency}`}
											className="flex items-center gap-2.5 text-sm"
										>
											<span
												className="size-2.5 shrink-0 rounded-full"
												style={{ background: `oklch(0.68 0.12 ${hue(c.category)})` }}
											/>
											<span className="min-w-0 flex-1 truncate">{c.category}</span>
											<span className="tnum text-xs text-faint">
												{Math.round((c.monthly.minor / catTotal) * 100)}%
											</span>
											<span className="tnum w-20 text-right text-muted">{money(c.monthly)}</span>
										</li>
									))}
								</ul>
							</div>
						</Card>
						<Card>
							<CardHeader
								title="By payment method"
								description="What each card or account carries per month"
							/>
							<ul className="divide-y divide-line border-t border-line">
								{methods.map((m) => (
									<li key={m.name} className="flex items-center gap-3 px-5 py-3 text-sm">
										<CreditCard className="size-4 text-faint" />
										<span className="min-w-0 flex-1">
											<span className="block truncate font-medium">{m.name}</span>
											<span className="block text-xs text-muted">
												{m.count} subscription{m.count === 1 ? "" : "s"}
											</span>
										</span>
										<span className="tnum text-right text-muted">{m.total}</span>
									</li>
								))}
							</ul>
						</Card>
					</div>
				</div>
			)}
		</>
	);
}

function Kpi({ label, value, hint, tone }: { label: string; value: string; hint?: string; tone?: "warn" }) {
	return (
		<Card className="px-4 py-3.5">
			<p className="text-xs font-medium text-muted">{label}</p>
			<p className={`tnum mt-1 text-2xl font-semibold tracking-tight ${tone === "warn" ? "text-warn" : ""}`}>
				{value}
			</p>
			{hint && <p className="mt-0.5 truncate text-xs text-muted">{hint}</p>}
		</Card>
	);
}

function ChargeRow({ charge }: { charge: ChargeView }) {
	const due = charge.daysUntil <= 0 || charge.status === "failed";
	return (
		<li className="group flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 sm:flex-nowrap sm:px-5">
			<ServiceTile name={charge.subscriptionName} size="sm" />
			<div className="min-w-0 flex-1">
				<Link
					to="/subscriptions/$id"
					params={{ id: charge.subscriptionId }}
					className="block truncate text-sm font-medium hover:underline"
				>
					{charge.subscriptionName}
				</Link>
				<p className="truncate text-xs text-muted">
					{date(charge.dueDate, "day")}
					{charge.delayed && ` · moved from ${date(charge.originalDate)}`}
					{charge.paymentMethod && ` · ${charge.paymentMethod}`}
				</p>
			</div>
			<div className="hidden sm:block">
				<ChargeStatus charge={charge} />
			</div>
			<span className="tnum text-right text-sm font-medium sm:w-24">{money(charge.amount)}</span>
			<div className={due ? "basis-full sm:basis-auto" : ""}>
				<ChargeActions charge={charge} compact />
			</div>
		</li>
	);
}
