import { Link } from "@tanstack/react-router";
import { ServiceTile } from "@/components/service-tile";
import { SubscriptionStatus } from "@/components/status";
import { Badge } from "@/components/ui/badge";
import type { Breakdown, ChargeList, Screen, SubscriptionView } from "@/gen/api";
import { t, tn } from "@/lib/i18n";
import { cadenceText, cn, date, money } from "@/lib/utils";

// Screens the assistant chose to show (ADR 0019 §4). The model picked the
// kind and which records; every name, date and amount here was read from
// the database by Exhale. Each row opens its record.

export function AssistantScreen({ screen, onOpen }: { screen: Screen; onOpen: () => void }) {
	return (
		<div className="overflow-hidden rounded-lg border border-line">
			{screen.kind === "subscriptions" && <Subscriptions subs={screen.subscriptions ?? []} onOpen={onOpen} />}
			{screen.kind === "charges" && screen.charges && (
				<Charges list={screen.charges} more={screen.more ?? 0} onOpen={onOpen} />
			)}
			{screen.kind === "total" && screen.total && <Total b={screen.total} onOpen={onOpen} />}
		</div>
	);
}

const rowLink = "flex items-center gap-3 px-3.5 py-2.5 text-sm transition-colors hover:bg-raised/60";

function Subscriptions({ subs, onOpen }: { subs: SubscriptionView[]; onOpen: () => void }) {
	return (
		<ul className="divide-y divide-line">
			{subs.map((s) => (
				<li key={s.id}>
					<Link to="/subscriptions/$id" params={{ id: s.id }} onClick={onOpen} className={rowLink}>
						<ServiceTile name={s.name} size="sm" />
						<span className="min-w-0 flex-1">
							<span className="block truncate font-medium">{s.name}</span>
							<span className="block truncate text-xs text-muted">
								{cadenceText(s.cadence)}
								{s.nextCharge && ` · ${date(s.nextCharge, "day")}`}
							</span>
						</span>
						<span className="tnum text-right">
							<span className="block font-medium">{money(s.price)}</span>
							{s.status !== "active" && <SubscriptionStatus status={s.status} />}
						</span>
					</Link>
				</li>
			))}
		</ul>
	);
}

const chargeStatus: Record<string, { label: string; tone: "ok" | "bad" | "neutral" }> = {
	paid: { label: t("Paid"), tone: "ok" },
	failed: { label: t("Failed"), tone: "bad" },
	skipped: { label: t("Skipped"), tone: "neutral" },
};

function Charges({ list, more, onOpen }: { list: ChargeList; more: number; onOpen: () => void }) {
	const charges = list.charges ?? [];
	return (
		<>
			<p className="border-b border-line bg-raised/40 px-3.5 py-2 text-xs text-muted">
				{t("Charges from {0} to {1}", date(list.from, "day"), date(list.to, "day"))}
			</p>
			{charges.length === 0 ? (
				<p className="px-3.5 py-3 text-sm text-muted">{t("No charges in this period.")}</p>
			) : (
				<ul className="divide-y divide-line">
					{charges.map((c) => {
						const st = chargeStatus[c.status];
						return (
							<li key={c.id}>
								<Link
									to="/subscriptions/$id"
									params={{ id: c.subscriptionId }}
									onClick={onOpen}
									className={rowLink}
								>
									<span className="tnum w-20 shrink-0 text-xs text-muted">{date(c.dueDate, "day")}</span>
									<span className="min-w-0 flex-1 truncate font-medium">{c.subscriptionName}</span>
									{st && <Badge tone={st.tone}>{st.label}</Badge>}
									<span className="tnum font-medium">{money(c.amount)}</span>
								</Link>
							</li>
						);
					})}
				</ul>
			)}
			{more > 0 && (
				<p className="border-t border-line px-3.5 py-2 text-xs text-muted">
					{tn(more, "{n} more did not fit.", "{n} more did not fit.")}
				</p>
			)}
		</>
	);
}

const breakdowns: Record<string, { heading: string; none: string }> = {
	category: { heading: t("By category"), none: t("No category") },
	payment_method: { heading: t("By payment method"), none: t("No payment method") },
	subscription: { heading: t("By subscription"), none: "" },
};

function Total({ b, onOpen }: { b: Breakdown; onOpen: () => void }) {
	const rows = b.rows ?? [];
	const by = breakdowns[b.by] ?? breakdowns.category;
	const top = Math.max(1, ...rows.map((r) => r.amount.minor));
	return (
		<>
			<div className="border-b border-line px-3.5 py-3">
				<p className="text-xs text-muted">{b.period === "year" ? t("Per year") : t("Per month")}</p>
				<p className="tnum text-2xl font-semibold tracking-tight">{money(b.total)}</p>
				{b.converted && <p className="text-xs text-faint">{t("Other currencies converted at ECB rates.")}</p>}
				{(b.unconverted ?? []).length > 0 && (
					<p className="text-xs text-muted">
						{t("Left out, no exchange rate: {0}", (b.unconverted ?? []).join(", "))}
					</p>
				)}
			</div>
			<p className="px-3.5 pt-2.5 text-xs text-muted">{by.heading}</p>
			<ul className="px-3.5 pb-2">
				{rows.map((r) => {
					const label = r.label || by.none;
					const bar = (
						<span className="block h-1 rounded-full bg-raised">
							<span
								className="block h-1 rounded-full bg-fg/60"
								style={{ width: `${Math.max(2, (r.amount.minor / top) * 100)}%` }}
							/>
						</span>
					);
					const body = (
						<>
							<span className="flex items-baseline justify-between gap-3 text-sm">
								<span className={cn("truncate", !r.label && "text-muted")}>{label}</span>
								<span className="tnum shrink-0 font-medium">{money(r.amount)}</span>
							</span>
							{bar}
						</>
					);
					return (
						<li key={r.subscriptionId || label} className="py-1.5">
							{r.subscriptionId ? (
								<Link
									to="/subscriptions/$id"
									params={{ id: r.subscriptionId }}
									onClick={onOpen}
									className="grid gap-1 hover:underline"
								>
									{body}
								</Link>
							) : (
								<span className="grid gap-1">{body}</span>
							)}
						</li>
					);
				})}
			</ul>
		</>
	);
}
