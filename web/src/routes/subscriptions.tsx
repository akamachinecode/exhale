import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Plus, Search } from "lucide-react";
import { useState } from "react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { SubscriptionStatus } from "@/components/status";
import { NewSubscription } from "@/components/subscription-form";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/field";
import { useListSubscriptions } from "@/gen/api";
import { cn, date, money } from "@/lib/utils";

const tabs = [
	{ value: undefined, label: "All" },
	{ value: "active", label: "Active" },
	{ value: "paused", label: "Paused" },
	{ value: "cancelled", label: "Cancelled" },
] as const;

export function Subscriptions() {
	const search = useSearch({ from: "/subscriptions/" });
	const navigate = useNavigate({ from: "/subscriptions/" });
	const [creating, setCreating] = useState(false);
	const list = useListSubscriptions({ status: search.status, query: search.q });
	const subs = list.data?.subscriptions ?? [];
	return (
		<>
			<PageHeader
				title="Subscriptions"
				description="Everything you pay for on repeat."
				action={
					<Button variant="primary" onClick={() => setCreating(true)}>
						<Plus /> New subscription
					</Button>
				}
			/>
			<NewSubscription open={creating} onOpenChange={setCreating} />
			<div className="mb-4 flex flex-wrap items-center justify-between gap-3">
				<div className="flex gap-1 rounded-lg bg-raised p-1" role="tablist" aria-label="Filter by status">
					{tabs.map((t) => (
						<button
							key={t.label}
							type="button"
							role="tab"
							aria-selected={search.status === t.value}
							onClick={() =>
								navigate({ search: (s: { status?: string; q?: string }) => ({ ...s, status: t.value }) })
							}
							className={cn(
								"rounded-md px-3 py-1.5 text-[13px] font-medium text-muted transition-colors",
								search.status === t.value && "bg-surface text-fg shadow-sm",
							)}
						>
							{t.label}
						</button>
					))}
				</div>
				<div className="relative w-full sm:w-64">
					<Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
					<Input
						className="pl-9"
						placeholder="Search"
						aria-label="Search subscriptions"
						value={search.q ?? ""}
						onChange={(e) =>
							navigate({
								search: (s: { status?: string; q?: string }) => ({ ...s, q: e.target.value || undefined }),
								replace: true,
							})
						}
					/>
				</div>
			</div>
			<Card className="overflow-hidden">
				{subs.length === 0 ? (
					<p className="px-5 py-12 text-center text-sm text-muted">
						{list.isLoading ? "Loading…" : "No subscriptions here yet."}
					</p>
				) : (
					<table className="w-full text-sm">
						<thead className="border-b border-line bg-raised/50 text-left text-xs text-muted">
							<tr>
								<th className="px-5 py-2.5 font-medium">Service</th>
								<th className="hidden px-3 py-2.5 font-medium md:table-cell">Next charge</th>
								<th className="hidden px-3 py-2.5 font-medium lg:table-cell">Payment method</th>
								<th className="hidden px-3 py-2.5 font-medium sm:table-cell">Status</th>
								<th className="px-5 py-2.5 text-right font-medium">Price</th>
							</tr>
						</thead>
						<tbody className="divide-y divide-line">
							{subs.map((s) => (
								<tr key={s.id} className="group transition-colors hover:bg-raised/50">
									<td className="px-5 py-3">
										<Link to="/subscriptions/$id" params={{ id: s.id }} className="flex items-center gap-3">
											<ServiceTile name={s.name} />
											<span className="min-w-0">
												<span className="block truncate font-medium group-hover:underline">{s.name}</span>
												<span className="block truncate text-xs text-muted">
													{s.cadence}
													{s.categoryName && ` · ${s.categoryName}`}
												</span>
											</span>
										</Link>
									</td>
									<td className="hidden px-3 py-3 text-muted md:table-cell">
										{s.nextCharge ? date(s.nextCharge, "day") : "—"}
									</td>
									<td className="hidden px-3 py-3 text-muted lg:table-cell">{s.paymentMethod ?? "—"}</td>
									<td className="hidden px-3 py-3 sm:table-cell">
										<SubscriptionStatus status={s.status} />
									</td>
									<td className="px-5 py-3 text-right">
										<span className="tnum block font-medium">{money(s.price)}</span>
										<span className="tnum block text-xs text-muted">{money(s.monthlyCost)}/mo</span>
									</td>
								</tr>
							))}
						</tbody>
					</table>
				)}
			</Card>
		</>
	);
}
