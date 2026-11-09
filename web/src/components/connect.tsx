import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, ExternalLink, Loader2, Trash2, UserPlus } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { encode } from "uqr";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, PasswordInput, Select } from "@/components/ui/field";
import {
	type LinkCodeOut,
	useAuthCreateInvite,
	useAuthCreateToken,
	useAuthLinkTelegram,
	useAuthListLinks,
	useAuthListTokens,
	useAuthRevokeToken,
	useAuthSession,
	useAuthUnlink,
	useGetHousehold,
	useSettingsRemoveTelegram,
	useSettingsSetTelegram,
	useSettingsTelegram,
	useUpdateHousehold,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { currencies, timezones } from "@/lib/locale";
import { cn } from "@/lib/utils";

// Everything the home onboarding and Settings share: the Telegram bot and
// chat link, reminder time, preferences, sharing, Claude and API tokens.

/** Where AI clients on this machine reach the API (the Go server, not Vite). */
export const apiOrigin = import.meta.env.DEV ? "http://127.0.0.1:8484" : window.location.origin;

function errText(e: unknown, fallback: string) {
	return e instanceof ApiError ? e.detail : fallback;
}

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
			<pre className="overflow-x-auto p-3 pr-10 font-mono text-[12px] leading-relaxed break-all whitespace-pre-wrap">
				{code}
			</pre>
		</div>
	);
}

/** A QR code drawn as SVG: dark modules on white, so phones can read it. */
export function QrCode({ value, size = 168, label }: { value: string; size?: number; label: string }) {
	const qr = useMemo(() => encode(value, { border: 0, ecc: "M" }), [value]);
	const n = qr.size;
	let d = "";
	qr.data.forEach((row, y) => {
		row.forEach((on, x) => {
			if (on) d += `M${x} ${y}h1v1h-1z`;
		});
	});
	return (
		<div className="inline-block rounded-xl bg-white p-3 shadow-sm">
			<svg
				viewBox={`0 0 ${n} ${n}`}
				width={size}
				height={size}
				role="img"
				aria-label={label}
				shapeRendering="crispEdges"
			>
				<path d={d} fill="#0a0a0a" />
			</svg>
		</div>
	);
}

function when(iso: string) {
	return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

function Steps({ children }: { children: React.ReactNode }) {
	return <ol className="grid gap-4 [counter-reset:step]">{children}</ol>;
}

function StepItem({ children }: { children: React.ReactNode }) {
	return (
		<li className="grid grid-cols-[1.5rem_1fr] gap-3 [counter-increment:step] before:grid before:size-6 before:place-items-center before:rounded-full before:border before:border-line before:text-xs before:font-medium before:text-muted before:content-[counter(step)]">
			<div className="min-w-0 pt-0.5 text-sm">{children}</div>
		</li>
	);
}

/**
 * Telegram, start to finish: create your own bot with BotFather (each
 * Exhale has its own), paste its token, then link your chat by scanning a
 * QR code with your phone.
 */
export function TelegramSetup() {
	const session = useAuthSession();
	const bot = useSettingsTelegram({
		query: { refetchInterval: (q) => (q.state.data?.running ? false : 4000) },
	});
	const links = useAuthListLinks();
	const isOwner = session.data?.user?.role === "owner";
	const linked = (links.data?.links ?? []).filter((l) => l.channel === "telegram");
	if (bot.isPending) return <Loader2 className="size-4 animate-spin text-muted" />;
	const s = bot.data;
	if (!s?.configured) {
		return isOwner ? (
			<BotFatherGuide />
		) : (
			<p className="text-sm text-muted">The owner of this Exhale has not set up the Telegram bot yet.</p>
		);
	}
	if (!s.running) {
		return (
			<p className="flex items-center gap-2 text-sm text-muted">
				<Loader2 className="size-4 animate-spin" /> Starting your bot…
			</p>
		);
	}
	return (
		<div className="grid gap-5">
			<div className="flex flex-wrap items-center justify-between gap-3">
				<p className="text-sm">
					Your bot:{" "}
					<a
						href={`https://t.me/${s.username}`}
						target="_blank"
						rel="noopener"
						className="font-medium underline-offset-4 hover:underline"
					>
						@{s.username}
					</a>
					{s.source === "env" && <span className="text-muted"> · set on the server</span>}
				</p>
				{isOwner && s.source !== "env" && <ChangeBot />}
			</div>
			{linked.length === 0 ? <LinkChat username={s.username ?? ""} /> : <LinkedChats />}
			<ReminderTime />
		</div>
	);
}

function BotFatherGuide() {
	const qc = useQueryClient();
	const set = useSettingsSetTelegram();
	const [token, setToken] = useState("");
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			const s = await set.mutateAsync({ data: { token } });
			await qc.invalidateQueries();
			toast.success(`@${s.username} is ready`);
		} catch (err) {
			setError(errText(err, "Could not connect the bot"));
		}
	};
	return (
		<div className="grid gap-5">
			<p className="text-sm text-muted">
				Exhale talks to you through your own Telegram bot: nobody else runs it, and nothing goes through
				anyone's servers. Creating it takes a minute.
			</p>
			<div className="grid gap-5 sm:grid-cols-[1fr_auto]">
				<Steps>
					<StepItem>
						Open{" "}
						<a
							href="https://t.me/BotFather"
							target="_blank"
							rel="noopener"
							className="font-medium underline underline-offset-4"
						>
							@BotFather
						</a>{" "}
						in Telegram (or scan the code with your phone).
					</StepItem>
					<StepItem>
						Send <code className="rounded bg-raised px-1 font-mono text-[12px]">/newbot</code>. Give it a
						name, like <i>Exhale</i>, and a username that ends in <i>bot</i>, like <i>alex_exhale_bot</i>.
					</StepItem>
					<StepItem>BotFather answers with a token. Paste it here:</StepItem>
				</Steps>
				<div className="hidden sm:block">
					<QrCode value="https://t.me/BotFather" size={112} label="QR code that opens BotFather" />
				</div>
			</div>
			<form onSubmit={onSubmit} className="grid gap-2 sm:grid-cols-[1fr_auto]">
				<PasswordInput
					value={token}
					onChange={(e) => setToken(e.target.value)}
					placeholder="123456789:AAH…"
					aria-label="Bot token"
					autoComplete="off"
					spellCheck={false}
					required
					className="font-mono"
				/>
				<Button type="submit" variant="primary" disabled={set.isPending || token.trim() === ""}>
					{set.isPending && <Loader2 className="animate-spin" />}
					Connect bot
				</Button>
			</form>
			{error && <p className="text-xs text-bad">{error}</p>}
		</div>
	);
}

function ChangeBot() {
	const qc = useQueryClient();
	const remove = useSettingsRemoveTelegram();
	const [confirm, setConfirm] = useState(false);
	if (!confirm) {
		return (
			<Button variant="ghost" size="sm" onClick={() => setConfirm(true)}>
				Use another bot
			</Button>
		);
	}
	return (
		<span className="flex items-center gap-2 text-xs text-muted">
			Disconnect this bot?
			<Button
				variant="danger"
				size="sm"
				onClick={async () => {
					await remove.mutateAsync();
					await qc.invalidateQueries();
					setConfirm(false);
				}}
			>
				Disconnect
			</Button>
			<Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>
				Keep
			</Button>
		</span>
	);
}

/** Link this person's chat: a fresh one-time code as a QR, a link and a command. */
function LinkChat({ username }: { username: string }) {
	const linkTelegram = useAuthLinkTelegram();
	const [code, setCode] = useState<LinkCodeOut>();
	useAuthListLinks({ query: { refetchInterval: 2000 } }); // notices the link as soon as it happens
	const { mutateAsync } = linkTelegram;
	useEffect(() => {
		let alive = true;
		const fresh = () =>
			mutateAsync().then((c) => {
				if (alive) setCode(c);
			});
		fresh();
		const timer = setInterval(fresh, 9 * 60 * 1000); // codes last 10 minutes
		return () => {
			alive = false;
			clearInterval(timer);
		};
	}, [mutateAsync]);
	const url = code?.url ?? `https://t.me/${username}`;
	return (
		<div className="grid items-center gap-5 sm:grid-cols-[auto_1fr]">
			{code ? (
				<QrCode value={url} label={`QR code that opens @${username} with your link code`} />
			) : (
				<div className="grid size-[192px] place-items-center rounded-xl bg-raised">
					<Loader2 className="size-5 animate-spin text-muted" />
				</div>
			)}
			<div className="grid gap-3 text-sm">
				<p className="font-medium">Scan it with your phone's camera</p>
				<p className="text-muted">
					It opens @{username} in Telegram. Tap <b className="text-fg">Start</b> and this page notices on its
					own.
				</p>
				<p className="flex items-center gap-2 text-xs text-muted">
					<Loader2 className="size-3.5 animate-spin" /> Waiting for Telegram…
				</p>
				{code && (
					<details className="text-xs text-muted">
						<summary className="cursor-pointer select-none hover:text-fg">
							Telegram is on this computer?
						</summary>
						<div className="mt-2 grid gap-2">
							{code.url && (
								<a
									href={code.url}
									target="_blank"
									rel="noopener"
									className="inline-flex items-center gap-1.5 font-medium text-fg hover:underline"
								>
									<ExternalLink className="size-3.5" /> Open @{username}
								</a>
							)}
							<span>Or send it this message (valid 10 minutes, only once):</span>
							<CodeBlock code={code.command} />
						</div>
					</details>
				)}
			</div>
		</div>
	);
}

function LinkedChats() {
	const qc = useQueryClient();
	const links = useAuthListLinks();
	const unlink = useAuthUnlink();
	return (
		<div className="grid gap-2">
			{(links.data?.links ?? [])
				.filter((l) => l.channel === "telegram")
				.map((l) => (
					<div
						key={l.id}
						className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2"
					>
						<span className="flex items-center gap-2 text-sm">
							<Check className="size-4 text-ok" />
							Your chat is linked <span className="text-muted">· since {when(l.createdAt)}</span>
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
		</div>
	);
}

const pad = (n: number) => String(n).padStart(2, "0");

/** Default time of day for reminders (owners). */
export function ReminderTime() {
	const qc = useQueryClient();
	const household = useGetHousehold();
	const session = useAuthSession();
	const update = useUpdateHousehold();
	const h = household.data?.household;
	if (!h) return null;
	const value = `${pad(h.reminderHour)}:${pad(h.reminderMinute)}`;
	const editable = session.data?.user?.role === "owner";
	return (
		<div className="flex flex-wrap items-center gap-3 text-sm">
			<span aria-hidden>Remind me at</span>
			<Input
				type="time"
				aria-label="Reminder time"
				className="w-36"
				defaultValue={value}
				key={value}
				disabled={!editable}
				onBlur={async (e) => {
					if (!e.target.value || e.target.value === value) return;
					try {
						await update.mutateAsync({ data: { reminderTime: e.target.value } });
						await qc.invalidateQueries();
						toast.success(`Reminders at ${e.target.value}`);
					} catch (err) {
						toast.error(errText(err, "Could not change the time"));
					}
				}}
			/>
			<span className="text-muted">in {h.timezone.replaceAll("_", " ")}</span>
		</div>
	);
}

/** Display currency, time zone and language. */
export function PreferencesForm() {
	const qc = useQueryClient();
	const household = useGetHousehold();
	const session = useAuthSession();
	const update = useUpdateHousehold();
	const h = household.data?.household;
	if (!h) return null;
	const editable = session.data?.user?.role === "owner";
	const save = async (data: Parameters<typeof update.mutateAsync>[0]["data"], ok: string) => {
		try {
			await update.mutateAsync({ data });
			await qc.invalidateQueries();
			toast.success(ok);
		} catch (err) {
			toast.error(errText(err, "Could not save"));
		}
	};
	return (
		<div className="grid gap-4 sm:grid-cols-3">
			<Field
				label="Display currency"
				hint="Totals and charts convert into it. Each subscription keeps its own currency."
			>
				<Select
					value={h.currency}
					disabled={!editable}
					onChange={(e) => save({ currency: e.target.value }, `Showing totals in ${e.target.value}`)}
				>
					{[...new Set([h.currency, ...currencies])].map((c) => (
						<option key={c}>{c}</option>
					))}
				</Select>
			</Field>
			<Field label="Time zone" hint="For due dates and reminder times.">
				<Select
					value={h.timezone}
					disabled={!editable}
					onChange={(e) => save({ timezone: e.target.value }, "Time zone saved")}
				>
					{[...new Set([h.timezone, ...timezones()])].map((z) => (
						<option key={z} value={z}>
							{z.replaceAll("_", " ")}
						</option>
					))}
				</Select>
			</Field>
			<Field label="Reminder language">
				<Select
					value={h.locale}
					disabled={!editable}
					onChange={(e) => save({ locale: e.target.value }, "Language saved")}
				>
					<option value="en">English</option>
					<option value="es">Español</option>
				</Select>
			</Field>
		</div>
	);
}

/** People who share this Exhale, and invite links (owners). */
export function Sharing() {
	const household = useGetHousehold();
	const session = useAuthSession();
	const invite = useAuthCreateInvite();
	const [link, setLink] = useState<string>();
	const members = household.data?.members ?? [];
	const isOwner = session.data?.user?.role === "owner";
	return (
		<div className="grid gap-4">
			<p className="text-sm text-muted">
				Share Exhale with a partner, family or flatmates. Each person gets their own login and reminders;
				shared subscriptions and spend are seen together, private ones stay private.
			</p>
			<ul className="divide-y divide-line rounded-lg border border-line">
				{members.map((m) => (
					<li key={m.id} className="flex items-center justify-between gap-3 px-3 py-2.5">
						<div className="min-w-0">
							<p className="truncate text-sm font-medium">
								{m.name}
								{m.id === session.data?.user?.id && <span className="font-normal text-muted"> (you)</span>}
							</p>
							{m.email && <p className="truncate text-xs text-muted">{m.email}</p>}
						</div>
						<Badge>{m.role}</Badge>
					</li>
				))}
			</ul>
			{isOwner && !link && (
				<div>
					<Button
						disabled={invite.isPending}
						onClick={async () => {
							const inv = await invite.mutateAsync({ data: { role: "member" } });
							setLink(window.location.origin + inv.path);
						}}
					>
						<UserPlus />
						Invite someone
					</Button>
				</div>
			)}
			{link && (
				<div className="grid gap-2">
					<p className="text-sm">Send them this link. It works once, for 7 days.</p>
					<CodeBlock code={link} />
				</div>
			)}
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
				Claude sees your subscriptions, charges and spend, and can manage them with previews, confirmations
				and undo. Paste your list into Claude and it adds everything for you.
			</p>
			{command ? (
				<>
					<CodeBlock code={command} />
					<p className="text-xs text-muted">
						Run it in a terminal on this computer, then ask Claude something like{" "}
						<i>"add my subscriptions to Exhale: Netflix 13.99 € monthly on the 6th…"</i>. The token is shown
						only once; revoke it in Settings.
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
			setError(errText(err, "Could not create the token"));
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
