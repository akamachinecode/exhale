import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { type ChargeView, useListCharges } from "@/gen/api";
import { cn, money, todayISO } from "@/lib/utils";

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
	const q = useListCharges({ from: b.from, to: b.to });
	const byDay = new Map<string, ChargeView[]>();
	for (const c of q.data?.charges ?? []) byDay.set(c.dueDate, [...(byDay.get(c.dueDate) ?? []), c]);
	const title = new Intl.DateTimeFormat(undefined, {
		month: "long",
		year: "numeric",
		timeZone: "UTC",
	}).format(new Date(Date.UTC(b.y, b.m - 1, 1)));
	const cells = Array.from({ length: Math.ceil((b.offset + b.days) / 7) * 7 }, (_, i) => i - b.offset + 1);

	return (
		<>
			<PageHeader
				title="Calendar"
				description="Every charge, on the day it happens."
				action={
					<div className="flex items-center gap-1">
						<Button
							size="icon"
							variant="ghost"
							aria-label="Previous month"
							onClick={() => navigate({ search: { month: shift(month, -1) } })}
						>
							<ChevronLeft />
						</Button>
						<span className="min-w-36 text-center text-sm font-medium capitalize">{title}</span>
						<Button
							size="icon"
							variant="ghost"
							aria-label="Next month"
							onClick={() => navigate({ search: { month: shift(month, 1) } })}
						>
							<ChevronRight />
						</Button>
						<Button size="sm" className="ml-2" onClick={() => navigate({ search: {} })}>
							Today
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
						return (
							<div
								// biome-ignore lint/suspicious/noArrayIndexKey: fixed grid positions
								key={i}
								className={cn(
									"min-h-24 border-line p-1.5 sm:min-h-28 sm:p-2",
									i % 7 !== 6 && "border-r",
									i < cells.length - 7 && "border-b",
									!inMonth && "bg-raised/40",
								)}
							>
								{inMonth && (
									<span
										className={cn(
											"tnum inline-grid size-6 place-items-center rounded-full text-xs text-muted",
											iso === today && "bg-accent font-semibold text-accent-fg",
										)}
									>
										{day}
									</span>
								)}
								<div className="mt-1 grid gap-1">
									{items.map((c) => (
										<Link
											key={c.id}
											to="/subscriptions/$id"
											params={{ id: c.subscriptionId }}
											title={`${c.subscriptionName} · ${money(c.amount)}`}
											className={cn(
												"flex items-center gap-1.5 rounded-md bg-raised px-1.5 py-1 text-[11px] leading-tight hover:bg-accent-soft",
												c.status === "paid" && "opacity-60",
											)}
										>
											<ServiceTile name={c.subscriptionName} size="sm" />
											<span className="hidden min-w-0 sm:block">
												<span className="block truncate font-medium">{c.subscriptionName}</span>
												<span className="tnum block text-muted">{money(c.amount)}</span>
											</span>
										</Link>
									))}
								</div>
							</div>
						);
					})}
				</div>
			</Card>
		</>
	);
}
