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
import { cn, date } from "@/lib/utils";

const labels: Record<string, string> = {
	"subscription.create": "Created a subscription",
	"subscription.update": "Updated a subscription",
	"subscription.pause": "Paused a subscription",
	"subscription.resume": "Resumed a subscription",
	"subscription.cancel_now": "Cancelled a subscription",
	"subscription.cancel_at_period_end": "Cancelled at period end",
	"subscription.delete": "Deleted a subscription",
	"subscription.reschedule": "Changed the billing day",
	"subscription.reprice": "Changed the price",
	"charge.move": "Moved a charge",
	"charge.amount": "Changed a charge amount",
	"charge.paid": "Marked a charge paid",
	"charge.failed": "Marked a charge failed",
	"charge.skipped": "Skipped a charge",
	"charge.pending": "Reopened a charge",
	"payment_method.save": "Saved a payment method",
	"category.create": "Created a category",
	undo: "Undid a change",
};

export function ActivityPage() {
	const q = useListChanges({ limit: 100 });
	const qc = useQueryClient();
	const undo = useUndo({
		mutation: {
			onSuccess: () => {
				qc.invalidateQueries();
				toast.success("Change undone");
			},
			onError: (e) => toast.error(e instanceof ApiError ? e.detail : "Could not undo"),
		},
	});
	const entries = q.data?.entries ?? [];
	const [tab, setTab] = useState<"changes" | "reminders">("changes");
	return (
		<>
			<PageHeader
				title="Activity"
				description={
					tab === "changes"
						? "Every change, by you, the people you share with, the scheduler or an AI agent. Anything can be undone."
						: "Every reminder Exhale planned and what happened to it."
				}
			/>
			<div className="mb-4 inline-flex gap-1 rounded-lg bg-raised p-1" role="tablist">
				{(["changes", "reminders"] as const).map((t) => (
					<button
						key={t}
						type="button"
						role="tab"
						aria-selected={tab === t}
						onClick={() => setTab(t)}
						className={cn(
							"rounded-md px-3 py-1.5 text-[13px] font-medium text-muted transition-colors",
							tab === t && "bg-surface text-fg shadow-sm",
						)}
					>
						{t === "changes" ? "Changes" : "Reminders"}
					</button>
				))}
			</div>
			{tab === "reminders" ? (
				<Reminders />
			) : (
				<Card>
					{entries.length === 0 ? (
						<p className="px-5 py-12 text-center text-sm text-muted">No changes yet.</p>
					) : (
						<ul className="divide-y divide-line">
							{entries.map((e) => {
								const Icon = e.actorKind === "agent" ? Bot : e.actorKind === "system" ? Clock : User;
								const canUndo = !e.undoneBy && e.action !== "undo";
								return (
									<li key={e.id} className="flex items-center gap-3 px-5 py-3 text-sm">
										<span className="grid size-8 place-items-center rounded-full bg-raised text-muted">
											<Icon className="size-4" />
										</span>
										<div className="min-w-0 flex-1">
											<p className="truncate">
												<span className="font-medium">{e.actor || "Someone"}</span>{" "}
												<span className="text-muted">{(labels[e.action] ?? e.action).toLowerCase()}</span>
											</p>
											<p className="text-xs text-faint">{new Date(e.at).toLocaleString()}</p>
										</div>
										{e.actorKind === "agent" && <Badge tone="accent">AI agent</Badge>}
										{e.undoneBy ? (
											<Badge>Undone</Badge>
										) : (
											canUndo && (
												<Button
													size="sm"
													variant="ghost"
													onClick={() => undo.mutate({ data: { auditId: e.id } })}
												>
													<Undo2 /> Undo
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
	reminder: "Reminder",
	decision: "Last day to cancel",
	confirm: "Was it charged?",
};

/** The reminder log: the answer to "why didn't I get that reminder?". */
function Reminders() {
	const q = useListReminders({ limit: 200 });
	const entries = q.data?.entries ?? [];
	return (
		<Card>
			{entries.length === 0 ? (
				<p className="px-5 py-12 text-center text-sm text-muted">No reminders yet.</p>
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
										· {kinds[e.kind] ?? e.kind} · charge of {date(e.chargeDate, "long")}
										{e.to && ` · for ${e.to}`}
									</span>
								</p>
								<p className="text-xs text-faint">
									{new Date(e.sentAt ?? e.dueAt).toLocaleString()} · {e.explanation}
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
									? "Scheduled"
									: e.status === "cancelled"
										? "Not needed"
										: e.status === "sent"
											? "Sent"
											: "Not sent"}
							</Badge>
						</li>
					))}
				</ul>
			)}
		</Card>
	);
}
