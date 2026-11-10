import { useQueryClient } from "@tanstack/react-query";
import { Check, ExternalLink, Loader2, UserPlus } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { toast } from "sonner";
import { CodeBlock } from "@/components/connect";
import { Button } from "@/components/ui/button";
import { Field, Input, PasswordInput } from "@/components/ui/field";
import {
	type InviteOut,
	type SlackLinkCode,
	type SlackStatus,
	useAuthCreateInvite,
	useAuthLinkSlack,
	useAuthListLinks,
	useAuthSession,
	useAuthUnlink,
	useSettingsRemoveSlack,
	useSettingsSetSlack,
	useSettingsSlack,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";

// Slack for a team: create your own Slack app from the manifest, paste its
// two tokens, link your account, and optionally post renewals to a channel.
// The steps are short on purpose; docs/guides/slack.md has the details.

function errText(e: unknown, fallback: string) {
	return e instanceof ApiError ? e.detail : fallback;
}

function when(iso: string) {
	return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

const link = "inline-flex items-center gap-1 font-medium underline underline-offset-4";

export function SlackSetup() {
	const session = useAuthSession();
	const slack = useSettingsSlack({
		query: { refetchInterval: (q) => (q.state.data?.configured && !q.state.data.running ? 3000 : false) },
	});
	const links = useAuthListLinks();
	const isOwner = session.data?.user?.role === "owner";
	const linked = (links.data?.links ?? []).filter((l) => l.channel === "slack");
	if (slack.isPending) return <Loader2 className="size-4 animate-spin text-muted" />;
	const s = slack.data;
	if (!s?.configured) {
		return isOwner && s ? (
			<CreateApp status={s} />
		) : (
			<p className="text-sm text-muted">{t("The owner hasn't connected Slack yet.")}</p>
		);
	}
	const editable = isOwner && s.source !== "env";
	if (!s.running) {
		return (
			<div className="flex flex-wrap items-center justify-between gap-3">
				{s.error ? (
					<p className="text-sm text-bad">
						{t("Slack is not connected:")} {s.error}
					</p>
				) : (
					<p className="flex items-center gap-2 text-sm text-muted">
						<Loader2 className="size-4 animate-spin" /> {t("Connecting to Slack…")}
					</p>
				)}
				{editable && <Disconnect />}
			</div>
		);
	}
	return (
		<div className="grid gap-5">
			<div className="flex flex-wrap items-center justify-between gap-3">
				<p className="text-sm">
					{t("Connected to")} <span className="font-medium">{s.team}</span> {t("as")}{" "}
					{s.appUrl ? (
						<a
							href={s.appUrl}
							target="_blank"
							rel="noopener"
							className="font-medium underline-offset-4 hover:underline"
						>
							@{s.bot}
						</a>
					) : (
						<span className="font-medium">@{s.bot}</span>
					)}
					{s.source === "env" && <span className="text-muted"> {t("· set on the server")}</span>}
				</p>
				{editable && <Disconnect />}
			</div>
			{linked.length === 0 ? <LinkSlack /> : <LinkedSlack />}
			{isOwner && <TeamChannel channel={s.channel ?? ""} />}
			{isOwner && <InviteTeammate />}
		</div>
	);
}

/** Pulls both tokens out of whatever was typed, whichever field it landed in. */
function findTokens(text: string) {
	return {
		botToken: text.match(/xoxb-[A-Za-z0-9-]+/)?.[0],
		appToken: text.match(/xapp-[A-Za-z0-9-]+/)?.[0],
	};
}

function CreateApp({ status }: { status: SlackStatus }) {
	const qc = useQueryClient();
	const set = useSettingsSetSlack();
	const [bot, setBot] = useState("");
	const [app, setApp] = useState("");
	const [manifest, setManifest] = useState(false);
	const [error, setError] = useState<string>();

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		const { botToken, appToken } = findTokens(`${bot} ${app}`);
		if (!botToken || !appToken) {
			setError(
				t("Two tokens are needed: the bot token starts with xoxb- and the app-level token with xapp-."),
			);
			return;
		}
		setError(undefined);
		try {
			const s = await set.mutateAsync({ data: { botToken, appToken } });
			await qc.invalidateQueries();
			toast.success(s.team ? t("Connected to {0}", s.team) : t("Slack connected"));
		} catch (err) {
			setError(errText(err, t("Could not connect to Slack")));
		}
	};

	return (
		<div className="grid gap-5">
			<p className="text-sm text-muted">
				{t("Each team creates its own Slack app. We prepared everything.")}
			</p>
			<ol className="grid gap-3 text-sm">
				<li className="grid gap-2">
					<span>
						<span className="text-muted">1.</span>{" "}
						<a href={status.createUrl} target="_blank" rel="noopener" className={link}>
							{t("Create the Slack app")} <ExternalLink className="size-3.5" />
						</a>{" "}
						<span className="text-muted">{t("pick your workspace and confirm.")}</span>{" "}
						<button
							type="button"
							onClick={() => setManifest(!manifest)}
							className="text-xs text-muted underline underline-offset-4 hover:text-fg"
						>
							{manifest ? t("Hide the manifest") : t("Or paste the manifest")}
						</button>
					</span>
					{manifest && <CodeBlock code={status.manifest} className="[&_pre]:max-h-56 [&_pre]:break-normal" />}
				</li>
				<li>
					<span className="text-muted">2.</span> {t("Press")} <span className="font-medium">Install App</span>
					{t(", then copy the")} <span className="font-medium">Bot User OAuth Token</span>.
				</li>
				<li>
					<span className="text-muted">3.</span> {t("In")}{" "}
					<span className="font-medium">Basic Information</span>
					{t(", generate an")} <span className="font-medium">App-Level Token</span> {t("with the scope")}{" "}
					<code className="font-mono text-[12.5px]">connections:write</code>.
				</li>
			</ol>
			<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
				<Field label={t("Bot token")}>
					<PasswordInput
						value={bot}
						onChange={(e) => setBot(e.target.value)}
						placeholder="xoxb-…"
						autoComplete="off"
						spellCheck={false}
						required
					/>
				</Field>
				<Field label={t("App-level token")}>
					<PasswordInput
						value={app}
						onChange={(e) => setApp(e.target.value)}
						placeholder="xapp-…"
						autoComplete="off"
						spellCheck={false}
						required
					/>
				</Field>
				<Button type="submit" variant="primary" disabled={set.isPending}>
					{set.isPending && <Loader2 className="animate-spin" />}
					{t("Connect")}
				</Button>
			</form>
			{error && <p className="text-xs text-bad">{error}</p>}
		</div>
	);
}

function Disconnect() {
	const qc = useQueryClient();
	const remove = useSettingsRemoveSlack();
	const [confirm, setConfirm] = useState(false);
	if (!confirm) {
		return (
			<Button variant="ghost" size="sm" onClick={() => setConfirm(true)}>
				{t("Use another app")}
			</Button>
		);
	}
	return (
		<span className="flex items-center gap-2 text-xs text-muted">
			{t("Disconnect Slack?")}
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

/** Link this person's Slack account: a fresh one-time code to type in Slack. */
function LinkSlack() {
	const { mutateAsync } = useAuthLinkSlack();
	const [code, setCode] = useState<SlackLinkCode>();
	useAuthListLinks({ query: { refetchInterval: 2000 } }); // notices the link as soon as it happens
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
	return (
		<div className="grid gap-2 text-sm">
			<p className="font-medium">{t("Link your Slack account: type this in Slack, anywhere")}</p>
			{code ? (
				<CodeBlock code={code.command} />
			) : (
				<div className="grid h-10 place-items-center rounded-lg bg-raised">
					<Loader2 className="size-4 animate-spin text-muted" />
				</div>
			)}
			<p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
				<span className="flex items-center gap-2">
					<Loader2 className="size-3.5 animate-spin" /> {t("Waiting for Slack…")}
				</span>
				{code?.url && (
					<a
						href={code.url}
						target="_blank"
						rel="noopener"
						className="inline-flex items-center gap-1.5 hover:text-fg"
					>
						<ExternalLink className="size-3.5" /> {t("Open Exhale in Slack")}
					</a>
				)}
			</p>
		</div>
	);
}

function LinkedSlack() {
	const qc = useQueryClient();
	const links = useAuthListLinks();
	const unlink = useAuthUnlink();
	return (
		<div className="grid gap-2">
			{(links.data?.links ?? [])
				.filter((l) => l.channel === "slack")
				.map((l) => (
					<div
						key={l.id}
						className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2"
					>
						<span className="flex items-center gap-2 text-sm">
							<Check className="size-4 text-ok" />
							{t("Your Slack account is linked")}{" "}
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
								toast.success("Slack unlinked");
							}}
						>
							{t("Unlink")}
						</Button>
					</div>
				))}
		</div>
	);
}

/** The channel where renewals of shared subscriptions are also posted (owners). */
function TeamChannel({ channel }: { channel: string }) {
	const qc = useQueryClient();
	const set = useSettingsSetSlack();
	const [value, setValue] = useState(channel);
	const [error, setError] = useState<string>();
	const save = async (next: string) => {
		setError(undefined);
		try {
			const s = await set.mutateAsync({ data: { channel: next } });
			await qc.invalidateQueries();
			setValue(s.channel ?? "");
			toast.success(
				s.channel ? t("Renewals also go to {0}", s.channel) : t("Renewals stay in direct messages"),
			);
		} catch (err) {
			setError(errText(err, t("Could not set the channel")));
		}
	};
	return (
		<form
			onSubmit={(e: FormEvent) => {
				e.preventDefault();
				save(value.trim());
			}}
			className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-start"
		>
			<Field
				label={t("Team channel")}
				error={error}
				hint={t(
					"Renewals of shared subscriptions are also posted there. Invite the app first if it is private.",
				)}
			>
				<Input
					value={value}
					onChange={(e) => setValue(e.target.value)}
					placeholder="#subscriptions"
					spellCheck={false}
					autoComplete="off"
				/>
			</Field>
			<Button type="submit" className="sm:mt-[25px]" disabled={set.isPending || value.trim() === channel}>
				{t("Save")}
			</Button>
			{channel && (
				<Button variant="ghost" className="sm:mt-[25px]" disabled={set.isPending} onClick={() => save("")}>
					{t("Stop posting")}
				</Button>
			)}
		</form>
	);
}

/** Invite a teammate from Slack: they type one command and they're in (owners). */
function InviteTeammate() {
	const invite = useAuthCreateInvite();
	const [inv, setInv] = useState<InviteOut>();
	if (!inv) {
		return (
			<div className="flex flex-wrap items-center gap-3">
				<Button
					disabled={invite.isPending}
					onClick={async () => setInv(await invite.mutateAsync({ data: { role: "member" } }))}
				>
					<UserPlus />
					{t("Invite a teammate")}
				</Button>
				<span className="text-sm text-muted">
					{t("They join from Slack. Private subscriptions stay private.")}
				</span>
			</div>
		);
	}
	return (
		<div className="grid gap-2 text-sm">
			<p className="font-medium">{t("Send them this: they type it in Slack and they're in")}</p>
			<CodeBlock code={`/exhale join ${inv.code}`} />
			<p className="text-xs text-muted">{t("Works once, for 7 days.")}</p>
		</div>
	);
}
