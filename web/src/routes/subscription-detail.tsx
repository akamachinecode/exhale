import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ArrowLeft, Copy, ExternalLink, Pause, Pencil, Play, Trash2, XCircle } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ChargeActions } from "@/components/charge-actions";
import { Documents, LogoControl } from "@/components/documents";
import { ServiceTile } from "@/components/service-tile";
import { ChargeStatus, SubscriptionStatus } from "@/components/status";
import { reminderLabel, SubscriptionDialog } from "@/components/subscription-form";
import { TagChips } from "@/components/tags";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import {
	type ChargeView,
	useDeleteSubscription,
	useGetHousehold,
	useGetSubscription,
	useSetSubscriptionStatus,
	useUndo,
	useUpdateSubscription,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";
import { billingDayText, cadenceText, date, money, weekendText } from "@/lib/utils";

export function SubscriptionDetail() {
	const { id } = useParams({ from: "/subscriptions/$id" });
	const q = useGetSubscription(id);
	const qc = useQueryClient();
	const navigate = useNavigate();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const household = useGetHousehold();
	const [editing, setEditing] = useState(false);
	const [copying, setCopying] = useState(false);
	const usage = useUpdateSubscription({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const onError = (e: unknown) => toast.error(e instanceof ApiError ? e.detail : t("Something went wrong"));
	const status = useSetSubscriptionStatus({ mutation: { onSuccess: () => qc.invalidateQueries(), onError } });
	const del = useDeleteSubscription({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				navigate({ to: "/subscriptions", search: {} });
				toast.success(t("Subscription deleted"), {
					action: { label: t("Undo"), onClick: () => undo.mutate({ data: { auditId: r.auditId } }) },
				});
			},
			onError,
		},
	});
	if (q.isLoading) return <p className="text-sm text-muted">{t("Loading…")}</p>;
	if (!q.data) return <p className="text-sm text-muted">{t("Subscription not found.")}</p>;
	const { subscription: s, upcoming, history, priceHistory, paid } = q.data;
	const act = (action: "pause" | "resume" | "cancel_at_period_end") =>
		status.mutate({ data: { id: s.id, action } });
	const prices = priceHistory ?? [];
	const names = new Map((household.data?.members ?? []).map((m) => [m.id, m.name]));
	const shares = s.shares ?? [];
	const totalWeight = shares.reduce((a, x) => a + x.weight, 0);
	const plan = s.planSwitch;
	const per = (unit?: string) => (unit === "month" ? t("per month") : t("per year"));
	const rule = s.schedule.versions?.at(-1)?.rule;

	return (
		<>
			<Link
				to="/subscriptions"
				search={{}}
				className="mb-4 inline-flex items-center gap-1.5 text-[13px] text-muted hover:text-fg"
			>
				<ArrowLeft className="size-3.5" /> {t("Subscriptions")}
			</Link>
			<div className="mb-6 flex flex-wrap items-center gap-4">
				<ServiceTile name={s.name} logo={s.logo} size="lg" />
				<div className="min-w-0 flex-1">
					<div className="flex flex-wrap items-center gap-2">
						<h1 className="truncate text-[22px] font-semibold tracking-tight">{s.name}</h1>
						<SubscriptionStatus status={s.status} />
						{s.inTrial && <Badge tone="accent">{t("Free trial")}</Badge>}
						{s.expires && <Badge>{t("Expires unless renewed")}</Badge>}
					</div>
					<p className="mt-0.5 text-sm text-muted">
						<span className="tnum font-medium text-fg">{money(s.price)}</span> {cadenceText(s.cadence)}
						{s.status === "cancelled" &&
							s.serviceUntil &&
							t(" · service until {0}", date(s.serviceUntil, "long"))}
					</p>
					<TagChips tags={s.tags} className="mt-1.5" />
				</div>
				<div className="flex flex-wrap gap-2">
					<Button size="sm" onClick={() => setEditing(true)}>
						<Pencil /> {t("Edit")}
					</Button>
					<Button size="sm" onClick={() => setCopying(true)}>
						<Copy /> {t("Duplicate")}
					</Button>
					{s.status === "active" && (
						<Button size="sm" onClick={() => act("pause")}>
							<Pause /> {t("Pause")}
						</Button>
					)}
					{s.status !== "active" && (
						<Button size="sm" onClick={() => act("resume")}>
							<Play /> {t("Resume")}
						</Button>
					)}
					{s.status === "active" && (
						<Button size="sm" onClick={() => act("cancel_at_period_end")}>
							<XCircle /> {t("Cancel at period end")}
						</Button>
					)}
					<Button
						size="sm"
						variant="danger"
						aria-label={t("Delete")}
						onClick={() => del.mutate({ data: { id: s.id } })}
					>
						<Trash2 />
					</Button>
				</div>
			</div>

			<SubscriptionDialog open={editing} onOpenChange={setEditing} subscription={s} />
			<SubscriptionDialog open={copying} onOpenChange={setCopying} copyOf={s} />
			{s.status === "active" && plan && plan.yearlySaving.minor > 0 && s.altPrice && (
				<Card className="mb-6 flex flex-wrap items-center gap-3 px-5 py-3.5 text-sm">
					<span className="flex-1">
						<span className="font-medium">
							{t(
								"Switching to {0} {1} saves {2} a year.",
								money(s.altPrice.price),
								per(s.altPrice.unit),
								money(plan.yearlySaving),
							)}
						</span>{" "}
						<span className="text-muted">
							{t(
								"Now {0} a year; the other price comes to {1}.",
								money(plan.currentYearly),
								money(plan.otherYearly),
							)}
						</span>
					</span>
					{s.cancelUrl && (
						<a
							href={s.cancelUrl}
							target="_blank"
							rel="noreferrer"
							className="inline-flex items-center gap-1.5 text-[13px] font-medium hover:underline"
						>
							{t("Manage plan")} <ExternalLink className="size-3.5" />
						</a>
					)}
				</Card>
			)}
			{s.status === "active" && s.cancelBy && (
				<Card className="mb-6 flex flex-wrap items-center gap-3 border-warn/40 px-5 py-3.5 text-sm">
					<span className="flex-1">
						<span className="font-medium">
							{t("Cancel by")} {date(s.cancelBy, "long")}
						</span>{" "}
						<span className="text-muted">
							{t("or it renews: it needs")} {s.noticeDays} {t("days' notice.")}
						</span>
					</span>
					{s.cancelUrl && (
						<a
							href={s.cancelUrl}
							target="_blank"
							rel="noreferrer"
							className="inline-flex items-center gap-1.5 text-[13px] font-medium hover:underline"
						>
							{t("Cancel page")} <ExternalLink className="size-3.5" />
						</a>
					)}
				</Card>
			)}
			{s.status === "active" && s.askUsage && (
				<Card className="mb-6 flex flex-wrap items-center gap-3 px-5 py-3.5 text-sm">
					<span className="flex-1">
						{t("Still using it?")}{" "}
						<span className="text-muted">
							{t("It costs")} {money(s.yearlyCost)} {t("a year.")}
						</span>
					</span>
					<Button size="sm" onClick={() => usage.mutate({ data: { id: s.id, using: true } })}>
						{t("Yes")}
					</Button>
					<Button size="sm" onClick={() => usage.mutate({ data: { id: s.id, using: false } })}>
						{t("Not really")}
					</Button>
				</Card>
			)}
			<div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_300px]">
				<div className="grid min-w-0 content-start gap-6">
					<Card>
						<CardHeader
							title={t("Upcoming charges")}
							description={t("Move one charge for a one-off delay, or the series from that charge on.")}
						/>
						<Charges charges={upcoming ?? []} empty={t("No upcoming charges.")} />
					</Card>
					<Card>
						<CardHeader title={t("History")} />
						<Charges charges={history ?? []} empty={t("No past charges yet.")} />
					</Card>
					<Documents subscription={s} charges={[...(history ?? []), ...(upcoming ?? [])]} />
				</div>
				<div className="grid content-start gap-6">
					<Card>
						<CardHeader title={t("Details")} />
						<dl className="grid gap-3 border-t border-line px-5 py-4 text-sm">
							<Detail
								label={t("Per month · per year")}
								value={`${money(s.monthlyCost)} · ${money(s.yearlyCost)}`}
							/>
							<Detail
								label={s.expires ? t("Expires") : s.inTrial ? t("Trial ends") : t("Next charge")}
								value={date(s.nextCharge, "long")}
							/>
							{s.nextPrice && (
								<Detail
									label={t("Price change")}
									value={`${money(s.nextPrice.price)} from ${date(s.nextPrice.from, "long")}`}
								/>
							)}
							{s.altPrice && plan && (
								<Detail
									label={t("Also offered at")}
									value={`${money(s.altPrice.price)} ${per(s.altPrice.unit)} · ${
										plan.yearlySaving.minor > 0
											? t("saves {0} a year", money(plan.yearlySaving))
											: plan.yearlySaving.minor < 0
												? t(
														"{0} a year more than now",
														money({ ...plan.yearlySaving, minor: -plan.yearlySaving.minor }),
													)
												: t("costs the same")
									}`}
								/>
							)}
							<Detail label={t("Payment method")} value={s.paymentMethod ?? "—"} />
							<Detail label={t("Category")} value={s.categoryName ?? "—"} />
							<Detail label={t("Paid by")} value={s.payerName ?? "—"} />
							{shares.length > 0 && (
								<Detail
									label={t("Split")}
									value={shares
										.map(
											(x) => `${names.get(x.memberId) ?? "?"} ${Math.round((x.weight / totalWeight) * 100)}%`,
										)
										.join(" · ")}
								/>
							)}
							{rule?.dayRule && rule.dayRule !== "day" && (
								<Detail label={t("Charged on")} value={billingDayText(rule.dayRule, rule.anchor)} />
							)}
							{rule?.weekendShift && rule.weekendShift !== "none" && (
								<Detail label={t("On weekends")} value={weekendText(rule.weekendShift)} />
							)}
							{s.account && <Detail label={t("Account")} value={s.account} />}
							{s.startedOn && <Detail label={t("Subscribed since")} value={date(s.startedOn, "long")} />}
							{s.schedule.until && s.status !== "cancelled" && (
								<Detail label={t("Ends on")} value={date(s.schedule.until, "long")} />
							)}
							<Detail
								label={t("Reminders")}
								value={
									(s.reminders.daysBefore ?? []).length
										? `${(s.reminders.daysBefore ?? []).map((d) => reminderLabel(d).toLowerCase()).join(", ")} · ${String(s.reminders.hour).padStart(2, "0")}:${String(s.reminders.minute).padStart(2, "0")}`
										: "Off"
								}
							/>
							{s.visibility === "private" && <Detail label={t("Visibility")} value="Private" />}
							{s.cancelUrl && (
								<div className="grid gap-0.5">
									<dt className="text-xs text-muted">{t("Cancel or manage")}</dt>
									<dd>
										<a
											href={s.cancelUrl}
											target="_blank"
											rel="noreferrer"
											className="inline-flex items-center gap-1.5 break-all hover:underline"
										>
											{s.cancelUrl.replace(/^https?:\/\//, "")} <ExternalLink className="size-3.5 shrink-0" />
										</a>
									</dd>
								</div>
							)}
							{s.notes && <Detail label={t("Notes")} value={s.notes} />}
							<LogoControl subscription={s} />
						</dl>
					</Card>
					<Card>
						<CardHeader
							title={t("What it has cost")}
							description={
								paid.count
									? tn(
											paid.count,
											"{n} charge paid since {0}",
											"{n} charges paid since {0}",
											date(paid.since, "long"),
										)
									: t("Nothing marked as paid yet.")
							}
						/>
						<div className="border-t border-line px-5 py-4">
							<p className="tnum text-2xl font-semibold tracking-tight">{money(paid.total)}</p>
							{prices.length > 1 && (
								<ol
									className="mt-4 grid gap-2 border-t border-line pt-3 text-sm"
									aria-label={t("Price history")}
								>
									{prices.map((p) => (
										<li key={p.from} className="flex items-center gap-2">
											<span className="flex-1 text-muted">
												{p.scheduled ? t("From ") : ""}
												{date(p.from, "long")}
											</span>
											{p.changePercent && (
												<span
													className={`tnum text-xs ${p.changePercent.startsWith("+") ? "text-warn" : "text-ok"}`}
												>
													{p.changePercent}%
												</span>
											)}
											<span className="tnum font-medium">{money(p.price)}</span>
										</li>
									))}
								</ol>
							)}
						</div>
					</Card>
				</div>
			</div>
		</>
	);
}

function Detail({ label, value }: { label: string; value: string }) {
	return (
		<div className="grid gap-0.5">
			<dt className="text-xs text-muted">{label}</dt>
			<dd className="tnum whitespace-pre-line">{value}</dd>
		</div>
	);
}

function Charges({ charges, empty }: { charges: ChargeView[]; empty: string }) {
	if (charges.length === 0)
		return <p className="border-t border-line px-5 py-8 text-center text-sm text-muted">{empty}</p>;
	return (
		<ul className="divide-y divide-line border-t border-line">
			{charges.map((c) => (
				<li
					key={c.id}
					className="group flex flex-wrap items-center gap-x-3 gap-y-2 px-5 py-3 text-sm sm:flex-nowrap"
				>
					<div className="min-w-0 flex-1">
						<p className="font-medium">{date(c.dueDate, "long")}</p>
						{c.delayed && (
							<p className="text-xs text-muted">
								{t("moved from")} {date(c.originalDate)}
							</p>
						)}
						{c.chargedAmount && c.chargedAmount.minor !== c.amount.minor && (
							<p className="text-xs text-muted">
								{t("charged")} {money(c.chargedAmount)}
							</p>
						)}
						{c.kind === "trial" && c.status === "pending" && (
							<p className="text-xs text-muted">{t("the free trial ends")}</p>
						)}
						{c.cancelBy && !c.cancelPassed && c.status === "pending" && (
							<p className="text-xs text-muted">
								{t("cancel by")} {date(c.cancelBy)}
							</p>
						)}
					</div>
					<ChargeStatus charge={c} />
					<span className="tnum w-20 text-right font-medium">{money(c.amount)}</span>
					<ChargeActions charge={c} compact persistent />
				</li>
			))}
		</ul>
	);
}
