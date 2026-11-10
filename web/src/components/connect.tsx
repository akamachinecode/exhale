import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, ExternalLink, Loader2, Trash2, UserPlus } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { encode } from "uqr";
import { MemberRow } from "@/components/manage";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/field";
import {
	type InviteOut,
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
import { languages, t } from "@/lib/i18n";
import { currencies, timezones } from "@/lib/locale";
import { cn } from "@/lib/utils";

// Everything the home setup and Settings share: the Telegram bot and chat
// link, reminder time, preferences, sharing, Claude and API tokens. Copy is
// kept short on purpose; the details live in the docs.

/** Where AI clients on this machine reach the API (the Go server, not Vite). */
export const apiOrigin = import.meta.env.DEV ? "http://127.0.0.1:8484" : window.location.origin;

/** True when this page is opened through a local address nobody else can reach. */
const localOrigin = /^(localhost|127\.|\[::1\])/.test(window.location.hostname);

function errText(e: unknown, fallback: string) {
	return e instanceof ApiError ? e.detail : fallback;
}

async function copy(text: string) {
	try {
		await navigator.clipboard.writeText(text);
		return true;
	} catch {
		return false;
	}
}

export function CodeBlock({ code, className }: { code: string; className?: string }) {
	const [copied, setCopied] = useState(false);
	return (
		<div className={cn("relative rounded-lg border border-line bg-raised", className)}>
			<button
				type="button"
				aria-label={t("Copy")}
				onClick={async () => {
					if (await copy(code)) {
						setCopied(true);
						setTimeout(() => setCopied(false), 1500);
					}
				}}
				className="absolute top-2 right-2 rounded-md p-1.5 text-muted hover:bg-surface hover:text-fg"
			>
				{copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
			</button>
			<pre className="overflow-x-auto p-3 pr-10 font-mono text-[12px] leading-relaxed break-all whitespace-pre-wrap select-all">
				{code}
			</pre>
		</div>
	);
}

/** A chip that copies its text when clicked. */
function CopyChip({ text, label }: { text: string; label?: string }) {
	const [copied, setCopied] = useState(false);
	return (
		<button
			type="button"
			onClick={async () => {
				if (await copy(text)) {
					setCopied(true);
					setTimeout(() => setCopied(false), 1200);
				}
			}}
			className="inline-flex max-w-full items-center gap-2 rounded-lg border border-line bg-raised px-2.5 py-1.5 font-mono text-[12.5px] transition-colors hover:border-fg/30"
			aria-label={t("Copy {0}", label ?? text)}
		>
			<span className="truncate">{text}</span>
			{copied ? (
				<Check className="size-3.5 shrink-0 text-ok" />
			) : (
				<Copy className="size-3.5 shrink-0 text-faint" />
			)}
		</button>
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
		<div className="inline-block shrink-0 rounded-xl bg-white p-3 shadow-sm">
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

/**
 * Telegram, start to finish: create your own bot with BotFather (each
 * Exhale has its own), paste the reply, then link your chat with a QR code.
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
			<p className="text-sm text-muted">{t("The owner hasn't set up the Telegram bot yet.")}</p>
		);
	}
	if (!s.running) {
		return (
			<p className="flex items-center gap-2 text-sm text-muted">
				<Loader2 className="size-4 animate-spin" /> {t("Starting your bot…")}
			</p>
		);
	}
	return (
		<div className="grid gap-5">
			<div className="flex flex-wrap items-center justify-between gap-3">
				<p className="text-sm">
					{t("Your bot:")}{" "}
					<a
						href={`https://t.me/${s.username}`}
						target="_blank"
						rel="noopener"
						className="font-medium underline-offset-4 hover:underline"
					>
						@{s.username}
					</a>
					{s.source === "env" && <span className="text-muted"> {t("· set on the server")}</span>}
				</p>
				{isOwner && s.source !== "env" && <ChangeBot />}
			</div>
			{linked.length === 0 ? <LinkChat username={s.username ?? ""} /> : <LinkedChats />}
			<ReminderTime />
		</div>
	);
}

/** Telegram usernames: 5–32 of a–z, 0–9 and _, ending in "bot". */
function suggestUsername(name: string) {
	const base =
		name
			.normalize("NFD")
			.replace(/[̀-ͯ]/g, "")
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, "_")
			.replace(/^_+|_+$/g, "")
			.slice(0, 18) || "my";
	return `${base}_exhale_bot`;
}

function usernameProblem(u: string) {
	if (!/^[a-zA-Z][a-zA-Z0-9_]{4,31}$/.test(u))
		return t("5 to 32 letters, numbers or _, starting with a letter.");
	if (!/bot$/i.test(u)) return t("It must end in “bot”.");
	return undefined;
}

/** Pulls the token out of whatever was pasted (BotFather's whole reply works). */
function findToken(text: string) {
	return text.match(/\d{6,12}:[A-Za-z0-9_-]{30,}/)?.[0];
}

function BotFatherGuide() {
	const qc = useQueryClient();
	const session = useAuthSession();
	const set = useSettingsSetTelegram();
	const first = session.data?.user?.name?.split(" ")[0] ?? "My";
	const [name, setName] = useState(`${first}'s Exhale`);
	const [username, setUsername] = useState(suggestUsername(first));
	const [pasted, setPasted] = useState("");
	const [error, setError] = useState<string>();
	const problem = usernameProblem(username);

	const connect = async (text: string) => {
		const token = findToken(text);
		if (!token) {
			setError(t("No token found. Paste BotFather's last message, the one that starts with “Done!”."));
			return;
		}
		setError(undefined);
		try {
			const s = await set.mutateAsync({ data: { token } });
			await qc.invalidateQueries();
			toast.success(t("@{0} is ready", s.username));
		} catch (err) {
			setError(errText(err, t("Could not connect the bot")));
		}
	};

	return (
		<div className="grid gap-5">
			<p className="text-sm text-muted">
				{t("Telegram makes you create the bot yourself. We prepared everything.")}
			</p>
			<div className="grid gap-3 sm:grid-cols-2">
				<Field label={t("Bot name")}>
					<Input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} />
				</Field>
				<Field label={t("Username")} error={problem}>
					<Input
						value={username}
						onChange={(e) => setUsername(e.target.value.replace(/^@/, ""))}
						spellCheck={false}
						className="font-mono"
					/>
				</Field>
			</div>
			<div className="grid items-start gap-5 sm:grid-cols-[1fr_auto]">
				<ol className="grid gap-3 text-sm">
					<li>
						<span className="text-muted">1.</span>{" "}
						<a
							href="https://t.me/BotFather"
							target="_blank"
							rel="noopener"
							className="inline-flex items-center gap-1 font-medium underline underline-offset-4"
						>
							{t("Open @BotFather")} <ExternalLink className="size-3.5" />
						</a>{" "}
						<span className="text-muted sm:hidden">{t("in Telegram")}</span>
						<span className="hidden text-muted sm:inline">{t("or scan the code with your phone")}</span>
					</li>
					<li className="grid gap-2">
						<span>
							<span className="text-muted">2.</span>{" "}
							{t("Send these three messages, one by one (tap to copy):")}
						</span>
						<span className="flex flex-wrap gap-2">
							<CopyChip text="/newbot" />
							<CopyChip text={name} label={t("bot name")} />
							<CopyChip text={username} label={t("username")} />
						</span>
					</li>
					<li>
						<span className="text-muted">3.</span> {t("Paste BotFather's reply here:")}
					</li>
				</ol>
				<div className="hidden sm:block">
					<QrCode value="https://t.me/BotFather" size={104} label={t("QR code that opens BotFather")} />
				</div>
			</div>
			<form
				onSubmit={(e: FormEvent) => {
					e.preventDefault();
					connect(pasted);
				}}
				className="grid gap-2 sm:grid-cols-[1fr_auto]"
			>
				<Input
					value={pasted}
					onChange={(e) => setPasted(e.target.value)}
					onPaste={(e) => {
						const text = e.clipboardData.getData("text");
						if (findToken(text)) {
							e.preventDefault();
							setPasted(`${"•".repeat(12)} token found`);
							connect(text);
						}
					}}
					placeholder={t("Done! Congratulations on your new bot…")}
					aria-label={t("BotFather's reply")}
					autoComplete="off"
					spellCheck={false}
				/>
				<Button type="submit" variant="primary" disabled={set.isPending || pasted.trim() === ""}>
					{set.isPending && <Loader2 className="animate-spin" />}
					{t("Connect")}
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
				{t("Use another bot")}
			</Button>
		);
	}
	return (
		<span className="flex items-center gap-2 text-xs text-muted">
			{t("Disconnect this bot?")}
			<Button
				variant="danger"
				size="sm"
				onClick={async () => {
					await remove.mutateAsync();
					await qc.invalidateQueries();
					setConfirm(false);
				}}
			>
				{t("Disconnect")}
			</Button>
			<Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>
				{t("Keep")}
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
				<QrCode value={url} label={t("QR code that opens @{0} with your link code", username)} />
			) : (
				<div className="grid size-[192px] place-items-center rounded-xl bg-raised">
					<Loader2 className="size-5 animate-spin text-muted" />
				</div>
			)}
			<div className="grid gap-2 text-sm">
				<p className="font-medium">{t("Scan it with your phone, then tap Start")}</p>
				<p className="flex items-center gap-2 text-xs text-muted">
					<Loader2 className="size-3.5 animate-spin" /> {t("Waiting for Telegram…")}
				</p>
				{code?.url && (
					<a
						href={code.url}
						target="_blank"
						rel="noopener"
						className="inline-flex items-center gap-1.5 text-xs text-muted hover:text-fg"
					>
						<ExternalLink className="size-3.5" /> {t("Telegram is on this computer")}
					</a>
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
							{t("Your chat is linked")}{" "}
							<span className="text-muted">
								{t("· since")} {when(l.createdAt)}
							</span>
						</span>
						<Button
							variant="ghost"
							size="sm"
							onClick={async () => {
								await unlink.mutateAsync({ data: { id: l.id } });
								qc.invalidateQueries();
								toast.success(t("Telegram unlinked"));
							}}
						>
							{t("Unlink")}
						</Button>
					</div>
				))}
		</div>
	);
}

const pad = (n: number) => String(n).padStart(2, "0");

function useSavePreferences() {
	const qc = useQueryClient();
	const update = useUpdateHousehold();
	return async (data: Parameters<typeof update.mutateAsync>[0]["data"], ok: string) => {
		try {
			await update.mutateAsync({ data });
			await qc.invalidateQueries();
			toast.success(ok);
		} catch (err) {
			toast.error(errText(err, t("Could not save")));
		}
	};
}

function TimezoneSelect({
	value,
	disabled,
	className,
}: {
	value: string;
	disabled?: boolean;
	className?: string;
}) {
	const save = useSavePreferences();
	return (
		<Select
			value={value}
			disabled={disabled}
			aria-label={t("Time zone")}
			className={className}
			onChange={(e) =>
				save({ timezone: e.target.value }, `Time zone: ${e.target.value.replaceAll("_", " ")}`)
			}
		>
			{[...new Set([value, ...timezones()])].map((z) => (
				<option key={z} value={z}>
					{z.replaceAll("_", " ")}
				</option>
			))}
		</Select>
	);
}

/** When reminders arrive: a time of day, optionally with the time zone (owners can change both). */
export function ReminderTime({ withTimezone = true }: { withTimezone?: boolean }) {
	const household = useGetHousehold();
	const session = useAuthSession();
	const save = useSavePreferences();
	const h = household.data?.household;
	if (!h) return null;
	const value = `${pad(h.reminderHour)}:${pad(h.reminderMinute)}`;
	const editable = session.data?.user?.role === "owner";
	return (
		<div className="flex flex-wrap items-center gap-2 text-sm">
			<span aria-hidden>{t("Reminders at")}</span>
			<Input
				type="time"
				aria-label={t("Reminder time")}
				className="w-36"
				defaultValue={value}
				key={value}
				disabled={!editable}
				onBlur={(e) => {
					if (e.target.value && e.target.value !== value)
						save({ reminderTime: e.target.value }, t("Reminders at {0}", e.target.value));
				}}
			/>
			{withTimezone && (
				<>
					<span aria-hidden className="text-muted">
						{t("in")}
					</span>
					<TimezoneSelect value={h.timezone} disabled={!editable} className="w-auto min-w-48" />
				</>
			)}
		</div>
	);
}

/** Display currency, time zone and language. */
export function PreferencesForm() {
	const household = useGetHousehold();
	const session = useAuthSession();
	const save = useSavePreferences();
	const h = household.data?.household;
	if (!h) return null;
	const editable = session.data?.user?.role === "owner";
	return (
		<div className="grid gap-4 sm:grid-cols-3">
			<Field label={t("Display currency")} hint={t("Totals convert into it.")}>
				<Select
					value={h.currency}
					disabled={!editable}
					onChange={(e) => save({ currency: e.target.value }, t("Showing totals in {0}", e.target.value))}
				>
					{[...new Set([h.currency, ...currencies])].map((c) => (
						<option key={c}>{c}</option>
					))}
				</Select>
			</Field>
			<Field label={t("Time zone")}>
				<TimezoneSelect value={h.timezone} disabled={!editable} />
			</Field>
			<Field label={t("Reminder language")}>
				<Select
					value={h.locale}
					disabled={!editable}
					onChange={(e) => save({ locale: e.target.value }, t("Language saved"))}
				>
					{languages.map((l) => (
						<option key={l.code} value={l.code} lang={l.code}>
							{l.name}
						</option>
					))}
				</Select>
			</Field>
		</div>
	);
}

/** People who share this Exhale, and invites (owners). Invites go through Telegram, which works from anywhere. */
export function Sharing() {
	const household = useGetHousehold();
	const session = useAuthSession();
	const invite = useAuthCreateInvite();
	const bot = useSettingsTelegram();
	const [inv, setInv] = useState<InviteOut>();
	const members = household.data?.members ?? [];
	const isOwner = session.data?.user?.role === "owner";
	const canInvite = bot.data?.running || !localOrigin;
	return (
		<div className="grid gap-4">
			{members.length > 1 && (
				<ul className="divide-y divide-line rounded-lg border border-line">
					{members.map((m) => (
						<MemberRow
							key={m.id}
							member={m}
							me={session.data?.user?.id}
							isOwner={isOwner}
							owners={members.filter((x) => x.role === "owner").length}
						/>
					))}
				</ul>
			)}
			{isOwner && !inv && !canInvite && (
				<p className="text-sm text-muted">
					{t("Set up Telegram first: invites go through it, so they work from anywhere.")}
				</p>
			)}
			{isOwner && !inv && canInvite && (
				<div className="flex flex-wrap items-center gap-3">
					<Button
						disabled={invite.isPending}
						onClick={async () => setInv(await invite.mutateAsync({ data: { role: "member" } }))}
					>
						<UserPlus />
						{t("Invite someone")}
					</Button>
					<span className="text-sm text-muted">
						{t("A partner, family or flatmates. Private subscriptions stay private.")}
					</span>
				</div>
			)}
			{inv &&
				(inv.telegramUrl ? (
					<div className="grid items-center gap-5 sm:grid-cols-[auto_1fr]">
						<QrCode value={inv.telegramUrl} size={144} label={t("QR code with the invite")} />
						<div className="grid gap-2 text-sm">
							<p className="font-medium">{t("Send them this link, or let them scan the code")}</p>
							<p className="text-muted">
								{t("It opens in Telegram and they're in. Works once, for 7 days.")}
							</p>
							<CopyChip text={inv.telegramUrl} label={t("invite link")} />
							{!localOrigin && (
								<p className="text-xs text-muted">
									{t("Or on the web:")}{" "}
									<CopyChip text={window.location.origin + inv.path} label={t("web invite link")} />
								</p>
							)}
						</div>
					</div>
				) : localOrigin ? (
					<p className="text-sm text-muted">
						{t("Set up Telegram first: invites are sent through it, so they work from anywhere.")}
					</p>
				) : (
					<div className="grid gap-2">
						<p className="text-sm">{t("Send them this link. It works once, for 7 days.")}</p>
						<CodeBlock code={window.location.origin + inv.path} />
					</div>
				))}
		</div>
	);
}

/** Claude Code: one button copies the command; the step completes when Claude first connects. */
export function ClaudeConnect() {
	const create = useAuthCreateToken();
	const qc = useQueryClient();
	const [command, setCommand] = useState<string>();
	const [copied, setCopied] = useState(false);
	const [shown, setShown] = useState(false);
	const tokens = useAuthListTokens({ query: { refetchInterval: command ? 2000 : false } });
	const connected = (tokens.data?.tokens ?? []).some((t) => t.lastUsedAt);
	if (connected && !command) {
		return (
			<p className="flex items-center gap-2 text-sm">
				<Check className="size-4 text-ok" />{" "}
				{t("Claude is connected. Ask it anything about your subscriptions.")}
			</p>
		);
	}
	if (connected) {
		return (
			<p className="flex items-center gap-2 text-sm">
				<Check className="size-4 text-ok" /> {t("Claude is connected. Try:")}{" "}
				<i>{t("“add Netflix, 13.99 € on the 6th”")}</i>
			</p>
		);
	}
	// One token per visit: copying again or showing reuses the same command.
	const ensureCommand = async () => {
		if (command) return command;
		const made = await create.mutateAsync({ data: { name: "Claude Code", scope: "write" } });
		const cmd = `claude mcp add --scope user --transport http exhale ${apiOrigin}/mcp --header "Authorization: Bearer ${made.token}"`;
		setCommand(cmd);
		qc.invalidateQueries();
		return cmd;
	};
	return (
		<div className="grid gap-3">
			<div className="flex flex-wrap items-center gap-2">
				<Button
					variant="primary"
					disabled={create.isPending}
					onClick={async () => setCopied(await copy(await ensureCommand()))}
				>
					{copied ? t("Copy again") : t("Copy the command")}
				</Button>
				<Button
					variant="ghost"
					disabled={create.isPending}
					onClick={async () => {
						await ensureCommand();
						setShown(!shown);
					}}
				>
					{shown ? t("Hide it") : t("Show it")}
				</Button>
				{copied && (
					<span className="flex items-center gap-2 text-sm text-muted">
						<Check className="size-4 text-ok" /> {t("Copied. Paste it in a terminal.")}
					</span>
				)}
			</div>
			{command && shown && <CodeBlock code={command} />}
			{command && (
				<p className="flex items-center gap-2 text-xs text-muted">
					<Loader2 className="size-3.5 animate-spin" /> {t("Waiting for Claude…")}
				</p>
			)}
		</div>
	);
}

const scopes = {
	read: t("Read only"),
	write: t("Read and change (asks before destructive changes)"),
	trusted: t("Read and change without asking"),
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
			const made = await create.mutateAsync({ data: { name: name.trim(), scope } });
			setCreated(made.token);
			setName("");
			qc.invalidateQueries();
		} catch (err) {
			setError(errText(err, t("Could not create the token")));
		}
	};
	const list = tokens.data?.tokens ?? [];
	return (
		<div className="grid gap-4">
			{list.length > 0 && (
				<ul className="divide-y divide-line rounded-lg border border-line">
					{list.map((tok) => (
						<li key={tok.id} className="flex items-center justify-between gap-3 px-3 py-2.5">
							<div className="min-w-0">
								<p className="truncate text-sm font-medium">{tok.name}</p>
								<p className="text-xs text-muted">
									{t("Created")} {when(tok.createdAt)} ·{" "}
									{tok.lastUsedAt ? t("last used {0}", when(tok.lastUsedAt)) : t("never used")}
								</p>
							</div>
							<div className="flex items-center gap-2">
								<Badge>{tok.scope}</Badge>
								<Button
									variant="ghost"
									size="icon"
									aria-label={t("Revoke {0}", tok.name)}
									onClick={async () => {
										await revoke.mutateAsync({ data: { id: tok.id } });
										qc.invalidateQueries();
										toast.success(`${tok.name} revoked`);
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
					<p className="text-sm font-medium">{t("Copy it now: it will not be shown again.")}</p>
					<CodeBlock code={created} />
				</div>
			)}
			<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
				<Field label={t("Name")}>
					<Input
						value={name}
						onChange={(e) => setName(e.target.value)}
						placeholder="Claude Desktop"
						required
						maxLength={60}
					/>
				</Field>
				<Field label={t("Access")}>
					<Select value={scope} onChange={(e) => setScope(e.target.value as keyof typeof scopes)}>
						{Object.entries(scopes).map(([k, label]) => (
							<option key={k} value={k}>
								{label}
							</option>
						))}
					</Select>
				</Field>
				<Button type="submit" disabled={create.isPending}>
					{t("Create token")}
				</Button>
			</form>
			{error && <p className="text-xs text-bad">{error}</p>}
		</div>
	);
}
