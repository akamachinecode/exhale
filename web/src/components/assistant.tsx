import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowUp, Check, Loader2, RotateCcw, Sparkles, Undo2, X } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, PasswordInput, Select } from "@/components/ui/field";
import {
	type AssistantOutcome,
	type AssistantStatus,
	type AssistantTurn,
	assistantDecide,
	assistantMessage,
	type Proposal,
	useAuthSession,
	useSettingsAssistant,
	useSettingsRemoveAssistant,
	useSettingsSetAssistant,
	useSettingsTestAssistant,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
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
	/** What happened to each proposal, by its id. */
	outcomes: Record<string, AssistantOutcome>;
	deciding: Record<string, boolean>;
};

let state: State = { open: false, busy: false, fresh: true, entries: [], outcomes: {}, deciding: {} };
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
	set({ open: true, busy: true, entries: [...state.entries, { kind: "user", id: nextId++, text }] });
	try {
		const turn = await assistantMessage({ text, reset: state.fresh });
		set({ fresh: false, entries: [...state.entries, { kind: "answer", id: nextId++, turn }] });
	} catch (err) {
		const entry: Entry = { kind: "error", id: nextId++, text: errText(err, "Something went wrong.") };
		set({ entries: [...state.entries, entry] });
	} finally {
		set({ busy: false });
	}
}

const examples = ["What do I pay per month?", "What renews this week?", "Add Netflix, 13.99 on the 6th"];

/** Where messages go, in words a person recognises. */
function providerName(s?: AssistantStatus) {
	if (!s?.provider) return "the model provider";
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
				placeholder="Ask Exhale: what do I pay per month? add Netflix, 13.99 on the 6th…"
				aria-label="Ask Exhale"
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
	}, [count, s.busy, s.open, s.outcomes]);

	if (!enabled) return null;
	const onSubmit = (e: FormEvent) => {
		e.preventDefault();
		ask(text);
		setText("");
	};
	const restart = () => {
		set({ entries: [], outcomes: {}, deciding: {}, fresh: true });
	};
	return (
		<Dialog
			open={s.open}
			onOpenChange={(open) => set({ open })}
			title="Ask Exhale"
			description="Questions get an answer. Changes wait for you to confirm."
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
						{s.busy && (
							<p className="flex items-center gap-2 text-sm text-muted">
								<Loader2 className="size-4 animate-spin" /> Thinking…
							</p>
						)}
						<div ref={end} />
					</div>
				)}
				<form ref={form} onSubmit={onSubmit} className="flex items-center gap-2">
					<Input
						value={text}
						onChange={(e) => setText(e.target.value)}
						placeholder={count === 0 ? "Ask about your subscriptions, or say what to change" : "Reply…"}
						aria-label="Message"
						maxLength={4000}
					/>
					<Button
						type="submit"
						variant="primary"
						size="icon"
						aria-label="Send"
						disabled={s.busy || !text.trim()}
						className="size-9 shrink-0"
					>
						<ArrowUp />
					</Button>
				</form>
				<div className="flex items-start justify-between gap-3">
					<p className="text-xs text-faint">
						Sent to {providerName(status.data)}: your message, a briefing of your subscriptions and what
						Exhale looks up to answer.
					</p>
					{count > 0 && (
						<button
							type="button"
							onClick={restart}
							className="flex shrink-0 items-center gap-1 text-xs text-muted hover:text-fg"
						>
							<RotateCcw className="size-3" /> New
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
			{turn.note && <p className="text-xs text-muted">{turn.note}</p>}
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
	applied: "Done",
	discarded: "Discarded",
	undone: "Undone",
	failed: "Not done",
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
			toast.error(errText(err, "Could not do that"));
		} finally {
			set({ deciding: { ...state.deciding, [p.id]: false } });
		}
	};
	const status = outcome?.status ?? "pending";
	const lines = (status === "pending" ? p.lines : outcome?.lines) ?? [];
	return (
		<div className={cn("rounded-lg border border-line px-3.5 py-3", p.destructive && "border-bad/40")}>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{p.title}</p>
				{status !== "pending" && (
					<Badge tone={status === "applied" ? "ok" : status === "failed" ? "bad" : "neutral"}>
						{status === "applied" && <Check className="size-3" />}
						{done[status] ?? status}
					</Badge>
				)}
			</div>
			{lines.map((l) => (
				<p key={l} className={cn("mt-0.5 text-xs text-muted", status === "failed" && "text-bad")}>
					{l}
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
						<Check /> Confirm
					</Button>
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => decide("discard")}>
						<X /> Discard
					</Button>
				</div>
			)}
			{status === "applied" && (outcome?.canUndo || outcome?.subscriptionId) && (
				<div className="mt-3 flex items-center gap-3">
					{outcome.canUndo && (
						<Button size="sm" disabled={busy} onClick={() => decide("undo")}>
							<Undo2 /> Undo
						</Button>
					)}
					{outcome.subscriptionId && (
						<Link
							to="/subscriptions/$id"
							params={{ id: outcome.subscriptionId }}
							onClick={() => set({ open: false })}
							className="text-xs font-medium underline-offset-4 hover:underline"
						>
							Open it
						</Link>
					)}
				</div>
			)}
		</div>
	);
}

const providers = [
	{ id: "anthropic", label: "Claude (Anthropic)" },
	{ id: "openai", label: "OpenAI-compatible: OpenAI, OpenRouter, Ollama, LM Studio" },
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
	}>();
	const [error, setError] = useState<string>();
	const [result, setResult] = useState<{ ok: boolean; message: string }>();

	if (status.isPending || !s) return <Loader2 className="size-4 animate-spin text-muted" />;
	const sent = (
		<p className="text-xs text-muted">
			While it is on, what you type to it is sent to the provider you choose, with a briefing of your
			household (names of the people you share with, the subscriptions you can see, coming charges) and what
			Exhale looks up to answer. A PDF, a picture or text you import is sent too. Nothing is sent while it is
			off, and nothing ever goes to the makers of Exhale.
		</p>
	);
	if (!isOwner) {
		return (
			<div className="grid gap-3">
				<p className="text-sm">
					{s.enabled ? `On, with ${s.model} at ${providerName(s)}.` : "Off. An owner can turn it on."}
				</p>
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
				},
			});
			setForm(undefined);
			await refresh();
			toast.success(f.on ? "The assistant is on" : "Saved. The assistant is off");
		} catch (err) {
			setError(errText(err, "Could not save"));
		}
	};
	const onTest = async () => {
		setResult(undefined);
		try {
			setResult(await test.mutateAsync());
		} catch (err) {
			setResult({ ok: false, message: errText(err, "Could not run the test") });
		}
	};
	const onRemove = async () => {
		try {
			await remove.mutateAsync();
			setForm(undefined);
			setResult(undefined);
			await refresh();
		} catch (err) {
			setError(errText(err, "Could not remove it"));
		}
	};
	const claude = f.provider === "anthropic";
	return (
		<form onSubmit={onSubmit} className="grid gap-4">
			<div className="grid gap-3 sm:grid-cols-2">
				<Field label="Provider" hint={locked("provider") ? onServer : undefined}>
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
					label="API key"
					hint={
						locked("apiKey")
							? onServer
							: s.hasKey
								? "A key is saved. Leave empty to keep it."
								: claude
									? "From console.anthropic.com."
									: "Leave empty for a local model."
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
					label="Model"
					hint={locked("model") ? onServer : claude ? `Default: ${s.defaultModel}` : undefined}
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
						label="Address"
						hint={locked("baseUrl") ? onServer : "Empty for OpenAI. Ollama: http://localhost:11434/v1"}
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
			<label className="flex items-center gap-2.5 text-sm">
				<input
					type="checkbox"
					className="size-4 accent-[var(--accent)]"
					checked={f.on}
					onChange={(e) => edit({ on: e.target.checked })}
				/>
				Answer in the web and in chat, and read statements you import
			</label>
			{sent}
			<div className="flex flex-wrap items-center gap-2">
				<Button type="submit" variant="primary" disabled={save.isPending}>
					Save
				</Button>
				<Button disabled={!s.configured || test.isPending || Boolean(form)} onClick={onTest}>
					{test.isPending ? "Testing…" : "Test"}
				</Button>
				{(s.configured || s.hasKey) && (
					<Button variant="danger" disabled={remove.isPending} onClick={onRemove}>
						Remove
					</Button>
				)}
				{s.configured && !form && (
					<Badge tone={s.enabled ? "ok" : "neutral"}>{s.enabled ? "On" : "Off"}</Badge>
				)}
			</div>
			{result && <p className={cn("text-xs", result.ok ? "text-ok" : "text-bad")}>{result.message}</p>}
			{error && <p className="text-xs text-bad">{error}</p>}
		</form>
	);
}
