import { useQueryClient } from "@tanstack/react-query";
import { FileText, ImageIcon, Paperclip, Sparkles, Trash2, Upload } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Select } from "@/components/ui/field";
import {
	type AssistantTerms,
	type ChargeView,
	type Document,
	type SubscriptionView,
	type UpdateSubscriptionInput,
	useAssistantReadTerms,
	useAuthSession,
	useDeleteDocument,
	useListDocuments,
	useRemoveLogo,
	useUndo,
	useUpdateSubscription,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import { date } from "@/lib/utils";

// Logos and documents of a subscription (ADR 0027). Files go up as
// multipart uploads; everything else uses the generated client.

const MAX_DOCUMENT = 10 << 20;
const MAX_LOGO = 2 << 20;
const documentTypes = "application/pdf,image/png,image/jpeg,image/webp";
const logoTypes = "image/png,image/jpeg,image/webp";

async function upload<T>(url: string, file: File, fields: Record<string, string> = {}): Promise<T> {
	const form = new FormData();
	form.append("file", file);
	for (const [k, v] of Object.entries(fields)) form.append(k, v);
	const res = await fetch(url, { method: "POST", body: form });
	const text = await res.text();
	const body = text ? JSON.parse(text) : undefined;
	if (!res.ok) throw new ApiError(res.status, body?.detail ?? res.statusText);
	return body as T;
}

const errorText = (e: unknown) => (e instanceof ApiError ? e.detail : t("Something went wrong"));

function size(bytes: number) {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1 << 20) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1 << 20)).toFixed(1)} MB`;
}

function kindLabel(kind: string) {
	return kind === "invoice"
		? t("Invoice or receipt")
		: kind === "other"
			? t("Other")
			: t("Contract or terms");
}

/** The logo row of the details card: upload your own, or go back to the bundled mark. */
export function LogoControl({ subscription: s }: { subscription: SubscriptionView }) {
	const qc = useQueryClient();
	const id = useId();
	const [busy, setBusy] = useState(false);
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const remove = useRemoveLogo({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				if (r.auditId)
					toast.success(t("Logo removed"), {
						action: { label: t("Undo"), onClick: () => undo.mutate({ data: { auditId: r.auditId ?? "" } }) },
					});
			},
			onError: (e) => toast.error(errorText(e)),
		},
	});
	const pick = async (file?: File) => {
		if (!file) return;
		if (file.size > MAX_LOGO) {
			toast.error(t("A logo can be up to 2 MB"));
			return;
		}
		setBusy(true);
		try {
			await upload(`/api/v1/subscriptions/${s.id}/logo`, file);
			await qc.invalidateQueries();
			toast.success(t("Logo updated"));
		} catch (e) {
			toast.error(errorText(e));
		} finally {
			setBusy(false);
		}
	};
	return (
		<div className="grid gap-1">
			<dt className="text-xs text-muted">{t("Logo")}</dt>
			<dd className="flex flex-wrap items-center gap-2">
				<span className="flex-1">{s.logo ? t("Your own") : t("Brand mark or initials")}</span>
				<label
					htmlFor={id}
					className="inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-2 text-[13px] text-muted hover:bg-raised hover:text-fg has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-accent"
				>
					<Upload className="size-3.5" /> {busy ? t("Uploading…") : t("Upload")}
					<input
						id={id}
						type="file"
						accept={logoTypes}
						className="sr-only"
						disabled={busy}
						onChange={(e) => {
							void pick(e.target.files?.[0]);
							e.target.value = "";
						}}
					/>
				</label>
				{s.logo && (
					<Button
						size="sm"
						variant="ghost"
						className="h-7 px-2 text-[13px]"
						onClick={() => remove.mutate({ data: { id: s.id } })}
					>
						{t("Remove")}
					</Button>
				)}
			</dd>
		</div>
	);
}

/** Contracts, terms, invoices and receipts attached to a subscription. */
export function Documents({
	subscription: s,
	charges,
}: {
	subscription: SubscriptionView;
	charges: ChargeView[];
}) {
	const qc = useQueryClient();
	const q = useListDocuments(s.id);
	const session = useAuthSession();
	const assistant = Boolean(session.data?.assistant);
	const [attaching, setAttaching] = useState(false);
	const [reading, setReading] = useState<Document>();
	const undo = useUndo({ mutation: { onSuccess: () => qc.invalidateQueries() } });
	const del = useDeleteDocument({
		mutation: {
			onSuccess: (r) => {
				qc.invalidateQueries();
				toast.success(t("Document deleted"), {
					action: { label: t("Undo"), onClick: () => undo.mutate({ data: { auditId: r.auditId ?? "" } }) },
				});
			},
			onError: (e) => toast.error(errorText(e)),
		},
	});
	const docs = q.data?.documents ?? [];
	const chargeDates = new Map(charges.map((c) => [c.id, c.dueDate]));
	return (
		<Card>
			<CardHeader
				title={t("Documents")}
				description={t("Contracts, terms, invoices and receipts: PDF or pictures, up to 10 MB each.")}
				action={
					<Button size="sm" onClick={() => setAttaching(true)}>
						<Paperclip /> {t("Attach")}
					</Button>
				}
			/>
			{docs.length === 0 ? (
				<p className="border-t border-line px-5 py-8 text-center text-sm text-muted">
					{t("No documents yet.")}
				</p>
			) : (
				<ul className="divide-y divide-line border-t border-line">
					{docs.map((d) => {
						const Icon = d.mediaType === "application/pdf" ? FileText : ImageIcon;
						const charge = d.chargeId ? chargeDates.get(d.chargeId) : undefined;
						return (
							<li key={d.id} className="flex flex-wrap items-center gap-x-3 gap-y-2 px-5 py-3 text-sm">
								<span className="grid size-8 shrink-0 place-items-center rounded-md bg-raised text-muted">
									<Icon className="size-4" />
								</span>
								<div className="min-w-0 flex-1">
									<a href={d.url} download={d.name} className="block truncate font-medium hover:underline">
										{d.name}
									</a>
									<p className="text-xs text-muted">
										{kindLabel(d.kind)}
										{charge && t(" · charge of {0}", date(charge))} · {size(d.size)} ·{" "}
										{date(d.createdAt.slice(0, 10))}
									</p>
								</div>
								{assistant && (
									<Button
										size="sm"
										variant="ghost"
										onClick={() => setReading(d)}
										title={t("Read dates from this document")}
									>
										<Sparkles /> {t("Read dates")}
									</Button>
								)}
								<Button
									size="icon"
									variant="ghost"
									aria-label={t("Delete {0}", d.name)}
									onClick={() => del.mutate({ data: { id: d.id } })}
								>
									<Trash2 />
								</Button>
							</li>
						);
					})}
				</ul>
			)}
			<AttachDialog open={attaching} onOpenChange={setAttaching} subscription={s} charges={charges} />
			{reading && (
				<ReadTermsDialog
					document={reading}
					subscription={s}
					onOpenChange={(open) => !open && setReading(undefined)}
				/>
			)}
		</Card>
	);
}

function AttachDialog({
	open,
	onOpenChange,
	subscription: s,
	charges,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	subscription: SubscriptionView;
	charges: ChargeView[];
}) {
	const qc = useQueryClient();
	const [file, setFile] = useState<File>();
	const [kind, setKind] = useState("contract");
	const [chargeId, setChargeId] = useState("");
	const [busy, setBusy] = useState(false);
	const tooLarge = file !== undefined && file.size > MAX_DOCUMENT;
	const submit = async () => {
		if (!file || tooLarge) return;
		setBusy(true);
		try {
			await upload(`/api/v1/subscriptions/${s.id}/documents`, file, {
				kind,
				...(kind === "invoice" && chargeId ? { chargeId } : {}),
			});
			await qc.invalidateQueries();
			toast.success(t("Document attached"));
			setFile(undefined);
			setChargeId("");
			onOpenChange(false);
		} catch (e) {
			toast.error(errorText(e));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Dialog
			open={open}
			onOpenChange={onOpenChange}
			title={t("Attach a document")}
			description={t("It is kept in Exhale's database, so it is part of every backup.")}
		>
			<form
				className="grid gap-4"
				onSubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
			>
				<Field
					label={t("File")}
					hint={t("PDF, PNG, JPEG or WebP, up to 10 MB.")}
					error={tooLarge ? t("The file is larger than 10 MB") : undefined}
				>
					<input
						type="file"
						accept={documentTypes}
						onChange={(e) => setFile(e.target.files?.[0])}
						className="text-sm file:mr-3 file:h-8 file:rounded-lg file:border file:border-line file:bg-surface file:px-3 file:text-sm file:text-fg hover:file:bg-raised"
					/>
				</Field>
				<Field label={t("What it is")}>
					<Select value={kind} onChange={(e) => setKind(e.target.value)}>
						<option value="contract">{t("Contract or terms")}</option>
						<option value="invoice">{t("Invoice or receipt")}</option>
						<option value="other">{t("Other")}</option>
					</Select>
				</Field>
				{kind === "invoice" && charges.length > 0 && (
					<Field label={t("Charge")} hint={t("The charge this invoice or receipt is for, if any.")}>
						<Select value={chargeId} onChange={(e) => setChargeId(e.target.value)}>
							<option value="">{t("None")}</option>
							{charges.map((c) => (
								<option key={c.id} value={c.id}>
									{date(c.dueDate, "long")}
								</option>
							))}
						</Select>
					</Field>
				)}
				<div className="flex justify-end gap-2">
					<Button onClick={() => onOpenChange(false)}>{t("Cancel")}</Button>
					<Button type="submit" variant="primary" disabled={!file || tooLarge || busy}>
						{busy ? t("Uploading…") : t("Attach")}
					</Button>
				</div>
			</form>
		</Dialog>
	);
}

function fieldLabel(field: string) {
	switch (field) {
		case "noticeDays":
			return t("Notice period");
		case "cancelUrl":
			return t("Cancel or manage");
		case "startedOn":
			return t("Subscribed since");
		case "trialEndsOn":
			return t("Trial ends");
		default:
			return t("Account");
	}
}

function fieldValue(field: string, value?: string) {
	if (!value) return "—";
	if (field === "noticeDays") return t("{0} days", value);
	if (field === "startedOn" || field === "trialEndsOn") return date(value, "long");
	return value;
}

function updateInput(
	id: string,
	terms: AssistantTerms,
	picked: Set<string>,
	dryRun: boolean,
): UpdateSubscriptionInput {
	const in_: UpdateSubscriptionInput = { id, dryRun };
	for (const f of terms.fields ?? []) {
		if (!picked.has(f.field)) continue;
		if (f.field === "noticeDays") in_.noticeDays = Number(f.value);
		else if (f.field === "cancelUrl") in_.cancelUrl = f.value;
		else if (f.field === "startedOn") in_.startedOn = f.value;
		else if (f.field === "trialEndsOn") in_.trialEndsOn = f.value;
		else if (f.field === "account") in_.account = f.value;
	}
	return in_;
}

/**
 * The assistant reads a document and proposes contract details. Nothing is
 * saved until the person ticks what to keep and applies it, through the
 * same update_subscription anyone uses, previewed first with a dry run.
 */
function ReadTermsDialog({
	document: doc,
	subscription: s,
	onOpenChange,
}: {
	document: Document;
	subscription: SubscriptionView;
	onOpenChange: (open: boolean) => void;
}) {
	const qc = useQueryClient();
	const read = useAssistantReadTerms();
	const preview = useUpdateSubscription();
	const apply = useUpdateSubscription({
		mutation: {
			onSuccess: () => {
				qc.invalidateQueries();
				toast.success(t("Contract details saved"));
				onOpenChange(false);
			},
			onError: (e) => toast.error(errorText(e)),
		},
	});
	const [picked, setPicked] = useState<Set<string>>(new Set());
	const { mutate: readMutate } = read;
	useEffect(() => {
		readMutate(
			{ id: doc.id },
			{ onSuccess: (r) => setPicked(new Set((r.fields ?? []).map((f) => f.field))) },
		);
	}, [doc.id, readMutate]);
	const terms = read.data;
	const { mutate: previewMutate } = preview;
	const key = [...picked].sort().join(",");
	useEffect(() => {
		if (!terms || key === "") return;
		previewMutate({ data: updateInput(s.id, terms, new Set(key.split(",")), true) });
	}, [terms, key, s.id, previewMutate]);
	const fields = terms?.fields ?? [];
	const after = preview.data?.subscription;
	const toggle = (field: string) => {
		const next = new Set(picked);
		if (next.has(field)) next.delete(field);
		else next.add(field);
		setPicked(next);
	};
	return (
		<Dialog
			open
			onOpenChange={onOpenChange}
			title={t("Read dates from {0}", doc.name)}
			description={t(
				"The assistant reads the document and suggests contract details. Check each one: nothing is saved until you apply it.",
			)}
		>
			{read.isPending && <p className="py-6 text-center text-sm text-muted">{t("Reading the document…")}</p>}
			{read.isError && <p className="py-4 text-sm text-bad">{errorText(read.error)}</p>}
			{terms && (
				<div className="grid gap-4">
					{fields.length === 0 ? (
						<p className="text-sm text-muted">{t("Nothing new found in this document.")}</p>
					) : (
						<ul className="grid gap-2">
							{fields.map((f) => (
								<li key={f.field}>
									<label className="flex cursor-pointer items-start gap-3 rounded-lg border border-line px-3 py-2.5 text-sm hover:bg-raised">
										<input
											type="checkbox"
											className="mt-1 accent-[var(--color-accent)]"
											checked={picked.has(f.field)}
											onChange={() => toggle(f.field)}
										/>
										<span className="min-w-0 flex-1">
											<span className="block text-xs text-muted">{fieldLabel(f.field)}</span>
											<span className="block font-medium break-all">{fieldValue(f.field, f.value)}</span>
											{f.current && (
												<span className="block text-xs text-muted">
													{t("now: {0}", fieldValue(f.field, f.current))}
												</span>
											)}
											{f.quote && <span className="mt-1 block text-xs text-muted italic">“{f.quote}”</span>}
										</span>
									</label>
								</li>
							))}
						</ul>
					)}
					{picked.size > 0 && after?.cancelBy && (
						<p className="text-sm">
							{t(
								"With these, the last day to cancel before the next charge is {0}.",
								date(after.cancelBy, "long"),
							)}
						</p>
					)}
					{picked.size > 0 && preview.isError && (
						<p className="text-sm text-bad">{errorText(preview.error)}</p>
					)}
					{(terms.notes ?? []).length > 0 && (
						<ul className="grid gap-1 text-xs text-muted">
							{(terms.notes ?? []).map((n) => (
								<li key={n}>{n}</li>
							))}
						</ul>
					)}
					<p className="text-xs text-faint">{t("Read by {0}", terms.model)}</p>
					<div className="flex justify-end gap-2">
						<Button onClick={() => onOpenChange(false)}>{t("Cancel")}</Button>
						<Button
							variant="primary"
							disabled={picked.size === 0 || preview.isError || preview.isPending || apply.isPending}
							onClick={() => apply.mutate({ data: updateInput(s.id, terms, picked, false) })}
						>
							{t("Apply")}
						</Button>
					</div>
				</div>
			)}
		</Dialog>
	);
}
