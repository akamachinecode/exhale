import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Download, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { apiOrigin } from "@/components/connect";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	type ChargeMatch,
	getImportReviewQueryKey,
	type ImportItem,
	type ImportResult,
	importReviewDrop,
	type PriceChangeHint,
	type StatementMatch,
	setChargeStatus,
	updateSubscription,
	useAuthSession,
	useCalendarFeed,
	useCalendarFeedCreate,
	useCalendarFeedRemove,
	useImportReview,
	useImportSubscriptions,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";
import { date, decimal, every, money } from "@/lib/utils";

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

type Parsed = {
	source: string;
	items: ImportItem[];
	notes: string[];
	model?: string;
	match?: StatementMatch;
};

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

const channels: Record<string, string> = { telegram: "Telegram", slack: "Slack" };

const emptyPreview: ImportResult = {
	created: 0,
	duplicates: 0,
	errors: 0,
	items: [],
	newCategories: [],
	newPaymentMethods: [],
};

const payInput = (m: ChargeMatch, dryRun = false) => ({
	chargeId: m.charge.id,
	status: "paid" as const,
	chargedAmount: decimal(m.paid),
	chargedCurrency: m.paid.currency,
	dryRun,
});

/** Import: read a file, review the list, then bring in what is ticked. A
 * statement also confirms the charges Exhale expected. */
export function ImportData() {
	const qc = useQueryClient();
	const input = useRef<HTMLInputElement>(null);
	const run = useImportSubscriptions();
	const waiting = useImportReview();
	const [parsed, setParsed] = useState<Parsed>();
	const [fromChat, setFromChat] = useState(false);
	const [preview, setPreview] = useState<ImportResult>();
	const [picked, setPicked] = useState<boolean[]>([]);
	// Charges the statement confirms, kept when their dry run passed.
	const [pays, setPays] = useState<ChargeMatch[]>([]);
	const [payPicked, setPayPicked] = useState<boolean[]>([]);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const [pasted, setPasted] = useState("");
	// With the assistant on, a model reads what Exhale cannot read by itself.
	const assistant = Boolean(useAuthSession().data?.assistant);

	const load = async (p: Parsed) => {
		// A dry run says what would happen to each item and charge before
		// anything is saved.
		const dry =
			p.items.length > 0 ? await run.mutateAsync({ data: { items: p.items, dryRun: true } }) : emptyPreview;
		const candidates = (p.match?.matches ?? []).filter((m) => m.action === "mark_paid");
		const ok = await Promise.all(
			candidates.map((m) =>
				setChargeStatus(payInput(m, true)).then(
					() => true,
					() => false,
				),
			),
		);
		const kept = candidates.filter((_, i) => ok[i]);
		setParsed(p);
		setPreview(dry);
		setPicked((dry.items ?? []).map((o) => o.action === "create"));
		setPays(kept);
		setPayPicked(kept.map(() => true));
	};

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
			if (!res.ok) throw new ApiError(res.status, body?.detail ?? t("Could not read the file"));
			await load(body as Parsed);
			setFromChat(false);
			setPasted("");
		} catch (err) {
			setError(errText(err, t("Could not read the file")));
		} finally {
			setBusy(false);
			if (input.current) input.current.value = "";
		}
	};

	const openWaiting = async () => {
		const r = waiting.data?.review;
		if (!r) return;
		setError(undefined);
		setBusy(true);
		try {
			await load({
				source: r.result.source,
				items: r.result.items ?? [],
				notes: r.result.notes ?? [],
				model: r.result.model,
				match: r.result.match,
			});
			setFromChat(true);
		} catch (err) {
			setError(errText(err, t("Could not read the file")));
		} finally {
			setBusy(false);
		}
	};

	const close = async (forgetChat: boolean) => {
		setParsed(undefined);
		setPreview(undefined);
		if (forgetChat) {
			try {
				await importReviewDrop();
			} catch {}
			await qc.invalidateQueries({ queryKey: getImportReviewQueryKey() });
		}
	};

	const apply = async () => {
		if (!parsed) return;
		setError(undefined);
		setBusy(true);
		try {
			let paid = 0;
			const rises: { name: string; id: string; hint: PriceChangeHint }[] = [];
			for (const [i, m] of pays.entries()) {
				if (!payPicked[i]) continue;
				const r = await setChargeStatus(payInput(m));
				paid++;
				if (r.priceChange)
					rises.push({ name: m.charge.subscriptionName, id: m.charge.subscriptionId, hint: r.priceChange });
			}
			const items = parsed.items.filter((_, i) => picked[i]);
			const res = items.length > 0 ? await run.mutateAsync({ data: { items } }) : undefined;
			await qc.invalidateQueries();
			if (paid > 0) toast.success(tn(paid, "{n} charge marked paid", "{n} charges marked paid"));
			if (res) toast.success(tn(res.created, "{n} subscription imported", "{n} subscriptions imported"));
			// The bank took another amount: the same question as marking one paid.
			for (const r of rises) {
				toast(t("{0} took {1} instead of {2}", r.name, money(r.hint.charged), money(r.hint.expected)), {
					description: t(
						"{0}% · {1} a year. Is it the new price?",
						r.hint.changePercent,
						money(r.hint.yearlyDelta),
					),
					duration: 20_000,
					action: {
						label: t("Yes, from now on"),
						onClick: async () => {
							try {
								await updateSubscription({ id: r.id, price: decimal(r.hint.charged) });
								await qc.invalidateQueries();
								toast.success(t("New price saved for the next charges"));
							} catch (err) {
								toast.error(errText(err, t("Could not save")));
							}
						},
					},
				});
			}
			await close(fromChat);
		} catch (err) {
			setError(errText(err, t("Could not import")));
		} finally {
			setBusy(false);
		}
	};

	const count = picked.filter(Boolean).length;
	const payCount = payPicked.filter(Boolean).length;
	const match = parsed?.match;
	const alreadyPaid = (match?.matches ?? []).filter((m) => m.action === "already_paid").length;
	const missing = match?.missing ?? [];
	const review = waiting.data?.waiting ? waiting.data.review : undefined;
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
			{!parsed && review && (
				<div className="flex flex-wrap items-center gap-3 rounded-lg border border-line bg-raised px-3 py-2.5 text-sm">
					<span className="min-w-0 flex-1">
						{t("A file you sent in {0} is waiting for review", channels[review.channel] ?? review.channel)}
						<span className="text-muted"> · {sources[review.result.source] ?? review.result.source}</span>
					</span>
					<Button size="sm" variant="primary" disabled={busy} onClick={openWaiting}>
						{t("Review it")}
					</Button>
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => close(true)}>
						{t("Discard")}
					</Button>
				</div>
			)}
			{!parsed && (
				<>
					<p className="text-sm text-muted">
						{t("A Wallos database (")}
						<code className="font-mono text-xs">db/wallos.db</code>
						{t(
							") or export, a spreadsheet saved as CSV, an Exhale export, or your bank's statement (CSV or PDF): Exhale finds the charges that repeat and marks the ones it expected as paid.",
						)}{" "}
						{assistant
							? t("A scanned statement, a screenshot or pasted text is read by the assistant's model.")
							: t("With the built-in assistant on, it also reads a scanned statement or a screenshot.")}{" "}
						{t("You review the list before anything is saved.")}
					</p>
					<div>
						<Button disabled={busy} onClick={() => input.current?.click()}>
							<Upload /> {busy ? t("Reading…") : t("Choose a file")}
						</Button>
					</div>
					<div className="grid gap-2">
						<textarea
							value={pasted}
							onChange={(e) => setPasted(e.target.value)}
							rows={3}
							placeholder={
								assistant
									? t("…or paste text: an email from a service, lines from a statement")
									: t("…or paste lines from a statement: a date, a description and an amount on each")
							}
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
							{match?.from && ` · ${date(match.from, "long")} – ${date(match.to, "long")}`} ·{" "}
							{parsed.items.length} {t("found ·")} {preview.duplicates} {t("already in Exhale")}
							{preview.errors > 0 && t(" · {0} with problems", preview.errors)}
						</span>
					</p>
					{parsed.notes.map((n) => (
						<p key={n} className="text-xs text-muted">
							{n}
						</p>
					))}
					{pays.length > 0 && (
						<div className="grid gap-1.5">
							<p className="text-xs font-medium text-muted">{t("Paid, according to the statement")}</p>
							<ul className="max-h-72 divide-y divide-line overflow-y-auto rounded-lg border border-line">
								{pays.map((m, i) => {
									const other =
										m.paid.minor !== m.charge.amount.minor || m.paid.currency !== m.charge.amount.currency;
									return (
										<li key={m.charge.id} className="flex items-start gap-3 px-3 py-2.5 text-sm">
											<input
												type="checkbox"
												className="mt-0.5 size-4 accent-[var(--accent)]"
												checked={Boolean(payPicked[i])}
												onChange={(e) =>
													setPayPicked(payPicked.map((p, j) => (j === i ? e.target.checked : p)))
												}
												aria-label={t("Mark {0} paid", m.charge.subscriptionName)}
											/>
											<span className="min-w-0 flex-1">
												<span className="flex flex-wrap items-center gap-2">
													<span className="font-medium">{m.charge.subscriptionName}</span>
													{m.priceChange && <Badge tone="warn">{t("New price?")}</Badge>}
												</span>
												<span className="block text-xs text-muted">
													{t(
														"{0} on {1} · due {2}",
														money(m.paid),
														date(m.transaction.date),
														date(m.charge.dueDate),
													)}
													{other && ` · ${t("expected {0}", money(m.charge.amount))}`}
												</span>
												<span className="block truncate text-xs text-faint">
													“{m.transaction.description}”
												</span>
											</span>
										</li>
									);
								})}
							</ul>
						</div>
					)}
					{(alreadyPaid > 0 || (match && pays.length === 0)) && (
						<p className="text-xs text-muted">
							{pays.length === 0 && alreadyPaid === 0
								? t("No payment in this statement matches a charge Exhale expected.")
								: tn(
										alreadyPaid,
										"{n} charge in it was already marked paid.",
										"{n} charges in it were already marked paid.",
									)}
						</p>
					)}
					{missing.length > 0 && (
						<div className="grid gap-1.5">
							<p className="text-xs font-medium text-muted">{t("Expected but not in this statement")}</p>
							<ul className="grid gap-1 rounded-lg border border-line px-3 py-2.5 text-sm">
								{missing.map((c) => (
									<li key={c.id} className="flex justify-between gap-3">
										<span>{c.subscriptionName}</span>
										<span className="tnum text-muted">
											{money(c.amount)} · {date(c.dueDate)}
										</span>
									</li>
								))}
							</ul>
						</div>
					)}
					{parsed.items.length > 0 && match && (
						<p className="text-xs font-medium text-muted">{t("New subscriptions")}</p>
					)}
					{parsed.items.length > 0 && (
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
					)}
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
						<Button variant="primary" disabled={count + payCount === 0 || busy} onClick={apply}>
							{pays.length > 0
								? tn(count + payCount, "Apply {n} change", "Apply {n} changes")
								: tn(count, "Import {n} subscription", "Import {n} subscriptions")}
						</Button>
						<Button onClick={() => close(fromChat)}>{t("Discard")}</Button>
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
