import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { type CalendarEvent, useCalendar, useSpendingSummary } from "@/gen/api";
import { locale, t } from "@/lib/i18n";
import { cn, money, sumMain, todayISO } from "@/lib/utils";

const weekdays = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

function monthBounds(month: string) {
	const [y, m] = month.split("-").map(Number);
	const days = new Date(Date.UTC(y, m, 0)).getUTCDate();
	const offset = (new Date(Date.UTC(y, m - 1, 1)).getUTCDay() + 6) % 7;
	return { y, m, days, offset, from: `${month}-01`, to: `${month}-${String(days).padStart(2, "0")}` };
}

function shift(month: string, n: number) {
	const [y, m] = month.split("-").map(Number);
	const d = new Date(Date.UTC(y, m - 1 + n, 1));
	return d.toISOString().slice(0, 7);
}

export function Calendar() {
	const search = useSearch({ from: "/calendar" });
	const navigate = useNavigate({ from: "/calendar" });
	const today = todayISO();
	const month = search.month ?? today.slice(0, 7);
	const b = monthBounds(month);
	// Events come from the engine: real charges and, further ahead, the ones
	// the schedule will produce, plus trial ends and last days to cancel.
	const q = useCalendar({ from: b.from, to: b.to });
	const events = q.data?.events ?? [];
	const byDay = new Map<string, CalendarEvent[]>();
	for (const e of events) byDay.set(e.date, [...(byDay.get(e.date) ?? []), e]);
	const summary = useSpendingSummary().data;
	const charges = events.filter((e) => e.kind !== "cancelBy" && e.status !== "skipped");
	const monthTotal = money(
		sumMain(
			charges.map((c) => c.amount),
			summary?.rates,
			summary?.mainCurrency ?? "EUR",
		),
	);
	const count = charges.length;
	const title = new Intl.DateTimeFormat(locale, {
		month: "long",
		year: "numeric",
		timeZone: "UTC",
	}).format(new Date(Date.UTC(b.y, b.m - 1, 1)));
	const cells = Array.from({ length: Math.ceil((b.offset + b.days) / 7) * 7 }, (_, i) => i - b.offset + 1);

	return (
		<>
			<PageHeader
				title={t("Calendar")}
				description={
					count ? t("{0} charges this month · {1}", count, monthTotal) : t("No charges this month.")
				}
				action={
					<div className="flex items-center gap-1">
						<Button
							size="icon"
							variant="ghost"
							aria-label={t("Previous month")}
							onClick={() => navigate({ search: { month: shift(month, -1) } })}
						>
							<ChevronLeft />
						</Button>
						<span className="min-w-36 text-center text-sm font-medium capitalize">{title}</span>
						<Button
							size="icon"
							variant="ghost"
							aria-label={t("Next month")}
							onClick={() => navigate({ search: { month: shift(month, 1) } })}
						>
							<ChevronRight />
						</Button>
						<Button size="sm" className="ml-2" onClick={() => navigate({ search: {} })}>
							{t("Today")}
						</Button>
					</div>
				}
			/>
			<Card className="overflow-hidden">
				<div className="grid grid-cols-7 border-b border-line bg-raised/50 text-center text-xs font-medium text-muted">
					{weekdays.map((d) => (
						<div key={d} className="py-2">
							{d}
						</div>
					))}
				</div>
				<div className="grid grid-cols-7">
					{cells.map((day, i) => {
						const inMonth = day >= 1 && day <= b.days;
						const iso = inMonth ? `${month}-${String(day).padStart(2, "0")}` : "";
						const items = inMonth ? (byDay.get(iso) ?? []) : [];
						const past = inMonth && iso < today;
						const paying = items.filter((c) => c.kind !== "cancelBy" && c.status !== "skipped");
						const dayTotal =
							paying.length > 1 && paying.every((c) => c.amount.currency === paying[0].amount.currency)
								? money({
										currency: paying[0].amount.currency,
										minor: paying.reduce((a, c) => a + c.amount.minor, 0),
									})
								: undefined;
						return (
							<div
								// biome-ignore lint/suspicious/noArrayIndexKey: fixed grid positions
								key={i}
								className={cn(
									"flex min-h-24 flex-col border-line p-1.5 sm:min-h-32 sm:p-2",
									i % 7 !== 6 && "border-r",
									i < cells.length - 7 && "border-b",
									!inMonth && "bg-raised/40",
									past && "bg-raised/25",
								)}
							>
								{inMonth && (
									<span
										className={cn(
											"tnum inline-grid size-6 place-items-center rounded-full text-xs text-muted",
											past && "text-faint",
											iso === today && "bg-accent font-semibold text-accent-fg",
										)}
									>
										{day}
									</span>
								)}
								<div className="mt-1 grid gap-1">
									{items.map((c) => (
										<Link
											key={`${c.subscriptionId}-${c.kind}-${c.chargeId ?? c.date}`}
											to="/subscriptions/$id"
											params={{ id: c.subscriptionId }}
											title={c.kind === "cancelBy" ? c.title : `${c.title} · ${money(c.amount)}`}
											className={cn(
												"flex items-center gap-2 rounded-lg border border-transparent bg-raised px-1.5 py-1.5 text-xs leading-tight transition-colors hover:border-accent/40",
												c.status === "paid" && "bg-ok-soft",
												c.status === "pending" && iso < today && c.kind !== "cancelBy" && "bg-warn-soft",
												c.status === "skipped" && "opacity-60",
												c.kind === "cancelBy" && "border-dashed border-warn/60 bg-transparent",
												c.projected && "opacity-75",
											)}
										>
											<ServiceTile name={c.subscription} size="sm" />
											<span className="hidden min-w-0 sm:block">
												<span className="block truncate font-medium">{c.subscription}</span>
												<span className="tnum block truncate text-muted">
													{c.kind === "cancelBy"
														? t("last day to cancel")
														: c.kind === "trial"
															? t("trial ends · {0}", money(c.amount))
															: c.kind === "expiry"
																? t("expires · {0}", money(c.amount))
																: money(c.amount)}
												</span>
											</span>
										</Link>
									))}
								</div>
								{dayTotal && (
									<p className="tnum mt-auto pt-1 text-right text-[11px] font-medium text-muted">
										{dayTotal}
									</p>
								)}
							</div>
						);
					})}
				</div>
			</Card>
		</>
	);
}
