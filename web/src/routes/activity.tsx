import { useQueryClient } from "@tanstack/react-query";
import { Bot, Clock, Undo2, User } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useListChanges, useUndo } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";

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
	return (
		<>
			<PageHeader
				title="Activity"
				description="Every change, by you, your household, the scheduler or an AI agent. Anything can be undone."
			/>
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
		</>
	);
}
