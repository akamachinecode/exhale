import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Download, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { apiOrigin } from "@/components/connect";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	type ImportItem,
	type ImportResult,
	useAuthSession,
	useCalendarFeed,
	useCalendarFeedCreate,
	useCalendarFeedRemove,
	useImportSubscriptions,
} from "@/gen/api";
import { ApiError, problemDetail } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";
import { date, every } from "@/lib/utils";

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

type Parsed = { source: string; items: ImportItem[]; notes: string[]; model?: string };

const sources: Record<string, string> = {
	"wallos-db": t("Wallos database"),
	"wallos-export": t("Wallos export"),
	"exhale-export": t("Exhale export"),
	csv: t("Spreadsheet (CSV)"),
	transactions: t("Bank export"),
	statement: t("Statement (PDF)"),
	picture: t("Picture"),
	text: t("Text"),
};

/** Import: read a file, review the list, then bring in what is ticked. */
export function ImportData() {
	const qc = useQueryClient();
	const input = useRef<HTMLInputElement>(null);
	const run = useImportSubscriptions();
	const [parsed, setParsed] = useState<Parsed>();
	const [preview, setPreview] = useState<ImportResult>();
	const [picked, setPicked] = useState<boolean[]>([]);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const [pasted, setPasted] = useState("");
	// With the assistant on, a model reads what Exhale cannot read by itself.
	const assistant = Boolean(useAuthSession().data?.assistant);

	const onFile = async (file: File | string) => {
		setError(undefined);
		setBusy(true);
		try {
			const res = await fetch("/api/v1/import/parse", {
				method: "POST",
				headers: { "Content-Type": typeof file === "string" ? "text/plain" : "application/octet-stream" },
				body: file,
			});
			const body = await res.json();
			if (!res.ok) throw new ApiError(res.status, problemDetail(body, t("Could not read the file")));
			const p = body as Parsed;
			// A dry run says what would happen to each item before anything is saved.
			const dry = await run.mutateAsync({ data: { items: p.items, dryRun: true } });
			setParsed(p);
			setPreview(dry);
			setPicked((dry.items ?? []).map((o) => o.action === "create"));
			setPasted("");
		} catch (err) {
			setError(errText(err, t("Could not read the file")));
		} finally {
			setBusy(false);
			if (input.current) input.current.value = "";
		}
	};

	const apply = async () => {
		if (!parsed) return;
		setError(undefined);
		try {
			const items = parsed.items.filter((_, i) => picked[i]);
			const res = await run.mutateAsync({ data: { items } });
			await qc.invalidateQueries();
			toast.success(tn(res.created, "{n} subscription imported", "{n} subscriptions imported"));
			setParsed(undefined);
			setPreview(undefined);
		} catch (err) {
			setError(errText(err, t("Could not import")));
		}
	};

	const count = picked.filter(Boolean).length;
	return (
		<div className="grid gap-3">
			<input
				ref={input}
				type="file"
				accept=".db,.sqlite,.json,.csv,.tsv,.txt,.pdf,.png,.jpg,.jpeg,.webp"
				className="sr-only"
				aria-label={t("File to import")}
				onChange={(e) => e.target.files?.[0] && onFile(e.target.files[0])}
			/>
			{!parsed && (
				<>
					<p className="text-sm text-muted">
						{t("A Wallos database (")}
						<code className="font-mono text-xs">db/wallos.db</code>
						{t(
							") or export, a spreadsheet saved as CSV, an Exhale export, or your bank's export of transactions (CSV): Exhale finds the charges that repeat.",
						)}{" "}
						{assistant
							? t("A statement in PDF, a screenshot or pasted text is read by the assistant's model.")
							: t("With the built-in assistant on, it also reads a statement in PDF or a screenshot.")}{" "}
						{t("You review the list before anything is saved.")}
					</p>
					<div>
						<Button disabled={busy} onClick={() => input.current?.click()}>
							<Upload /> {busy ? t("Reading…") : t("Choose a file")}
						</Button>
					</div>
					{assistant && (
						<div className="grid gap-2">
							<textarea
								value={pasted}
								onChange={(e) => setPasted(e.target.value)}
								rows={3}
								placeholder={t("…or paste text: an email from a service, lines from a statement")}
								aria-label={t("Text to import")}
								className="w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm placeholder:text-faint focus:border-accent focus:outline-2 focus:outline-accent/25"
							/>
							{pasted.trim() && (
								<div>
									<Button disabled={busy} onClick={() => onFile(pasted)}>
										{busy ? t("Reading…") : t("Read the text")}
									</Button>
								</div>
							)}
						</div>
					)}
				</>
			)}
			{parsed && preview && (
				<>
					<p className="text-sm">
						<span className="font-medium">{sources[parsed.source] ?? parsed.source}</span>
						{parsed.model && (
							<Badge className="ml-2">
								{t("Read by")} {parsed.model}
							</Badge>
						)}
						<span className="text-muted">
							{" "}
							· {parsed.items.length} {t("found ·")} {preview.duplicates} {t("already in Exhale")}
							{preview.errors > 0 && t(" · {0} with problems", preview.errors)}
						</span>
					</p>
					{parsed.notes.map((n) => (
						<p key={n} className="text-xs text-muted">
							{n}
						</p>
					))}
					<ul className="max-h-96 divide-y divide-line overflow-y-auto rounded-lg border border-line">
						{parsed.items.map((it, i) => {
							const o = preview.items?.[i];
							const ok = o?.action === "create";
							return (
								// biome-ignore lint/suspicious/noArrayIndexKey: the list is fixed while reviewing
								<li key={i} className="flex items-start gap-3 px-3 py-2.5 text-sm">
									<input
										type="checkbox"
										className="mt-0.5 size-4 accent-[var(--accent)]"
										disabled={!ok}
										checked={Boolean(picked[i])}
										onChange={(e) => setPicked(picked.map((p, j) => (j === i ? e.target.checked : p)))}
										aria-label={t("Import {0}", it.name)}
									/>
									<span className="min-w-0 flex-1">
										<span className="flex flex-wrap items-center gap-2">
											<span className="font-medium">{it.name}</span>
											{it.status === "cancelled" && <Badge>{t("Cancelled")}</Badge>}
											{o?.action === "duplicate" && <Badge tone="warn">{t("Already here")}</Badge>}
											{o?.action === "error" && <Badge tone="bad">{t("Problem")}</Badge>}
										</span>
										<span className="block text-xs text-muted">
											{[
												`${it.price} ${it.currency ?? ""}`.trim(),
												every(it.interval ?? 1, it.unit ?? "month"),
												it.nextCharge &&
													(it.status === "cancelled"
														? `from ${date(it.nextCharge, "long")}`
														: `next ${date(it.nextCharge, "long")}`),
												it.paymentMethod,
												it.category,
											]
												.filter(Boolean)
												.join(" · ")}
										</span>
										{(o?.message || it.remark) && (
											<span className="block text-xs text-faint">
												{o?.action === "create" ? it.remark : o?.message}
											</span>
										)}
									</span>
								</li>
							);
						})}
					</ul>
					{((preview.newCategories ?? []).length > 0 || (preview.newPaymentMethods ?? []).length > 0) && (
						<p className="text-xs text-muted">
							{t("Will also create")}{" "}
							{[
								(preview.newPaymentMethods ?? []).length &&
									`payment methods: ${(preview.newPaymentMethods ?? []).join(", ")}`,
								(preview.newCategories ?? []).length &&
									`categories: ${(preview.newCategories ?? []).join(", ")}`,
							]
								.filter(Boolean)
								.join("; ")}
							.
						</p>
					)}
					<div className="flex gap-2">
						<Button variant="primary" disabled={count === 0 || run.isPending} onClick={apply}>
							{tn(count, "Import {n} subscription", "Import {n} subscriptions")}
						</Button>
						<Button onClick={() => setParsed(undefined)}>{t("Discard")}</Button>
					</div>
				</>
			)}
			{error && <p className="text-xs text-bad">{error}</p>}
		</div>
	);
}

/** Export: everything as JSON, or spreadsheets as CSV. */
export function ExportData() {
	const link =
		"inline-flex h-9 items-center gap-2 rounded-lg border border-line bg-surface px-4 text-sm font-medium transition-colors hover:bg-raised [&_svg]:size-4";
	return (
		<div className="grid gap-3">
			<p className="text-sm text-muted">{t("Your data is yours: take it out whenever you want.")}</p>
			<div className="flex flex-wrap gap-2">
				<a className={link} href="/api/v1/export/subscriptions.csv" download>
					<Download /> {t("Subscriptions (CSV)")}
				</a>
				<a className={link} href="/api/v1/export/charges.csv" download>
					<Download /> {t("Charges (CSV)")}
				</a>
				<a className={link} href="/api/v1/export" download="exhale-export.json">
					<Download /> {t("Everything (JSON)")}
				</a>
			</div>
		</div>
	);
}

/** A private link that shows charges and deadlines in any calendar app. */
export function CalendarLink() {
	const qc = useQueryClient();
	const feed = useCalendarFeed();
	const create = useCalendarFeedCreate();
	const remove = useCalendarFeedRemove();
	const [url, setUrl] = useState<string>();
	const [copied, setCopied] = useState(false);
	const make = async () => {
		try {
			const r = await create.mutateAsync();
			setUrl(`${apiOrigin}${r.path}`);
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not create the link")));
		}
	};
	const drop = async () => {
		try {
			await remove.mutateAsync();
			setUrl(undefined);
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not remove the link")));
		}
	};
	return (
		<div className="grid gap-3">
			<p className="text-sm text-muted">
				{t(
					"Charges, trial ends and last days to cancel in Apple Calendar, Google Calendar or Outlook. Subscribe to the link; it updates by itself.",
				)}
			</p>
			{url ? (
				<div className="grid gap-2">
					<div className="flex items-center gap-2 rounded-lg border border-line bg-raised px-3 py-2">
						<code className="min-w-0 flex-1 font-mono text-xs break-all select-all">{url}</code>
						<Button
							size="icon"
							variant="ghost"
							aria-label={t("Copy the link")}
							onClick={async () => {
								try {
									await navigator.clipboard.writeText(url);
									setCopied(true);
									setTimeout(() => setCopied(false), 1500);
								} catch {}
							}}
						>
							{copied ? <Check className="text-ok" /> : <Copy />}
						</Button>
					</div>
					<p className="text-xs text-muted">
						{t("Copy it now: Exhale does not keep it. Anyone with the link can read your calendar.")}
					</p>
				</div>
			) : feed.data?.enabled ? (
				<p className="text-sm">
					{t("A link is active")}
					{feed.data.createdAt && t(" since {0}", date(feed.data.createdAt.slice(0, 10), "long"))}.
				</p>
			) : null}
			<div className="flex gap-2">
				<Button disabled={create.isPending} onClick={make}>
					{feed.data?.enabled ? t("Replace the link") : t("Create a calendar link")}
				</Button>
				{feed.data?.enabled && (
					<Button variant="danger" disabled={remove.isPending} onClick={drop}>
						{t("Remove it")}
					</Button>
				)}
			</div>
		</div>
	);
}
