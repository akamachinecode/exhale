import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Bot, Clock, Undo2, User } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useListChanges, useListReminders, useUndo } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { locale, t } from "@/lib/i18n";
import { cn, date } from "@/lib/utils";

const labels: Record<string, string> = {
	"subscription.create": t("Created a subscription"),
	"subscription.update": t("Updated a subscription"),
	"subscription.pause": t("Paused a subscription"),
	"subscription.resume": t("Resumed a subscription"),
	"subscription.cancel_now": t("Cancelled a subscription"),
	"subscription.cancel_at_period_end": t("Cancelled at period end"),
	"subscription.delete": t("Deleted a subscription"),
	"subscription.reschedule": t("Changed the billing day"),
	"subscription.reprice": t("Changed the price"),
	"charge.move": t("Moved a charge"),
	"charge.amount": t("Changed a charge amount"),
	"charge.paid": t("Marked a charge paid"),
	"charge.failed": t("Marked a charge failed"),
	"charge.skipped": t("Skipped a charge"),
	"charge.pending": t("Reopened a charge"),
	"payment_method.save": t("Saved a payment method"),
	"category.create": t("Created a category"),
	undo: t("Undid a change"),
	// Two-factor sign-in (ADR 0025): recorded, not undoable.
	"two_factor.enable": t("Turned on two-factor sign-in"),
	"two_factor.disable": t("Turned off two-factor sign-in"),
	"two_factor.reset": t("Turned off two-factor sign-in for a member"),
	"two_factor.backup_codes": t("Made new backup codes"),
	// Remote MCP (ADR 0024): the actor is the application.
	"app.connect": t("Got access"),
	"app.disconnect": t("Lost access"),
};

export function ActivityPage() {
	const q = useListChanges({ limit: 100 });
	const qc = useQueryClient();
	const undo = useUndo({
		mutation: {
			onSuccess: () => {
				qc.invalidateQueries();
				toast.success(t("Change undone"));
			},
			onError: (e) => toast.error(e instanceof ApiError ? e.detail : t("Could not undo")),
		},
	});
	const entries = q.data?.entries ?? [];
	const [tab, setTab] = useState<"changes" | "reminders">("changes");
	return (
		<>
			<PageHeader
				title={t("Activity")}
				description={
					tab === "changes"
						? t(
								"Every change, by you, the people you share with, the scheduler or an AI agent. Anything can be undone.",
							)
						: t("Every reminder Exhale planned and what happened to it.")
				}
			/>
			<div className="mb-4 inline-flex gap-1 rounded-lg bg-raised p-1" role="tablist">
				{(["changes", "reminders"] as const).map((name) => (
					<button
						key={name}
						type="button"
						role="tab"
						aria-selected={tab === name}
						onClick={() => setTab(name)}
						className={cn(
							"rounded-md px-3 py-1.5 text-[13px] font-medium text-muted transition-colors",
							tab === name && "bg-surface text-fg shadow-sm",
						)}
					>
						{name === "changes" ? t("Changes") : t("Reminders")}
					</button>
				))}
			</div>
			{tab === "reminders" ? (
				<Reminders />
			) : (
				<Card>
					{entries.length === 0 ? (
						<p className="px-5 py-12 text-center text-sm text-muted">{t("No changes yet.")}</p>
					) : (
						<ul className="divide-y divide-line">
							{entries.map((e) => {
								const Icon = e.actorKind === "agent" ? Bot : e.actorKind === "system" ? Clock : User;
								const canUndo =
									!e.undoneBy &&
									e.action !== "undo" &&
									!e.action.startsWith("two_factor.") &&
									!e.action.startsWith("app.");
								return (
									<li key={e.id} className="flex items-center gap-3 px-5 py-3 text-sm">
										<span className="grid size-8 place-items-center rounded-full bg-raised text-muted">
											<Icon className="size-4" />
										</span>
										<div className="min-w-0 flex-1">
											<p className="truncate">
												<span className="font-medium">{e.actor || t("Someone")}</span>{" "}
												<span className="text-muted">{(labels[e.action] ?? e.action).toLowerCase()}</span>
											</p>
											<p className="text-xs text-faint">{new Date(e.at).toLocaleString(locale)}</p>
										</div>
										{e.actorKind === "agent" && <Badge tone="accent">{t("AI agent")}</Badge>}
										{e.undoneBy ? (
											<Badge>{t("Undone")}</Badge>
										) : (
											canUndo && (
												<Button
													size="sm"
													variant="ghost"
													onClick={() => undo.mutate({ data: { auditId: e.id } })}
												>
													<Undo2 /> {t("Undo")}
												</Button>
											)
										)}
									</li>
								);
							})}
						</ul>
					)}
				</Card>
			)}
		</>
	);
}

const kinds: Record<string, string> = {
	reminder: t("Reminder"),
	decision: t("Last day to cancel"),
	confirm: t("Was it charged?"),
};

/** The reminder log: the answer to "why didn't I get that reminder?". */
function Reminders() {
	const q = useListReminders({ limit: 200 });
	const entries = q.data?.entries ?? [];
	return (
		<Card>
			{entries.length === 0 ? (
				<p className="px-5 py-12 text-center text-sm text-muted">{t("No reminders yet.")}</p>
			) : (
				<ul className="divide-y divide-line">
					{entries.map((e) => (
						<li key={e.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-3 text-sm">
							<div className="min-w-0 flex-1">
								<p className="truncate">
									<Link
										to="/subscriptions/$id"
										params={{ id: e.subscriptionId }}
										className="font-medium hover:underline"
									>
										{e.subscription}
									</Link>{" "}
									<span className="text-muted">
										· {kinds[e.kind] ?? e.kind} {t("· charge of")} {date(e.chargeDate, "long")}
										{e.to && t(" · for {0}", e.to)}
									</span>
								</p>
								<p className="text-xs text-faint">
									{new Date(e.sentAt ?? e.dueAt).toLocaleString(locale)} · {e.explanation}
								</p>
							</div>
							<Badge
								tone={
									e.status === "sent"
										? "ok"
										: e.status === "failed"
											? "bad"
											: e.status === "pending" && e.attempts > 0
												? "warn"
												: "neutral"
								}
							>
								{e.status === "pending"
									? t("Scheduled")
									: e.status === "cancelled"
										? t("Not needed")
										: e.status === "sent"
											? t("Sent")
											: t("Not sent")}
							</Badge>
						</li>
					))}
				</ul>
			)}
		</Card>
	);
}
