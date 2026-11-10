import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowUp, Check, Loader2, RotateCcw, Sparkles, Undo2, X } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, PasswordInput, Select } from "@/components/ui/field";
import {
	type AssistantOutcome,
	type AssistantStatus,
	type AssistantTurn,
	assistantDecide,
	assistantMessage,
	type MonthUsage,
	type Proposal,
	type UsageEntry,
	useAuthSession,
	useSettingsAssistant,
	useSettingsRemoveAssistant,
	useSettingsSetAssistant,
	useSettingsTestAssistant,
} from "@/gen/api";
import { ApiError, problemDetail } from "@/lib/fetcher";
import { capitalize, locale, say, t, tn } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// Ask Exhale (ADR 0018, part B): plain language in the web. The model only
// prepares changes; each one waits here for Confirm or Discard, and what was
// confirmed can be undone. The conversation lives in this tab: the panel can
// be closed and reopened from any page without losing it.

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

type Entry =
	| { kind: "user"; id: number; text: string }
	| { kind: "answer"; id: number; turn: AssistantTurn }
	| { kind: "error"; id: number; text: string };

type State = {
	open: boolean;
	busy: boolean;
	/** The next message starts a new conversation on the server too. */
	fresh: boolean;
	entries: Entry[];
	/** The answer while it is being written. */
	live: string;
	/** What happened to each proposal, by its id. */
	outcomes: Record<string, AssistantOutcome>;
	deciding: Record<string, boolean>;
};

let state: State = {
	open: false,
	busy: false,
	fresh: true,
	entries: [],
	live: "",
	outcomes: {},
	deciding: {},
};
let nextId = 1;
const listeners = new Set<() => void>();

function set(patch: Partial<State>) {
	state = { ...state, ...patch };
	for (const l of listeners) l();
}

function subscribe(l: () => void) {
	listeners.add(l);
	return () => listeners.delete(l);
}

const useAssistant = () => useSyncExternalStore(subscribe, () => state);

async function ask(text: string) {
	text = text.trim();
	if (!text || state.busy) return;
	set({
		open: true,
		busy: true,
		live: "",
		entries: [...state.entries, { kind: "user", id: nextId++, text }],
	});
	try {
		const turn = await askStreaming(text, state.fresh, (live) => set({ live }));
		set({ fresh: false, live: "", entries: [...state.entries, { kind: "answer", id: nextId++, turn }] });
	} catch (err) {
		const entry: Entry = { kind: "error", id: nextId++, text: errText(err, t("Something went wrong.")) };
		set({ entries: [...state.entries, entry] });
	} finally {
		set({ busy: false, live: "" });
	}
}

/** Asks with the answer streamed as it is written (Server-Sent Events over
 * a POST). The cards come with the finished turn. A server without the
 * streaming endpoint gets the plain request. */
async function askStreaming(
	text: string,
	reset: boolean,
	onText: (text: string) => void,
): Promise<AssistantTurn> {
	const res = await fetch("/api/v1/assistant/messages/stream", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ text, reset }),
	});
	const type = res.headers.get("Content-Type") ?? "";
	if (!res.ok || !res.body || !type.startsWith("text/event-stream")) {
		if (res.status === 404 || res.status === 405) return assistantMessage({ text, reset });
		const body = await res.json().catch(() => undefined);
		throw new ApiError(res.status, problemDetail(body, t("Something went wrong.")));
	}
	const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
	let buffer = "";
	let answer = "";
	for (;;) {
		const { value, done } = await reader.read();
		if (done) break;
		buffer += value;
		for (let end = buffer.indexOf("\n\n"); end >= 0; end = buffer.indexOf("\n\n")) {
			const block = buffer.slice(0, end);
			buffer = buffer.slice(end + 2);
			let event = "message";
			let data = "";
			for (const line of block.split("\n")) {
				if (line.startsWith("event:")) event = line.slice(6).trim();
				else if (line.startsWith("data:")) data += line.slice(5).trimStart();
			}
			const v = data ? JSON.parse(data) : {};
			if (event === "delta") answer += v.text;
			else if (event === "text") answer = v.text;
			else if (event === "turn") return v as AssistantTurn;
			else if (event === "error")
				throw new ApiError(v.status ?? 500, problemDetail(v, t("Something went wrong.")));
			if (event === "delta" || event === "text") onText(answer);
		}
	}
	throw new ApiError(502, t("The answer was cut off. Try again."));
}

const examples = ["What do I pay per month?", "What renews this week?", "Add Netflix, 13.99 on the 6th"];

/** Where messages go, in words a person recognises. */
function providerName(s?: AssistantStatus) {
	if (!s?.provider) return t("the model provider");
	if (s.provider === "anthropic") return s.baseUrl ? hostOf(s.baseUrl) : "Anthropic";
	return s.baseUrl ? hostOf(s.baseUrl) : "OpenAI";
}

function hostOf(url: string) {
	try {
		return new URL(url).host;
	} catch {
		return url;
	}
}

/** The box on Overview: type a question, or open the panel with ⌘K. */
export function AskExhale({ className }: { className?: string }) {
	const session = useAuthSession();
	const [text, setText] = useState("");
	if (!session.data?.assistant) return null;
	const onSubmit = (e: FormEvent) => {
		e.preventDefault();
		if (text.trim()) ask(text);
		else set({ open: true });
		setText("");
	};
	return (
		<form onSubmit={onSubmit} className={cn("relative", className)}>
			<Sparkles className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-faint" />
			<input
				value={text}
				onChange={(e) => setText(e.target.value)}
				placeholder={t("Ask Exhale: what do I pay per month? add Netflix, 13.99 on the 6th…")}
				aria-label={t("Ask Exhale")}
				maxLength={4000}
				className="h-11 w-full rounded-xl border border-line bg-surface pr-16 pl-10 text-sm text-fg placeholder:text-faint focus:border-accent focus:outline-2 focus:outline-accent/25"
			/>
			<kbd className="pointer-events-none absolute top-1/2 right-3 hidden -translate-y-1/2 rounded-md border border-line px-1.5 py-0.5 font-mono text-[11px] text-faint sm:block">
				{navigator.platform.startsWith("Mac") ? "⌘K" : "Ctrl K"}
			</kbd>
		</form>
	);
}

/** The conversation, mounted once in the layout. ⌘K or Ctrl+K opens it. */
export function AssistantPanel() {
	const session = useAuthSession();
	const enabled = Boolean(session.data?.assistant);
	const status = useSettingsAssistant({ query: { enabled } });
	const s = useAssistant();
	const [text, setText] = useState("");
	const end = useRef<HTMLDivElement>(null);
	const form = useRef<HTMLFormElement>(null);

	useEffect(() => {
		if (!enabled) return;
		const onKey = (e: KeyboardEvent) => {
			if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
				e.preventDefault();
				set({ open: !state.open });
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [enabled]);

	// The dialog puts the focus on its close button; the person came to type.
	useEffect(() => {
		if (!s.open) return;
		const t = setTimeout(() => form.current?.querySelector("input")?.focus(), 60);
		return () => clearTimeout(t);
	}, [s.open]);

	const count = s.entries.length;
	// biome-ignore lint/correctness/useExhaustiveDependencies: scroll when something is added or decided
	useEffect(() => {
		end.current?.scrollIntoView({ block: "end" });
	}, [count, s.busy, s.open, s.outcomes, s.live]);

	if (!enabled) return null;
	const onSubmit = (e: FormEvent) => {
		e.preventDefault();
		ask(text);
		setText("");
	};
	const restart = () => {
		set({ entries: [], live: "", outcomes: {}, deciding: {}, fresh: true });
	};
	return (
		<Dialog
			open={s.open}
			onOpenChange={(open) => set({ open })}
			title={t("Ask Exhale")}
			description={t("Questions get an answer. Changes wait for you to confirm.")}
		>
			<div className="grid gap-4">
				{count === 0 ? (
					<div className="flex flex-wrap gap-2">
						{examples.map((q) => (
							<button
								key={q}
								type="button"
								onClick={() => ask(q)}
								className="rounded-full border border-line px-3 py-1.5 text-[13px] text-muted transition-colors hover:bg-raised hover:text-fg"
							>
								{q}
							</button>
						))}
					</div>
				) : (
					<div className="grid max-h-[52dvh] gap-3 overflow-y-auto pr-1" aria-live="polite">
						{s.entries.map((e) =>
							e.kind === "user" ? (
								<p
									key={e.id}
									className="ml-8 justify-self-end rounded-2xl rounded-br-md bg-raised px-3.5 py-2 text-sm whitespace-pre-wrap"
								>
									{e.text}
								</p>
							) : e.kind === "error" ? (
								<p key={e.id} className="text-sm text-bad">
									{e.text}
								</p>
							) : (
								<Answer key={e.id} turn={e.turn} />
							),
						)}
						{s.busy &&
							(s.live ? (
								<p className="text-sm leading-relaxed whitespace-pre-wrap">
									{s.live}
									<span className="ml-0.5 inline-block animate-pulse text-faint">▍</span>
								</p>
							) : (
								<p className="flex items-center gap-2 text-sm text-muted">
									<Loader2 className="size-4 animate-spin" /> {t("Thinking…")}
								</p>
							))}
						<div ref={end} />
					</div>
				)}
				<form ref={form} onSubmit={onSubmit} className="flex items-center gap-2">
					<Input
						value={text}
						onChange={(e) => setText(e.target.value)}
						placeholder={count === 0 ? t("Ask about your subscriptions, or say what to change") : t("Reply…")}
						aria-label={t("Message")}
						maxLength={4000}
					/>
					<Button
						type="submit"
						variant="primary"
						size="icon"
						aria-label={t("Send")}
						disabled={s.busy || !text.trim()}
						className="size-9 shrink-0"
					>
						<ArrowUp />
					</Button>
				</form>
				<div className="flex items-start justify-between gap-3">
					<p className="text-xs text-faint">
						{t("Sent to")} {providerName(status.data)}
						{t(": your message, a briefing of your subscriptions and what Exhale looks up to answer.")}
					</p>
					{count > 0 && (
						<button
							type="button"
							onClick={restart}
							className="flex shrink-0 items-center gap-1 text-xs text-muted hover:text-fg"
						>
							<RotateCcw className="size-3" /> {t("New")}
						</button>
					)}
				</div>
			</div>
		</Dialog>
	);
}

function Answer({ turn }: { turn: AssistantTurn }) {
	const mentions = turn.mentions ?? [];
	return (
		<div className="grid gap-2">
			{turn.text && <p className="text-sm leading-relaxed whitespace-pre-wrap">{turn.text}</p>}
			{turn.note && <p className="text-xs text-muted">{say(turn.note)}</p>}
			{(turn.proposals ?? []).map((p) => (
				<ProposalCard key={p.id} proposal={p} />
			))}
			{mentions.length > 0 && (
				<p className="flex flex-wrap gap-1.5">
					{mentions.map((m) => (
						<Link
							key={m.id}
							to="/subscriptions/$id"
							params={{ id: m.id }}
							onClick={() => set({ open: false })}
							className="rounded-full border border-line px-2.5 py-0.5 text-xs text-muted transition-colors hover:bg-raised hover:text-fg"
						>
							{m.name}
						</Link>
					))}
				</p>
			)}
		</div>
	);
}

const done: Record<string, string> = {
	applied: t("Done"),
	discarded: t("Discarded"),
	undone: t("Undone"),
	failed: t("Not done"),
};

/** A change the assistant prepared. Exhale writes the card, not the model. */
function ProposalCard({ proposal: p }: { proposal: Proposal }) {
	const qc = useQueryClient();
	const s = useAssistant();
	const outcome = s.outcomes[p.id];
	const busy = Boolean(s.deciding[p.id]);
	const decide = async (action: "confirm" | "discard" | "undo") => {
		set({ deciding: { ...state.deciding, [p.id]: true } });
		try {
			const out = await assistantDecide(p.id, { action });
			set({ outcomes: { ...state.outcomes, [p.id]: out } });
			if (action !== "discard") await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not do that")));
		} finally {
			set({ deciding: { ...state.deciding, [p.id]: false } });
		}
	};
	const status = outcome?.status ?? "pending";
	const lines = (status === "pending" ? p.lines : outcome?.lines) ?? [];
	return (
		<div className={cn("rounded-lg border border-line px-3.5 py-3", p.destructive && "border-bad/40")}>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{say(p.title)}</p>
				{status !== "pending" && (
					<Badge tone={status === "applied" ? "ok" : status === "failed" ? "bad" : "neutral"}>
						{status === "applied" && <Check className="size-3" />}
						{done[status] ?? status}
					</Badge>
				)}
			</div>
			{lines.map((l, i) => (
				<p
					// biome-ignore lint/suspicious/noArrayIndexKey: the lines of a card never move
					key={i}
					className={cn("mt-0.5 text-xs text-muted", status === "failed" && "text-bad")}
				>
					{capitalize(say(l))}
				</p>
			))}
			{status === "pending" && (
				<div className="mt-3 flex gap-2">
					<Button
						size="sm"
						variant={p.destructive ? "danger" : "primary"}
						disabled={busy}
						onClick={() => decide("confirm")}
					>
						<Check /> {t("Confirm")}
					</Button>
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => decide("discard")}>
						<X /> {t("Discard")}
					</Button>
				</div>
			)}
			{status === "applied" && (outcome?.canUndo || outcome?.subscriptionId) && (
				<div className="mt-3 flex items-center gap-3">
					{outcome.canUndo && (
						<Button size="sm" disabled={busy} onClick={() => decide("undo")}>
							<Undo2 /> {t("Undo")}
						</Button>
					)}
					{outcome.subscriptionId && (
						<Link
							to="/subscriptions/$id"
							params={{ id: outcome.subscriptionId }}
							onClick={() => set({ open: false })}
							className="text-xs font-medium underline-offset-4 hover:underline"
						>
							{t("Open it")}
						</Link>
					)}
				</div>
			)}
		</div>
	);
}

const providers = [
	{ id: "anthropic", label: "Claude (Anthropic)" },
	{ id: "openai", label: t("OpenAI-compatible: OpenAI, OpenRouter, Ollama, LM Studio") },
] as const;

/** Settings → Built-in assistant: bring your own model. Owners only. */
export function AssistantSettings() {
	const qc = useQueryClient();
	const session = useAuthSession();
	const status = useSettingsAssistant();
	const save = useSettingsSetAssistant();
	const test = useSettingsTestAssistant();
	const remove = useSettingsRemoveAssistant();
	const s = status.data;
	const isOwner = session.data?.user?.role === "owner";
	const [form, setForm] = useState<{
		provider: string;
		apiKey: string;
		model: string;
		baseUrl: string;
		on: boolean;
		priceIn: string;
		priceOut: string;
	}>();
	const [error, setError] = useState<string>();
	const [result, setResult] = useState<{ ok: boolean; message: string }>();

	if (status.isPending || !s) return <Loader2 className="size-4 animate-spin text-muted" />;
	const sent = (
		<p className="text-xs text-muted">
			{t(
				"While it is on, what you type to it is sent to the provider you choose, with a briefing of your household (names of the people you share with, the subscriptions you can see, coming charges) and what Exhale looks up to answer. A PDF, a picture or text you import is sent too. Nothing is sent while it is off, and nothing ever goes to the makers of Exhale.",
			)}
		</p>
	);
	if (!isOwner) {
		return (
			<div className="grid gap-3">
				<p className="text-sm">
					{s.enabled
						? t("On, with {0} at {1}.", s.model, providerName(s))
						: t("Off. An owner can turn it on.")}
				</p>
				<MonthTotal month={s.month} />
				{sent}
			</div>
		);
	}
	const f = form ?? {
		provider: s.provider ?? "anthropic",
		apiKey: "",
		model: s.model ?? "",
		baseUrl: s.baseUrl ?? "",
		on: s.configured ? s.on : true,
		priceIn: s.priceSource === "settings" ? (s.price?.input ?? "") : "",
		priceOut: s.priceSource === "settings" ? (s.price?.output ?? "") : "",
	};
	const locked = (field: string) => (s.locked ?? []).includes(field);
	const onServer = "Set on the server";
	const edit = (patch: Partial<typeof f>) => {
		setForm({ ...f, ...patch });
		setResult(undefined);
	};
	const refresh = () => qc.invalidateQueries();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		setResult(undefined);
		try {
			await save.mutateAsync({
				data: {
					provider: f.provider as "anthropic" | "openai",
					apiKey: f.apiKey || undefined,
					model: f.model,
					baseUrl: f.baseUrl,
					enabled: f.on,
					price: { input: f.priceIn, output: f.priceOut },
				},
			});
			setForm(undefined);
			await refresh();
			toast.success(f.on ? t("The assistant is on") : t("Saved. The assistant is off"));
		} catch (err) {
			setError(errText(err, t("Could not save")));
		}
	};
	const onTest = async () => {
		setResult(undefined);
		try {
			const r = await test.mutateAsync();
			setResult({ ok: r.ok, message: say(r.message) });
		} catch (err) {
			setResult({ ok: false, message: errText(err, t("Could not run the test")) });
		}
	};
	const onRemove = async () => {
		try {
			await remove.mutateAsync();
			setForm(undefined);
			setResult(undefined);
			await refresh();
		} catch (err) {
			setError(errText(err, t("Could not remove it")));
		}
	};
	const claude = f.provider === "anthropic";
	return (
		<form onSubmit={onSubmit} className="grid gap-4">
			<div className="grid gap-3 sm:grid-cols-2">
				<Field label={t("Provider")} hint={locked("provider") ? onServer : undefined}>
					<Select
						value={f.provider}
						disabled={locked("provider")}
						onChange={(e) => edit({ provider: e.target.value, model: "", baseUrl: "" })}
					>
						{providers.map((p) => (
							<option key={p.id} value={p.id}>
								{p.label}
							</option>
						))}
					</Select>
				</Field>
				<Field
					label={t("API key")}
					hint={
						locked("apiKey")
							? onServer
							: s.hasKey
								? t("A key is saved. Leave empty to keep it.")
								: claude
									? t("From console.anthropic.com.")
									: t("Leave empty for a local model.")
					}
				>
					<PasswordInput
						value={f.apiKey}
						disabled={locked("apiKey")}
						onChange={(e) => edit({ apiKey: e.target.value })}
						placeholder={s.hasKey ? "••••••••" : claude ? "sk-ant-…" : ""}
						autoComplete="off"
					/>
				</Field>
				<Field
					label={t("Model")}
					hint={locked("model") ? onServer : claude ? t("Default: {0}", s.defaultModel) : undefined}
				>
					<Input
						value={f.model}
						disabled={locked("model")}
						onChange={(e) => edit({ model: e.target.value })}
						placeholder={claude ? s.defaultModel : "gpt-5, llama3.1…"}
						required={!claude}
					/>
				</Field>
				{(!claude || f.baseUrl) && (
					<Field
						label={t("Address")}
						hint={locked("baseUrl") ? onServer : t("Empty for OpenAI. Ollama: http://localhost:11434/v1")}
					>
						<Input
							value={f.baseUrl}
							disabled={locked("baseUrl")}
							onChange={(e) => edit({ baseUrl: e.target.value })}
							placeholder="https://api.openai.com/v1"
							inputMode="url"
						/>
					</Field>
				)}
			</div>
			<Field
				label={t("Price, in US dollars per million tokens")}
				hint={
					s.priceSource === "default"
						? t("From Exhale's price list. Type a price to replace it.")
						: s.priceSource === "settings"
							? t("Your price. Empty both to forget it.")
							: t("Unknown for this model: costs show as tokens only. A local model costs 0.")
				}
			>
				<div className="grid grid-cols-2 gap-3">
					<Input
						value={f.priceIn}
						onChange={(e) => edit({ priceIn: e.target.value })}
						placeholder={s.priceSource === "default" ? t("Input: {0}", s.price?.input) : t("Input")}
						inputMode="decimal"
						aria-label={t("Input price")}
					/>
					<Input
						value={f.priceOut}
						onChange={(e) => edit({ priceOut: e.target.value })}
						placeholder={s.priceSource === "default" ? t("Output: {0}", s.price?.output) : t("Output")}
						inputMode="decimal"
						aria-label={t("Output price")}
					/>
				</div>
			</Field>
			<label className="flex items-center gap-2.5 text-sm">
				<input
					type="checkbox"
					className="size-4 accent-[var(--accent)]"
					checked={f.on}
					onChange={(e) => edit({ on: e.target.checked })}
				/>
				{t("Answer in the web and in chat, and read statements you import")}
			</label>
			{sent}
			<div className="flex flex-wrap items-center gap-2">
				<Button type="submit" variant="primary" disabled={save.isPending}>
					{t("Save")}
				</Button>
				<Button disabled={!s.configured || test.isPending || Boolean(form)} onClick={onTest}>
					{test.isPending ? t("Testing…") : t("Test")}
				</Button>
				{(s.configured || s.hasKey) && (
					<Button variant="danger" disabled={remove.isPending} onClick={onRemove}>
						{t("Remove")}
					</Button>
				)}
				{s.configured && !form && (
					<Badge tone={s.enabled ? "ok" : "neutral"}>{s.enabled ? t("On") : t("Off")}</Badge>
				)}
			</div>
			<MonthTotal month={s.month} />
			{result && <p className={cn("text-xs", result.ok ? "text-ok" : "text-bad")}>{result.message}</p>}
			{error && <p className="text-xs text-bad">{error}</p>}
		</form>
	);
}

/** A cost estimated by the server, in US dollars ("0.0123"). */
export function dollars(usd: string) {
	const n = Number(usd);
	const fmt = (v: number) =>
		new Intl.NumberFormat(locale, { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(v);
	return n > 0 && n < 0.0001 ? `< ${fmt(0.0001)}` : fmt(n);
}

const count = (n: number) => new Intl.NumberFormat(locale).format(n);

/** What the assistant cost this month (ADR 0018, safety rule 6). */
function MonthTotal({ month: m }: { month?: MonthUsage }) {
	if (!m || m.questions === 0) return null;
	return (
		<p className="text-xs text-muted">
			{t("This month")}: {tn(m.questions, "{n} question", "{n} questions")} ·{" "}
			{t("{0} tokens", count(m.inputTokens + m.outputTokens))}
			{m.costUsd && <> · {t("about {0}", dollars(m.costUsd))}</>}
			{m.unpriced > 0 && <> · {tn(m.unpriced, "{n} without a price", "{n} without a price")}</>}
		</p>
	);
}

const surfaces: Record<string, string> = {
	web: t("in the web"),
	telegram: t("in Telegram"),
	slack: t("in Slack"),
	import: t("reading an import"),
};

/** Activity → Assistant: each question, its model, tokens and cost. */
export function AssistantActivity({ entries }: { entries: UsageEntry[] }) {
	return (
		<Card>
			{entries.length === 0 ? (
				<p className="px-5 py-12 text-center text-sm text-muted">{t("No questions yet.")}</p>
			) : (
				<ul className="divide-y divide-line">
					{entries.map((e) => (
						<li key={e.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-3 text-sm">
							<span className="grid size-8 place-items-center rounded-full bg-raised text-muted">
								<Sparkles className="size-4" />
							</span>
							<div className="min-w-0 flex-1">
								<p className="truncate">
									<span className="font-medium">{e.member || t("Someone")}</span>{" "}
									<span className="text-muted">
										{e.kind === "import" ? t("had a file read") : t("asked")}{" "}
										{surfaces[e.surface] ?? e.surface}
									</span>
								</p>
								<p className="text-xs text-faint">
									{new Date(e.at).toLocaleString(locale)} · {e.model} ·{" "}
									{tn(e.rounds, "{n} call", "{n} calls")} ·{" "}
									{t("{0} in, {1} out", count(e.inputTokens), count(e.outputTokens))}
								</p>
							</div>
							{e.failed && <Badge tone="bad">{t("Failed")}</Badge>}
							<span className="font-mono text-xs tabular-nums text-muted">
								{e.costUsd ? dollars(e.costUsd) : t("no price")}
							</span>
						</li>
					))}
				</ul>
			)}
		</Card>
	);
}
