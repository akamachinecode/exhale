import { Link } from "@tanstack/react-router";
import { AlertTriangle, ArrowRight, Clock, CreditCard, Plus, Wallet } from "lucide-react";
import { useState } from "react";
import { ChargeActions } from "@/components/charge-actions";
import { GetStarted, readGetStartedHidden } from "@/components/get-started";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { ChargeStatus } from "@/components/status";
import { NewSubscription } from "@/components/subscription-form";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { type ChargeView, useListCharges, useNeedsAttention, useSpendingSummary } from "@/gen/api";
import { addDaysISO, date, money, palette, sumMain, todayISO } from "@/lib/utils";

export function Overview() {
	const [creating, setCreating] = useState(false);
	const [setupHidden, setSetupHidden] = useState(readGetStartedHidden);
	const summary = useSpendingSummary();
	const attention = useNeedsAttention();
	const today = todayISO();
	const upcoming = useListCharges({ from: today, to: addDaysISO(today, 30) });
	const s = summary.data;
	const main = s?.mainCurrency ?? "EUR";
	const rates = s?.rates;
	const next7 = s?.next7Days ?? [];
	const unconfirmed = attention.data?.unconfirmed ?? [];
	const expiring = attention.data?.expiringPaymentMethods ?? [];
	const deadlines = attention.data?.deadlines ?? [];
	const payday = s?.beforePayday;
	const charges = upcoming.data?.charges ?? [];
	const byCat = s?.byCategory ?? [];
	const methods = s?.byPaymentMethod ?? [];
	const catTotal = Math.max(
		1,
		byCat.reduce((a, c) => a + c.monthly.minor, 0),
	);
	const sumCharges = (cs: ChargeView[]) =>
		money(
			sumMain(
				cs.map((c) => c.amount),
				rates,
				main,
			),
		);
	const fxNote = s?.converted ? `other currencies at ECB rates of ${date(s.ratesDate)}` : undefined;
	const attentionCount = unconfirmed.length + expiring.length + deadlines.length;
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
			{!setupHidden && (
				<GetStarted onAddSubscription={() => setCreating(true)} onHide={() => setSetupHidden(true)} />
			)}

			<div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
				<Kpi
					label="Monthly"
					tone="brand"
					value={money(s?.monthlyTotal)}
					hint={fxNote ?? `${s?.activeSubscriptions ?? 0} active subscriptions`}
				/>
				<Kpi
					label="Yearly"
					value={money(s?.yearlyTotal)}
					hint={`${s?.activeSubscriptions ?? 0} active subscriptions`}
				/>
				<Kpi
					label="Next 7 days"
					value={sumCharges(next7)}
					hint={`${next7.length} charge${next7.length === 1 ? "" : "s"}`}
				/>
				<Kpi
					label="Needs attention"
					value={String(attentionCount)}
					hint={
						unconfirmed.length
							? "unconfirmed charges"
							: deadlines.length
								? "deadlines close"
								: expiring.length
									? "a card expires soon"
									: "all clear"
					}
					tone={attentionCount > 0 ? "warn" : undefined}
				/>
			</div>

			{empty && !setupHidden ? null : empty ? (
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
						{attentionCount > 0 && (
							<Card className="border-warn/40">
								<CardHeader
									title={
										<span className="flex items-center gap-2">
											<AlertTriangle className="size-4 text-warn" />
											Needs attention
										</span>
									}
									description="Dates that passed without an answer, and deadlines that are close."
									action={
										<Link
											to="/checkup"
											className="inline-flex shrink-0 items-center gap-1 text-[13px] whitespace-nowrap text-muted hover:text-fg"
										>
											Check-up <ArrowRight className="size-3.5" />
										</Link>
									}
								/>
								<ul className="divide-y divide-line border-t border-line">
									{deadlines.map((c) => (
										<li key={`d-${c.id}`} className="flex items-center gap-3 px-5 py-3 text-sm">
											<Clock className="size-4 shrink-0 text-warn" />
											<span className="min-w-0 flex-1">
												<Link
													to="/subscriptions/$id"
													params={{ id: c.subscriptionId }}
													className="font-medium hover:underline"
												>
													{c.subscriptionName}
												</Link>{" "}
												<span className="text-muted">
													{c.kind === "trial"
														? `free trial ends ${date(c.dueDate, "long")}, then ${money(c.amount)}`
														: `cancel by ${date(c.cancelBy, "long")} or it renews for ${money(c.amount)}`}
												</span>
											</span>
										</li>
									))}
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
						{payday && (payday.charges ?? []).length > 0 && (
							<Card className="flex items-center gap-3 px-5 py-3.5 text-sm">
								<Wallet className="size-4 shrink-0 text-muted" />
								<span className="flex-1">
									<span className="tnum font-medium">{money(payday.due)}</span>{" "}
									<span className="text-muted">
										due before payday on {date(payday.payday, "day")} · {(payday.charges ?? []).length} charge
										{(payday.charges ?? []).length === 1 ? "" : "s"}
									</span>
								</span>
							</Card>
						)}
						<Card>
							<CardHeader
								title="Next 30 days"
								description={
									charges.length ? `${charges.length} charges · ${sumCharges(charges)}` : undefined
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
							<CardHeader title="By category" description={`Share of monthly spend, in ${main}`} />
							<div className="border-t border-line px-5 py-4">
								<div className="flex h-2.5 gap-0.5 overflow-hidden rounded-full" aria-hidden>
									{byCat.map((c, i) => (
										<div
											key={c.category}
											className="h-full"
											style={{
												width: `${(c.monthly.minor / catTotal) * 100}%`,
												background: palette[i % palette.length],
											}}
										/>
									))}
								</div>
								<ul className="mt-4 grid gap-2.5">
									{byCat.length === 0 && <li className="text-sm text-muted">No data yet.</li>}
									{byCat.map((c, i) => (
										<li key={c.category} className="flex items-center gap-2.5 text-sm">
											<span
												className="size-2.5 shrink-0 rounded-full"
												style={{ background: palette[i % palette.length] }}
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
								description={`What each card or account carries per month, in ${main}`}
							/>
							<ul className="divide-y divide-line border-t border-line">
								{methods.map((m) => (
									<li key={m.paymentMethod} className="flex items-center gap-3 px-5 py-3 text-sm">
										<CreditCard className="size-4 text-faint" />
										<span className="min-w-0 flex-1">
											<span className="block truncate font-medium">{m.paymentMethod}</span>
											<span className="block text-xs text-muted">
												{m.count} subscription{m.count === 1 ? "" : "s"}
											</span>
										</span>
										<span className="tnum text-right text-muted">{money(m.monthly)}</span>
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

function Kpi({
	label,
	value,
	hint,
	tone,
}: {
	label: string;
	value: string;
	hint?: string;
	tone?: "warn" | "brand";
}) {
	return (
		<Card className="px-4 py-3.5">
			<p className="text-xs font-medium text-muted">{label}</p>
			<p
				className={`tnum mt-1 text-2xl font-semibold tracking-tight ${tone === "warn" ? "text-warn" : tone === "brand" ? "text-brand" : ""}`}
			>
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
				<p className="text-xs text-muted sm:truncate">
					{date(charge.dueDate, "day")}
					{charge.delayed && ` · moved from ${date(charge.originalDate)}`}
					{charge.paymentMethod && (
						<span className="block sm:inline">
							<span className="hidden sm:inline"> · </span>
							{charge.paymentMethod}
						</span>
					)}
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
