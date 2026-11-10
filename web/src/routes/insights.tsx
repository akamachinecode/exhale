import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";
import { useState } from "react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { Card, CardHeader } from "@/components/ui/card";
import { type MonthSpend, useMonthBalances, useSpendingHistory, useSpendingSummary } from "@/gen/api";
import { locale, t, tn } from "@/lib/i18n";
import { cn, date, money, todayISO } from "@/lib/utils";

const monthName = (m: string, long = false) =>
	new Intl.DateTimeFormat(locale, {
		month: long ? "long" : "short",
		year: long ? "numeric" : undefined,
		timeZone: "UTC",
	}).format(new Date(`${m}-01T12:00:00Z`));

function shiftMonth(month: string, n: number) {
	const [y, m] = month.split("-").map(Number);
	return new Date(Date.UTC(y, m - 1 + n, 1)).toISOString().slice(0, 7);
}

export function Insights() {
	const history = useSpendingHistory({ months: 12 });
	const summary = useSpendingSummary();
	const h = history.data;
	const s = summary.data;
	const thisMonth = todayISO().slice(0, 7);
	const months = h?.months ?? [];
	const past = months.filter((m) => m.month <= thisMonth);
	const paidMonths = past.filter((m) => m.paid.minor > 0);
	const average =
		h && paidMonths.length
			? {
					currency: h.mainCurrency,
					minor: Math.round(paidMonths.reduce((a, m) => a + m.paid.minor, 0) / paidMonths.length),
				}
			: undefined;
	const subs = h?.bySubscription ?? [];
	const top = Math.max(1, ...subs.map((x) => x.paid.minor));

	return (
		<>
			<PageHeader title={t("Insights")} description={t("What you really paid, and what is coming.")} />
			<div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
				<Stat label={t("Paid in the last 12 months")} value={money(h?.totalPaid)} />
				<Stat
					label={t("Average month")}
					value={money(average)}
					hint={
						paidMonths.length
							? tn(paidMonths.length, "over {n} month with payments", "over {n} months with payments")
							: t("no payments yet")
					}
				/>
				<Stat
					label={t("Put away each month")}
					value={money(s?.setAsideMonthly)}
					hint={t("for what is billed yearly or every few months")}
				/>
				<Stat
					label={t("Heaviest month ahead")}
					value={h?.heaviestMonth ? monthName(h.heaviestMonth, true) : "—"}
					hint={money(months.find((m) => m.month === h?.heaviestMonth)?.expected)}
				/>
			</div>

			<Card className="mt-6">
				<CardHeader
					title={t("Month by month")}
					description={t(
						"Paid so far and still to come, in {0}. Past months come from the charges you confirmed.",
						h?.mainCurrency ?? "",
					)}
				/>
				<div className="border-t border-line px-5 py-5">
					<Bars months={months} current={thisMonth} />
					<div className="mt-4 flex gap-5 text-xs text-muted">
						<span className="flex items-center gap-1.5">
							<span className="size-2.5 rounded-sm bg-fg" /> {t("Paid")}
						</span>
						<span className="flex items-center gap-1.5">
							<span className="size-2.5 rounded-sm border border-dashed border-muted" /> {t("Still to come")}
						</span>
					</div>
				</div>
			</Card>

			<div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
				<Card className="self-start">
					<CardHeader
						title={t("What each one has cost")}
						description={t("Everything marked as paid, since you track it.")}
					/>
					{subs.length === 0 ? (
						<p className="border-t border-line px-5 py-8 text-center text-sm text-muted">
							{t("Confirm a charge and it shows up here.")}
						</p>
					) : (
						<ul className="divide-y divide-line border-t border-line">
							{subs.map((x) => (
								<li key={x.id} className="px-5 py-3 text-sm">
									<Link
										to="/subscriptions/$id"
										params={{ id: x.id }}
										className="group flex items-center gap-3"
									>
										<ServiceTile name={x.name} size="sm" />
										<span className="min-w-0 flex-1">
											<span className="block truncate font-medium group-hover:underline">{x.name}</span>
											<span className="block text-xs text-muted">
												{tn(x.count, "{n} charge", "{n} charges")}
												{x.since && t(" since {0}", date(x.since, "long"))}
											</span>
										</span>
										<span className="tnum font-medium">{money(x.paid)}</span>
									</Link>
									<div className="mt-2 h-1 overflow-hidden rounded-full bg-raised" aria-hidden>
										<div
											className="h-full rounded-full bg-fg/70"
											style={{ width: `${(x.paid.minor / top) * 100}%` }}
										/>
									</div>
								</li>
							))}
						</ul>
					)}
				</Card>
				<BalancesCard />
			</div>
			{(h?.unconverted ?? []).length > 0 && (
				<p className="mt-4 text-xs text-muted">
					{t("Not included (no exchange rate):")} {(h?.unconverted ?? []).join(", ")}.
				</p>
			)}
		</>
	);
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
	return (
		<Card className="px-4 py-3.5">
			<p className="text-xs font-medium text-muted">{label}</p>
			<p className="tnum mt-1 truncate text-2xl font-semibold tracking-tight">{value}</p>
			{hint && <p className="mt-0.5 truncate text-xs text-muted">{hint}</p>}
		</Card>
	);
}

function Bars({ months, current }: { months: MonthSpend[]; current: string }) {
	const max = Math.max(1, ...months.map((m) => m.paid.minor + m.expected.minor));
	return (
		<div className="flex gap-1 sm:gap-2" role="img" aria-label={t("Spend per month")}>
			{months.map((m) => {
				const total = m.paid.minor + m.expected.minor;
				return (
					<div key={m.month} className="group relative min-w-0 flex-1">
						<div className="flex h-44 flex-col justify-end">
							<div
								className="pointer-events-none absolute -top-1 left-1/2 z-10 hidden -translate-x-1/2 -translate-y-full rounded-lg border border-line bg-surface px-2.5 py-1.5 text-xs whitespace-nowrap shadow-lg group-hover:block"
								role="tooltip"
							>
								<span className="font-medium">{monthName(m.month, true)}</span>
								{m.paid.minor > 0 && (
									<span className="block text-muted">
										{t("Paid")} {money(m.paid)}
									</span>
								)}
								{m.expected.minor > 0 && (
									<span className="block text-muted">
										{t("To come")} {money(m.expected)}
									</span>
								)}
								{total === 0 && <span className="block text-muted">{t("Nothing")}</span>}
							</div>
							{m.expected.minor > 0 && (
								<div
									className="rounded-t-sm border border-b-0 border-dashed border-muted/70"
									style={{ height: `${(m.expected.minor / max) * 100}%` }}
								/>
							)}
							{m.paid.minor > 0 && (
								<div
									className={cn("bg-fg", m.expected.minor === 0 && "rounded-t-sm")}
									style={{ height: `${(m.paid.minor / max) * 100}%` }}
								/>
							)}
						</div>
						<span
							className={cn(
								"mt-1.5 block truncate text-center text-[10px] text-faint sm:text-[11px]",
								m.month === current && "font-semibold text-fg",
							)}
						>
							{monthName(m.month)}
						</span>
					</div>
				);
			})}
		</div>
	);
}

/** Who owes whom for the subscriptions with a cost split. */
function BalancesCard() {
	const [month, setMonth] = useState(todayISO().slice(0, 7));
	const q = useMonthBalances({ month });
	const b = q.data;
	const charges = b?.charges ?? [];
	const members = (b?.members ?? []).filter((m) => m.paid.minor > 0 || m.share.minor > 0);
	return (
		<Card className="self-start">
			<CardHeader
				title={t("Who owes whom")}
				description={t("For the subscriptions whose cost you split.")}
				action={
					<div className="flex items-center gap-1 text-[13px]">
						<button
							type="button"
							className="rounded-md px-1.5 py-1 text-muted hover:bg-raised hover:text-fg"
							aria-label={t("Previous month")}
							onClick={() => setMonth(shiftMonth(month, -1))}
						>
							‹
						</button>
						<span className="min-w-24 text-center font-medium capitalize">{monthName(month, true)}</span>
						<button
							type="button"
							className="rounded-md px-1.5 py-1 text-muted hover:bg-raised hover:text-fg"
							aria-label={t("Next month")}
							onClick={() => setMonth(shiftMonth(month, 1))}
						>
							›
						</button>
					</div>
				}
			/>
			{charges.length === 0 ? (
				<p className="border-t border-line px-5 py-8 text-center text-sm text-muted">
					{t("Nothing split this month. Edit a subscription and choose who shares it.")}
				</p>
			) : (
				<div className="border-t border-line">
					<ul className="grid gap-2 px-5 py-4">
						{(b?.transfers ?? []).length === 0 && (
							<li className="text-sm">{t("All even. Nobody owes anything.")}</li>
						)}
						{(b?.transfers ?? []).map((transfer) => (
							<li key={transfer.from + transfer.to} className="flex items-center gap-2 text-sm font-medium">
								<ArrowRight className="size-4 text-muted" />
								{transfer.text}
							</li>
						))}
					</ul>
					<table className="w-full border-t border-line text-sm">
						<thead className="text-left text-xs text-muted">
							<tr>
								<th className="py-2 pl-5 font-medium">{t("Member")}</th>
								<th className="px-3 py-2 text-right font-medium">{t("Paid")}</th>
								<th className="py-2 pr-5 text-right font-medium">{t("Share")}</th>
							</tr>
						</thead>
						<tbody className="divide-y divide-line">
							{members.map((m) => (
								<tr key={m.memberId}>
									<td className="py-2 pl-5">{m.name}</td>
									<td className="tnum px-3 py-2 text-right text-muted">{money(m.paid)}</td>
									<td className="tnum py-2 pr-5 text-right text-muted">{money(m.share)}</td>
								</tr>
							))}
						</tbody>
					</table>
					<ul className="divide-y divide-line border-t border-line text-xs text-muted">
						{charges.map((c) => (
							<li key={c.subscription + c.date} className="flex gap-2 px-5 py-2">
								<span className="flex-1 truncate">
									{date(c.date)} · {c.subscription} · {c.paidBy}
									{c.status === "pending" && t(" · not paid yet")}
								</span>
								<span className="tnum">{money(c.amount)}</span>
							</li>
						))}
					</ul>
				</div>
			)}
		</Card>
	);
}
