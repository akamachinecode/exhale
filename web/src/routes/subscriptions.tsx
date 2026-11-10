import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowDown, ArrowUp, Plus, Search } from "lucide-react";
import { useState } from "react";
import { PageHeader } from "@/components/layout";
import { ServiceTile } from "@/components/service-tile";
import { SubscriptionStatus } from "@/components/status";
import { NewSubscription } from "@/components/subscription-form";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input, Select } from "@/components/ui/field";
import { type SubscriptionView, useGetHousehold, useListSubscriptions, useSpendingSummary } from "@/gen/api";
import { t } from "@/lib/i18n";
import { cadenceText, cn, date, money, sumMain } from "@/lib/utils";

const tabs = [
	{ value: undefined, label: t("All") },
	{ value: "active", label: t("Active") },
	{ value: "paused", label: t("Paused") },
	{ value: "cancelled", label: t("Cancelled") },
] as const;

type Sort = "name" | "next" | "monthly";
type SearchParams = { status?: string; q?: string; sort?: string; desc?: boolean; cat?: string; pm?: string };

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
	const household = useGetHousehold();
	// "-" selects the ones without a category or payment method.
	const matches = (want: string | undefined, id: string | undefined) =>
		!want || (want === "-" ? !id : id === want);
	const subs = all
		.filter((s) => !search.status || s.status === search.status)
		.filter((s) => !q || s.name.toLowerCase().includes(q))
		.filter((s) => matches(search.cat, s.categoryId) && matches(search.pm, s.paymentMethodId))
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
				title={t("Subscriptions")}
				description={
					active.length
						? t("{0} active · {1} per month", active.length, totals(active))
						: t("Everything you pay for on repeat.")
				}
				action={
					<Button variant="primary" onClick={() => setCreating(true)}>
						<Plus /> {t("New subscription")}
					</Button>
				}
			/>
			<NewSubscription open={creating} onOpenChange={setCreating} />
			<div className="mb-4 flex flex-wrap items-center justify-between gap-3">
				<div
					className="flex gap-1 overflow-x-auto rounded-lg bg-raised p-1"
					role="tablist"
					aria-label={t("Filter by status")}
				>
					{tabs.map((tab) => (
						<button
							key={tab.label}
							type="button"
							role="tab"
							aria-selected={search.status === tab.value}
							onClick={() => set({ status: tab.value })}
							className={cn(
								"flex items-center gap-1.5 rounded-md px-3 py-1.5 text-[13px] font-medium text-muted transition-colors",
								search.status === tab.value && "bg-surface text-fg shadow-sm",
							)}
						>
							{tab.label}
							<span className="tnum text-xs text-faint">{count(tab.value)}</span>
						</button>
					))}
				</div>
				<div className="flex w-full flex-wrap gap-2 sm:w-auto">
					<Select
						className="w-auto min-w-36 flex-1"
						aria-label={t("Filter by category")}
						value={search.cat ?? ""}
						onChange={(e) => set({ cat: e.target.value || undefined }, true)}
					>
						<option value="">{t("All categories")}</option>
						{(household.data?.categories ?? []).map((c) => (
							<option key={c.id} value={c.id}>
								{c.name}
							</option>
						))}
						<option value="-">{t("No category")}</option>
					</Select>
					<Select
						className="w-auto min-w-36 flex-1"
						aria-label={t("Filter by payment method")}
						value={search.pm ?? ""}
						onChange={(e) => set({ pm: e.target.value || undefined }, true)}
					>
						<option value="">{t("All payment methods")}</option>
						{(household.data?.paymentMethods ?? []).map((p) => (
							<option key={p.id} value={p.id}>
								{p.name}
								{p.last4 ? ` ••${p.last4}` : ""}
							</option>
						))}
						<option value="-">{t("No payment method")}</option>
					</Select>
				</div>
				<div className="relative w-full sm:w-64">
					<Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
					<Input
						className="pl-9"
						placeholder={t("Search")}
						aria-label={t("Search subscriptions")}
						value={search.q ?? ""}
						onChange={(e) => set({ q: e.target.value || undefined }, true)}
					/>
				</div>
			</div>
			<Card className="overflow-hidden">
				{subs.length === 0 ? (
					<p className="px-5 py-12 text-center text-sm text-muted">
						{list.isLoading ? t("Loading…") : t("Nothing matches.")}
					</p>
				) : (
					<table className="w-full text-sm">
						<thead className="border-b border-line bg-raised/50 text-left text-xs text-muted">
							<tr>
								{header("name", t("Service"), "pl-5")}
								{header("next", t("Next charge"), "hidden md:table-cell")}
								<th className="hidden px-3 py-2.5 font-medium lg:table-cell">{t("Payment method")}</th>
								<th className="hidden px-3 py-2.5 text-right font-medium sm:table-cell">{t("Price")}</th>
								{header("monthly", t("Per month"), "pr-5 text-right")}
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
											<ServiceTile name={s.name} logo={s.logo} />
											<span className="min-w-0">
												<span className="flex items-center gap-2">
													<span className="truncate font-medium text-fg group-hover:underline">{s.name}</span>
													{s.status !== "active" && <SubscriptionStatus status={s.status} />}
												</span>
												<span className="block truncate text-xs text-muted">
													{cadenceText(s.cadence)}
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
									{subs.length} {t("shown")}
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
