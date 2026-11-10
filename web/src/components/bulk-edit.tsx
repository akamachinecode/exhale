import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { TagInput } from "@/components/tags";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Select } from "@/components/ui/field";
import {
	type BulkResult,
	type BulkUpdateInput,
	type FieldChange,
	type SubscriptionView,
	useBulkUpdateSubscriptions,
	useGetHousehold,
	useUndo,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";

export type BulkMode = "category" | "paymentMethod" | "tags" | "pause" | "resume" | "cancel" | "delete";

const statusText: Record<string, string> = {
	active: t("Active"),
	paused: t("Paused"),
	cancelled: t("Cancelled"),
};

function describe(c: FieldChange) {
	const none = t("none");
	switch (c.field) {
		case "category":
			return t("Category: {0} → {1}", c.from || none, c.to || none);
		case "paymentMethod":
			return t("Payment method: {0} → {1}", c.from || none, c.to || none);
		case "tags":
			return t("Tags: {0} → {1}", c.from || none, c.to || none);
		case "status":
			return `${statusText[c.from] ?? c.from} → ${statusText[c.to] ?? c.to}`;
		default:
			return t("Deleted");
	}
}

/**
 * Changes the selected subscriptions in one step. It always shows a preview
 * (a dry run) of what changes for each before anything is saved, and the
 * whole change can be undone at once.
 */
export function BulkEdit({
	mode,
	selected,
	onOpenChange,
	onDone,
}: {
	mode?: BulkMode;
	selected: SubscriptionView[];
	onOpenChange: (open: boolean) => void;
	onDone: () => void;
}) {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const bulk = useBulkUpdateSubscriptions();
	const preview = useBulkUpdateSubscriptions();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const [categoryId, setCategoryId] = useState("");
	const [methodId, setMethodId] = useState("");
	const [add, setAdd] = useState<string[]>([]);
	const [remove, setRemove] = useState<string[]>([]);
	const [cancelNow, setCancelNow] = useState(false);
	const [error, setError] = useState<string>();
	const open = Boolean(mode);
	const ids = selected.map((s) => s.id);

	// biome-ignore lint/correctness/useExhaustiveDependencies: reset when it opens
	useEffect(() => {
		if (open) {
			setCategoryId("");
			setMethodId("");
			setAdd([]);
			setRemove([]);
			setCancelNow(false);
			setError(undefined);
			preview.reset();
		}
	}, [open, mode]);

	const input = (): BulkUpdateInput | undefined => {
		switch (mode) {
			// "" is not chosen yet; "-" is none.
			case "category":
				return categoryId ? { ids, categoryId: categoryId === "-" ? "" : categoryId } : undefined;
			case "paymentMethod":
				return methodId ? { ids, paymentMethodId: methodId === "-" ? "" : methodId } : undefined;
			case "tags":
				return add.length || remove.length ? { ids, addTags: add, removeTags: remove } : undefined;
			case "cancel":
				return { ids, action: cancelNow ? "cancel_now" : "cancel_at_period_end" };
			case undefined:
				return undefined;
			default:
				return { ids, action: mode };
		}
	};
	const data = input();
	const key = JSON.stringify(data);
	const { mutate } = preview;
	// biome-ignore lint/correctness/useExhaustiveDependencies: key is the input
	useEffect(() => {
		if (!open || !data) return;
		const h = setTimeout(() => mutate({ data: { ...data, dryRun: true } }), 200);
		return () => clearTimeout(h);
	}, [open, key, mutate]);

	const result: BulkResult | undefined = data ? preview.data : undefined;
	const changed = result?.changed ?? 0;
	const destructive = mode === "delete" || mode === "cancel";
	const titles: Record<BulkMode, string> = {
		category: t("Change the category"),
		paymentMethod: t("Change the payment method"),
		tags: t("Add or remove tags"),
		pause: t("Pause"),
		resume: t("Resume"),
		cancel: t("Cancel"),
		delete: t("Delete"),
	};
	const apply = async () => {
		if (!data) return;
		setError(undefined);
		try {
			const r = await bulk.mutateAsync({ data });
			await qc.invalidateQueries();
			onOpenChange(false);
			onDone();
			toast.success(tn(r.changed, "{n} subscription changed", "{n} subscriptions changed"), {
				action: r.auditIds?.length
					? { label: t("Undo"), onClick: () => undo.mutate({ data: { auditIds: r.auditIds ?? [] } }) }
					: undefined,
			});
		} catch (err) {
			setError(err instanceof ApiError ? err.detail : t("Could not save"));
		}
	};
	const present = [...new Set(selected.flatMap((s) => s.tags ?? []))];

	return (
		<Dialog
			open={open}
			onOpenChange={onOpenChange}
			title={mode ? titles[mode] : ""}
			description={tn(selected.length, "{n} subscription selected", "{n} subscriptions selected")}
		>
			<div className="grid gap-4">
				{mode === "category" && (
					<Field label={t("New category")}>
						<Select value={categoryId} onChange={(e) => setCategoryId(e.target.value)}>
							<option value="" disabled>
								{t("Choose…")}
							</option>
							<option value="-">{t("None")}</option>
							{(household.data?.categories ?? []).map((c) => (
								<option key={c.id} value={c.id}>
									{c.name}
								</option>
							))}
						</Select>
					</Field>
				)}
				{mode === "paymentMethod" && (
					<Field label={t("New payment method")}>
						<Select value={methodId} onChange={(e) => setMethodId(e.target.value)}>
							<option value="" disabled>
								{t("Choose…")}
							</option>
							<option value="-">{t("None")}</option>
							{(household.data?.paymentMethods ?? []).map((p) => (
								<option key={p.id} value={p.id}>
									{p.name}
									{p.last4 ? ` ••${p.last4}` : ""}
								</option>
							))}
						</Select>
					</Field>
				)}
				{mode === "tags" && (
					<>
						<Field label={t("Add tags")}>
							<TagInput
								value={add}
								onChange={setAdd}
								suggestions={(household.data?.tags ?? []).map((x) => x.name)}
							/>
						</Field>
						{present.length > 0 && (
							<Field label={t("Remove tags")}>
								<TagInput value={remove} onChange={setRemove} suggestions={present} />
							</Field>
						)}
					</>
				)}
				{mode === "cancel" && (
					<Field label={t("When")}>
						<Select
							value={cancelNow ? "now" : "end"}
							onChange={(e) => setCancelNow(e.target.value === "now")}
						>
							<option value="end">{t("At the end of the period they are paid for")}</option>
							<option value="now">{t("Now: no more charges from today")}</option>
						</Select>
					</Field>
				)}
				{mode === "delete" && (
					<p className="text-sm text-muted">
						{t("Deleting removes them and their charge history. You can undo it right after.")}
					</p>
				)}
				{(mode === "cancel" || mode === "delete") && (
					<p className="text-xs text-muted">
						{t("Exhale only stops tracking them: cancel them with the provider too.")}
					</p>
				)}
				<div className="rounded-lg border border-line" aria-live="polite">
					<p className="border-b border-line px-3 py-2 text-xs font-medium text-muted">
						{!data
							? t("Choose what to change.")
							: result
								? tn(changed, "{n} will change", "{n} will change")
								: t("Checking…")}
					</p>
					<ul className="max-h-60 divide-y divide-line overflow-y-auto text-sm" aria-label={t("Preview")}>
						{(result?.items ?? []).map((it) => (
							<li key={it.id} className="flex flex-wrap items-baseline justify-between gap-x-3 px-3 py-2">
								<span className="font-medium">{it.name}</span>
								<span className="text-xs text-muted">
									{it.changes?.length ? it.changes.map(describe).join(" · ") : t("no change")}
								</span>
							</li>
						))}
					</ul>
				</div>
				{(error || preview.error) && (
					<p className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">
						{error ?? (preview.error instanceof ApiError ? preview.error.detail : t("Something went wrong"))}
					</p>
				)}
				<div className="flex justify-end gap-2">
					<Button onClick={() => onOpenChange(false)}>{t("Close")}</Button>
					<Button
						variant={destructive ? "danger" : "primary"}
						disabled={!data || !result || changed === 0 || bulk.isPending}
						onClick={apply}
					>
						{bulk.isPending
							? t("Saving…")
							: mode === "delete"
								? tn(changed, "Delete {n} subscription", "Delete {n} subscriptions")
								: tn(changed, "Change {n} subscription", "Change {n} subscriptions")}
					</Button>
				</div>
			</div>
		</Dialog>
	);
}
