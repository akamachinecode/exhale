import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ServiceTile } from "@/components/service-tile";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Select } from "@/components/ui/field";
import {
	type StoppedSpend,
	useAuthSession,
	useDismissDuplicate,
	useGetHousehold,
	useReassignPaymentMethod,
	useUpdateHousehold,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";
import { date, money } from "@/lib/utils";

// Reminders that end in an answer: how often "was it charged?" repeats,
// moving a card's subscriptions in one step, saying a duplicate is fine,
// and what cancelling saved.

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

/** How often an unanswered "was it charged?" comes back (owners can change it). */
export function AskAgain() {
	const household = useGetHousehold();
	const session = useAuthSession();
	const qc = useQueryClient();
	const update = useUpdateHousehold();
	const h = household.data?.household;
	if (!h) return null;
	const editable = session.data?.user?.role === "owner";
	const save = async (data: { confirmTimes?: number; confirmEveryDays?: number }) => {
		try {
			await update.mutateAsync({ data });
			await qc.invalidateQueries();
			toast.success(t("Saved"));
		} catch (err) {
			toast.error(errText(err, t("Could not save")));
		}
	};
	return (
		<div className="flex flex-wrap items-center gap-2 text-sm">
			<span aria-hidden>{t("If nobody says whether a charge went through, ask")}</span>
			<Select
				aria-label={t("How many times to ask")}
				className="w-auto"
				value={h.confirmTimes}
				disabled={!editable}
				onChange={(e) => save({ confirmTimes: Number(e.target.value) })}
			>
				{[...new Set([1, 2, 3, 5, h.confirmTimes])]
					.sort((a, b) => a - b)
					.map((n) => (
						<option key={n} value={n}>
							{n === 1 ? t("once") : tn(n, "{n} time", "{n} times")}
						</option>
					))}
			</Select>
			{h.confirmTimes > 1 && (
				<Select
					aria-label={t("How often to ask")}
					className="w-auto"
					value={h.confirmEveryDays}
					disabled={!editable}
					onChange={(e) => save({ confirmEveryDays: Number(e.target.value) })}
				>
					{[...new Set([1, 2, 3, 7, h.confirmEveryDays])]
						.sort((a, b) => a - b)
						.map((d) => (
							<option key={d} value={d}>
								{d === 1
									? t("every day")
									: d === 7
										? t("every week")
										: tn(d, "every {n} day", "every {n} days")}
							</option>
						))}
				</Select>
			)}
		</div>
	);
}

/** "Move them to another card": every subscription of a payment method, in one step. */
export function MoveSubscriptions({ fromId, count }: { fromId: string; count: number }) {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const move = useReassignPaymentMethod();
	const others = (household.data?.paymentMethods ?? []).filter((p) => p.id !== fromId);
	const [to, setTo] = useState("");
	if (count === 0) return null;
	if (others.length === 0)
		return (
			<p className="mt-2 text-xs text-muted">
				{t("Add the new card in Settings, then move them here in one step.")}
			</p>
		);
	const target = to || others[0].id;
	const run = async () => {
		try {
			const res = await move.mutateAsync({ data: { fromId, toId: target } });
			await qc.invalidateQueries();
			toast.success(tn(res.moved, "Moved {n} subscription", "Moved {n} subscriptions"));
		} catch (err) {
			toast.error(errText(err, t("Could not move them")));
		}
	};
	return (
		<div className="mt-2 flex flex-wrap items-center gap-2">
			<Select
				aria-label={t("Move them to")}
				className="h-8 w-auto text-xs"
				value={target}
				onChange={(e) => setTo(e.target.value)}
			>
				{others.map((p) => (
					<option key={p.id} value={p.id}>
						{p.name}
						{p.last4 && ` ••${p.last4}`}
					</option>
				))}
			</Select>
			<Button size="sm" disabled={move.isPending} onClick={run}>
				{tn(count, "Move it there", "Move all {n} there")}
			</Button>
		</div>
	);
}

/** "That's fine": paying for the service twice is on purpose. */
export function DuplicateIsFine({ groupKey }: { groupKey: string }) {
	const qc = useQueryClient();
	const dismiss = useDismissDuplicate();
	return (
		<Button
			size="sm"
			variant="ghost"
			disabled={dismiss.isPending}
			onClick={async () => {
				try {
					await dismiss.mutateAsync({ data: { key: groupKey } });
					await qc.invalidateQueries();
					toast.success(t("Noted. It won't come up again."));
				} catch (err) {
					toast.error(errText(err, t("Could not save")));
				}
			}}
		>
			{t("That's fine")}
		</Button>
	);
}

/** What the cancelled subscriptions no longer cost. */
export function StoppedCard({ stopped }: { stopped?: StoppedSpend }) {
	const subs = stopped?.subscriptions ?? [];
	if (!stopped || stopped.count === 0) return null;
	return (
		<Card className="mt-6">
			<CardHeader
				title={t("You stopped paying {0} a year", money(stopped.yearly))}
				description={t(
					"What the subscriptions you cancelled would cost now. Replaced ones (the same service is still active) are left out.",
				)}
			/>
			<ul className="divide-y divide-line border-t border-line">
				{subs.map((s) => (
					<li key={s.id} className="px-5 py-3 text-sm">
						<Link to="/subscriptions/$id" params={{ id: s.id }} className="group flex items-center gap-3">
							<ServiceTile name={s.name} size="sm" />
							<span className="min-w-0 flex-1">
								<span className="block truncate font-medium group-hover:underline">{s.name}</span>
								{s.until && (
									<span className="block text-xs text-muted">
										{t("service until {0}", date(s.until, "long"))}
									</span>
								)}
							</span>
							<span className="tnum font-medium">{t("{0} a year", money(s.yearlyMain))}</span>
						</Link>
					</li>
				))}
			</ul>
		</Card>
	);
}
