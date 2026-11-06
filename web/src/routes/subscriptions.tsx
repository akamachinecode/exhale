import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowDown, ArrowUp, Plus, Search } from "lucide-react";
import { useState } from "react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { SubscriptionStatus } from "@/components/status";
import { NewSubscription } from "@/components/subscription-form";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/field";
import { type SubscriptionView, useListSubscriptions, useSpendingSummary } from "@/gen/api";
import { cn, date, money, sumMain } from "@/lib/utils";

const tabs = [
	{ value: undefined, label: "All" },
	{ value: "active", label: "Active" },
	{ value: "paused", label: "Paused" },
	{ value: "cancelled", label: "Cancelled" },
] as const;

type Sort = "name" | "next" | "monthly";
type SearchParams = { status?: string; q?: string; sort?: string; desc?: boolean };

const sorters: Record<Sort, (a: SubscriptionView, b: SubscriptionView) => number> = {
	name: (a, b) => a.name.localeCompare(b.name),
	next: (a, b) => (a.nextCharge ?? "9999").localeCompare(b.nextCharge ?? "9999"),
	monthly: (a, b) => a.monthlyCost.minor - b.monthlyCost.minor,
};

function useTotals() {
	const summary = useSpendingSummary().data;
	return (subs: SubscriptionView[]) => {
		const active = subs.filter((s) => s.status === "active").map((s) => s.monthlyCost);
		if (active.length === 0) return "";
		return money(sumMain(active, summary?.rates, summary?.mainCurrency ?? active[0].currency));
	};
}

export function Subscriptions() {
	const search = useSearch({ from: "/subscriptions/" }) as SearchParams;
	const navigate = useNavigate({ from: "/subscriptions/" });
	const set = (patch: Partial<SearchParams>, replace = false) =>
		navigate({ search: (s: SearchParams) => ({ ...s, ...patch }), replace });
	const [creating, setCreating] = useState(false);
	const totals = useTotals();
	const list = useListSubscriptions({});
	const all = list.data?.subscriptions ?? [];
	const sort = (search.sort as Sort) in sorters ? (search.sort as Sort) : "next";
	const q = search.q?.toLowerCase() ?? "";
	const subs = all
		.filter((s) => !search.status || s.status === search.status)
		.filter((s) => !q || s.name.toLowerCase().includes(q))
		.sort((a, b) => (search.desc ? -1 : 1) * sorters[sort](a, b));
	const count = (status?: string) => all.filter((s) => !status || s.status === status).length;
	const active = all.filter((s) => s.status === "active");

	const header = (key: Sort, label: string, className?: string) => (
		<th
			className={cn("px-3 py-2.5 font-medium", className)}
			aria-sort={sort === key ? (search.desc ? "descending" : "ascending") : "none"}
		>
			<button
				type="button"
				onClick={() => set({ sort: key, desc: sort === key ? !search.desc : false })}
				className={cn("inline-flex items-center gap-1 hover:text-fg", sort === key && "text-fg")}
			>
				{label}
				{sort === key && (search.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
			</button>
		</th>
	);

	return (
		<>
			<PageHeader
				title="Subscriptions"
				description={
					active.length
						? `${active.length} active · ${totals(active)} per month`
						: "Everything you pay for on repeat."
				}
				action={
					<Button variant="primary" onClick={() => setCreating(true)}>
						<Plus /> New subscription
					</Button>
				}
			/>
			<NewSubscription open={creating} onOpenChange={setCreating} />
			<div className="mb-4 flex flex-wrap items-center justify-between gap-3">
				<div
					className="flex gap-1 overflow-x-auto rounded-lg bg-raised p-1"
					role="tablist"
					aria-label="Filter by status"
				>
					{tabs.map((t) => (
						<button
							key={t.label}
							type="button"
							role="tab"
							aria-selected={search.status === t.value}
							onClick={() => set({ status: t.value })}
							className={cn(
								"flex items-center gap-1.5 rounded-md px-3 py-1.5 text-[13px] font-medium text-muted transition-colors",
								search.status === t.value && "bg-surface text-fg shadow-sm",
							)}
						>
							{t.label}
							<span className="tnum text-xs text-faint">{count(t.value)}</span>
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
						onChange={(e) => set({ q: e.target.value || undefined }, true)}
					/>
				</div>
			</div>
			<Card className="overflow-hidden">
				{subs.length === 0 ? (
					<p className="px-5 py-12 text-center text-sm text-muted">
						{list.isLoading ? "Loading…" : "Nothing matches."}
					</p>
				) : (
					<table className="w-full text-sm">
						<thead className="border-b border-line bg-raised/50 text-left text-xs text-muted">
							<tr>
								{header("name", "Service", "pl-5")}
								{header("next", "Next charge", "hidden md:table-cell")}
								<th className="hidden px-3 py-2.5 font-medium lg:table-cell">Payment method</th>
								<th className="hidden px-3 py-2.5 text-right font-medium sm:table-cell">Price</th>
								{header("monthly", "Per month", "pr-5 text-right")}
							</tr>
						</thead>
						<tbody className="divide-y divide-line">
							{subs.map((s) => (
								<tr
									key={s.id}
									className={cn(
										"group transition-colors hover:bg-raised/50",
										s.status !== "active" && "text-muted",
									)}
								>
									<td className="py-3 pr-3 pl-5">
										<Link to="/subscriptions/$id" params={{ id: s.id }} className="flex items-center gap-3">
											<ServiceTile name={s.name} />
											<span className="min-w-0">
												<span className="flex items-center gap-2">
													<span className="truncate font-medium text-fg group-hover:underline">{s.name}</span>
													{s.status !== "active" && <SubscriptionStatus status={s.status} />}
												</span>
												<span className="block truncate text-xs text-muted">
													{s.cadence.replace("every 1 ", "every ")}
													{s.categoryName && ` · ${s.categoryName}`}
												</span>
											</span>
										</Link>
									</td>
									<td className="hidden px-3 py-3 text-muted md:table-cell">
										{s.nextCharge ? date(s.nextCharge, "day") : "—"}
									</td>
									<td className="hidden px-3 py-3 text-muted lg:table-cell">{s.paymentMethod ?? "—"}</td>
									<td className="tnum hidden px-3 py-3 text-right text-muted sm:table-cell">
										{money(s.price)}
									</td>
									<td className="tnum py-3 pr-5 pl-3 text-right font-medium">{money(s.monthlyCost)}</td>
								</tr>
							))}
						</tbody>
						<tfoot className="border-t border-line bg-raised/40 text-xs text-muted">
							<tr>
								<td className="py-2.5 pl-5" colSpan={4}>
									{subs.length} shown
								</td>
								<td className="tnum py-2.5 pr-5 text-right font-medium text-fg">{totals(subs) || "—"}</td>
							</tr>
						</tfoot>
					</table>
				)}
			</Card>
		</>
	);
}
