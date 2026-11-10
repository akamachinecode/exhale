import { useQueryClient } from "@tanstack/react-query";
import { Check, Pencil, X } from "lucide-react";
import { type KeyboardEvent, useId, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/field";
import { useDeleteTag, useGetHousehold, useRenameTag, useUndo } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

/** Small labels shown next to a subscription. */
export function TagChips({ tags, className }: { tags?: string[] | null; className?: string }) {
	if (!tags?.length) return null;
	return (
		<span className={cn("flex flex-wrap gap-1", className)}>
			{tags.map((tag) => (
				<span
					key={tag}
					className="rounded-full border border-line px-1.5 py-px text-[11px] leading-4 font-medium text-muted"
				>
					{tag}
				</span>
			))}
		</span>
	);
}

/** Free tags: type and press Enter or a comma; existing tags are suggested. */
export function TagInput({
	value,
	onChange,
	suggestions = [],
}: {
	value: string[];
	onChange: (tags: string[]) => void;
	suggestions?: string[];
}) {
	const [text, setText] = useState("");
	const listId = useId();
	const add = (raw: string) => {
		const tag = raw.replace(/[,;]/g, " ").replace(/\s+/g, " ").trim();
		setText("");
		if (!tag || value.some((v) => same(v, tag))) return;
		onChange([...value, tag]);
	};
	const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
		if (e.key === "Enter" || e.key === ",") {
			e.preventDefault();
			add(text);
		} else if (e.key === "Backspace" && !text && value.length) {
			onChange(value.slice(0, -1));
		}
	};
	return (
		<div className="flex min-h-9 flex-wrap items-center gap-1.5 rounded-lg border border-line bg-surface px-2 py-1 focus-within:border-accent focus-within:outline-2 focus-within:outline-accent/25">
			{value.map((tag) => (
				<span
					key={tag}
					className="flex items-center gap-0.5 rounded-full bg-raised py-0.5 pr-0.5 pl-2 text-xs font-medium"
				>
					{tag}
					<button
						type="button"
						aria-label={t("Remove tag {0}", tag)}
						onClick={() => onChange(value.filter((v) => v !== tag))}
						className="rounded-full p-0.5 text-faint hover:text-fg"
					>
						<X className="size-3" />
					</button>
				</span>
			))}
			<input
				className="h-7 min-w-24 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-faint"
				placeholder={value.length ? "" : t("work, family…")}
				aria-label={t("Add a tag")}
				list={listId}
				maxLength={40}
				value={text}
				onChange={(e) => {
					// Picking a suggestion fills the whole value at once.
					const v = e.target.value;
					if (suggestions.some((s) => s === v)) add(v);
					else setText(v);
				}}
				onKeyDown={onKey}
				onBlur={() => text && add(text)}
			/>
			<datalist id={listId}>
				{suggestions
					.filter((s) => !value.some((v) => same(v, s)))
					.map((s) => (
						<option key={s} value={s} />
					))}
			</datalist>
		</div>
	);
}

/** Every tag in use: rename or delete one on every subscription. */
export function Tags() {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const rename = useRenameTag();
	const del = useDeleteTag();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const [editing, setEditing] = useState<{ tag: string; to: string }>();
	const tags = household.data?.tags ?? [];
	const fail = (err: unknown) => toast.error(err instanceof ApiError ? err.detail : t("Could not save"));
	const withUndo = (message: string, auditIds: string[]) =>
		toast.success(message, {
			action: { label: t("Undo"), onClick: () => undo.mutate({ data: { auditIds } }) },
		});

	if (tags.length === 0)
		return (
			<p className="text-sm text-muted">
				{t("No tags yet. Add them to a subscription, or to several at once from the list.")}
			</p>
		);
	return (
		<div className="grid gap-3">
			<ul className="flex flex-wrap gap-2">
				{tags.map((tag) =>
					editing && same(editing.tag, tag.name) ? (
						<li key={tag.name}>
							<form
								className="flex items-center gap-1"
								onSubmit={async (e) => {
									e.preventDefault();
									try {
										const r = await rename.mutateAsync({ data: { tag: tag.name, to: editing.to } });
										await qc.invalidateQueries();
										setEditing(undefined);
										withUndo(t("Tag renamed"), r.auditIds ?? []);
									} catch (err) {
										fail(err);
									}
								}}
							>
								<Input
									autoFocus
									className="h-8 w-40"
									maxLength={40}
									aria-label={t("Tag name")}
									value={editing.to}
									onChange={(e) => setEditing({ tag: tag.name, to: e.target.value })}
								/>
								<Button type="submit" size="icon" variant="ghost" aria-label={t("Save")}>
									<Check />
								</Button>
								<Button
									size="icon"
									variant="ghost"
									aria-label={t("Cancel")}
									onClick={() => setEditing(undefined)}
								>
									<X />
								</Button>
							</form>
						</li>
					) : (
						<li
							key={tag.name}
							className="group flex items-center gap-1 rounded-full border border-line py-0.5 pr-1 pl-3 text-[13px]"
						>
							{tag.name}
							<span className="tnum text-xs text-faint">{tag.subscriptions}</span>
							<button
								type="button"
								aria-label={t("Rename {0}", tag.name)}
								onClick={() => setEditing({ tag: tag.name, to: tag.name })}
								className="rounded-full p-1 text-faint hover:bg-raised hover:text-fg"
							>
								<Pencil className="size-3" />
							</button>
							<button
								type="button"
								aria-label={t("Delete {0}", tag.name)}
								onClick={async () => {
									try {
										const r = await del.mutateAsync({ data: { tag: tag.name } });
										await qc.invalidateQueries();
										withUndo(
											tn(r.changed, "Removed from {n} subscription", "Removed from {n} subscriptions"),
											r.auditIds ?? [],
										);
									} catch (err) {
										fail(err);
									}
								}}
								className="rounded-full p-1 text-faint hover:bg-raised hover:text-bad"
							>
								<X className="size-3" />
							</button>
						</li>
					),
				)}
			</ul>
			<p className="text-xs text-muted">
				{t("Renaming or deleting a tag changes every subscription that has it. Totals stay by category.")}
			</p>
		</div>
	);
}
