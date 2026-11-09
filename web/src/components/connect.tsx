import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, ExternalLink, Loader2, Send, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/field";
import {
	type LinkCodeOut,
	useAuthCreateToken,
	useAuthLinkTelegram,
	useAuthListLinks,
	useAuthListTokens,
	useAuthRevokeToken,
	useAuthSession,
	useAuthUnlink,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { cn } from "@/lib/utils";

// Connections shared by the welcome page, settings and the AI agents page:
// linking Telegram, and tokens for Claude and other agents.

/** Where AI clients on this machine reach the API (the Go server, not Vite). */
export const apiOrigin = import.meta.env.DEV ? "http://127.0.0.1:8484" : window.location.origin;

export function CodeBlock({ code, className }: { code: string; className?: string }) {
	const [copied, setCopied] = useState(false);
	return (
		<div className={cn("relative rounded-lg border border-line bg-raised", className)}>
			<button
				type="button"
				aria-label="Copy"
				onClick={() => {
					navigator.clipboard?.writeText(code);
					setCopied(true);
					setTimeout(() => setCopied(false), 1500);
				}}
				className="absolute top-2 right-2 rounded-md p-1.5 text-muted hover:bg-surface hover:text-fg"
			>
				{copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
			</button>
			<pre className="overflow-x-auto p-3 pr-10 font-mono text-[12px] leading-relaxed whitespace-pre-wrap break-all">
				{code}
			</pre>
		</div>
	);
}

function when(iso: string) {
	return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

/** Link Telegram: a one-tap deep link, with the command as a fallback. */
export function TelegramConnect() {
	const session = useAuthSession();
	const [code, setCode] = useState<LinkCodeOut>();
	const links = useAuthListLinks({ query: { refetchInterval: code ? 2000 : false } });
	const linkTelegram = useAuthLinkTelegram();
	const unlink = useAuthUnlink();
	const qc = useQueryClient();
	const telegram = (links.data?.links ?? []).filter((l) => l.channel === "telegram");
	const bot = session.data?.telegramBot;

	if (!bot) {
		return (
			<p className="text-sm text-muted">
				Telegram is off on this server. Start Exhale with{" "}
				<code className="font-mono">EXHALE_TELEGRAM_TOKEN</code> (a bot token from @BotFather) to get
				reminders you can act on.
			</p>
		);
	}

	const start = async () => {
		try {
			const c = await linkTelegram.mutateAsync();
			setCode(c);
			if (c.url) window.open(c.url, "_blank", "noopener");
		} catch {
			toast.error("Could not create a link code");
		}
	};

	return (
		<div className="grid gap-3">
			{telegram.map((l) => (
				<div
					key={l.id}
					className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2"
				>
					<span className="flex items-center gap-2 text-sm">
						<Check className="size-4 text-ok" />
						Telegram linked <span className="text-muted">· since {when(l.createdAt)}</span>
					</span>
					<Button
						variant="ghost"
						size="sm"
						onClick={async () => {
							await unlink.mutateAsync({ data: { id: l.id } });
							qc.invalidateQueries();
							toast.success("Telegram unlinked");
						}}
					>
						Unlink
					</Button>
				</div>
			))}
			{code && telegram.length === 0 && (
				<div className="grid gap-2 rounded-lg border border-accent/40 bg-accent-soft/40 p-3 text-sm">
					<p className="flex items-center gap-2 font-medium">
						<Loader2 className="size-4 animate-spin text-accent" />
						Waiting for you in Telegram…
					</p>
					<p className="text-muted">
						Telegram should have opened @{bot}: tap <b>Start</b>. If it did not, send this to the bot:
					</p>
					<CodeBlock code={code.command} />
				</div>
			)}
			<div className="flex flex-wrap gap-2">
				<Button
					variant={telegram.length ? "secondary" : "primary"}
					onClick={start}
					disabled={linkTelegram.isPending}
				>
					<Send />
					{telegram.length ? "Link another chat" : `Link Telegram (@${bot})`}
				</Button>
				{code?.url && telegram.length === 0 && (
					<a
						href={code.url}
						target="_blank"
						rel="noopener"
						className="inline-flex h-9 items-center gap-2 rounded-lg px-3 text-sm text-muted hover:bg-raised hover:text-fg"
					>
						<ExternalLink className="size-4" />
						Open Telegram again
					</a>
				)}
			</div>
		</div>
	);
}

/** Connect Claude Code: creates a token and shows the one command to run. */
export function ClaudeConnect() {
	const create = useAuthCreateToken();
	const qc = useQueryClient();
	const [token, setToken] = useState<string>();
	const command = token
		? `claude mcp add --transport http exhale ${apiOrigin}/mcp --header "Authorization: Bearer ${token}"`
		: undefined;
	return (
		<div className="grid gap-3">
			<p className="text-sm text-muted">
				Claude gets the whole picture (subscriptions, charges, spend) and can manage everything, with
				previews, confirmations and undo. Every change shows up here and in Telegram.
			</p>
			{command ? (
				<>
					<CodeBlock code={command} />
					<p className="text-xs text-muted">
						Run it in a terminal, then ask Claude something like{" "}
						<i>"add my subscriptions to Exhale: Netflix 13.99 € monthly on the 6th…"</i>. The token is shown
						only once; you can revoke it in Settings.
					</p>
				</>
			) : (
				<div>
					<Button
						variant="primary"
						disabled={create.isPending}
						onClick={async () => {
							const t = await create.mutateAsync({ data: { name: "Claude Code", scope: "write" } });
							setToken(t.token);
							qc.invalidateQueries();
						}}
					>
						Connect Claude Code
					</Button>
				</div>
			)}
		</div>
	);
}

const scopes = {
	read: "Read only",
	write: "Read and change (asks before destructive changes)",
	trusted: "Read and change without asking",
} as const;

/** API tokens: create (shown once), list, revoke. */
export function TokensManager() {
	const tokens = useAuthListTokens();
	const create = useAuthCreateToken();
	const revoke = useAuthRevokeToken();
	const qc = useQueryClient();
	const [name, setName] = useState("");
	const [scope, setScope] = useState<keyof typeof scopes>("write");
	const [created, setCreated] = useState<string>();
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			const t = await create.mutateAsync({ data: { name: name.trim(), scope } });
			setCreated(t.token);
			setName("");
			qc.invalidateQueries();
		} catch (err) {
			setError(err instanceof ApiError ? err.detail : "Could not create the token");
		}
	};
	const list = tokens.data?.tokens ?? [];
	return (
		<div className="grid gap-4">
			{list.length > 0 && (
				<ul className="divide-y divide-line rounded-lg border border-line">
					{list.map((t) => (
						<li key={t.id} className="flex items-center justify-between gap-3 px-3 py-2.5">
							<div className="min-w-0">
								<p className="truncate text-sm font-medium">{t.name}</p>
								<p className="text-xs text-muted">
									Created {when(t.createdAt)} ·{" "}
									{t.lastUsedAt ? `last used ${when(t.lastUsedAt)}` : "never used"}
								</p>
							</div>
							<div className="flex items-center gap-2">
								<Badge>{t.scope}</Badge>
								<Button
									variant="ghost"
									size="icon"
									aria-label={`Revoke ${t.name}`}
									onClick={async () => {
										await revoke.mutateAsync({ data: { id: t.id } });
										qc.invalidateQueries();
										toast.success(`${t.name} revoked`);
									}}
								>
									<Trash2 />
								</Button>
							</div>
						</li>
					))}
				</ul>
			)}
			{created && (
				<div className="grid gap-2">
					<p className="text-sm font-medium">Copy it now: it will not be shown again.</p>
					<CodeBlock code={created} />
				</div>
			)}
			<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
				<Field label="Name">
					<Input
						value={name}
						onChange={(e) => setName(e.target.value)}
						placeholder="Claude Desktop"
						required
						maxLength={60}
					/>
				</Field>
				<Field label="Access">
					<Select value={scope} onChange={(e) => setScope(e.target.value as keyof typeof scopes)}>
						{Object.entries(scopes).map(([k, label]) => (
							<option key={k} value={k}>
								{label}
							</option>
						))}
					</Select>
				</Field>
				<Button type="submit" disabled={create.isPending}>
					Create token
				</Button>
			</form>
			{error && <p className="text-xs text-bad">{error}</p>}
		</div>
	);
}
