import { useQueryClient } from "@tanstack/react-query";
import { Check, Loader2, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, type TextareaHTMLAttributes, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input, PasswordInput, Select } from "@/components/ui/field";
import {
	type ChannelsStatus,
	type DestinationInChannel,
	type LinkInfo,
	type SMTPInputTls,
	useAuthAddDestination,
	useAuthListLinks,
	useAuthSession,
	useAuthTestLink,
	useAuthUnlink,
	useSettingsChannels,
	useSettingsRemoveNtfy,
	useSettingsRemoveSmtp,
	useSettingsSetChannels,
	useSettingsSetNtfy,
	useSettingsSetSmtp,
	useSettingsTestSmtp,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { cn } from "@/lib/utils";

// Email, ntfy and webhooks: channels that only notify. Owners set up how
// they are sent (mail server, ntfy server); everyone chooses where their own
// reminders go. Copy is kept short; the details live in the docs.

const docs = "https://github.com/akamachinecode/exhale/blob/main/docs/guides/notifications.md";

function errText(e: unknown, fallback: string) {
	return e instanceof ApiError ? e.detail : fallback;
}

function Problem({ children }: { children?: string }) {
	return children ? <p className="text-xs text-bad">{children}</p> : null;
}

function Block({ title, hint, children }: { title: string; hint?: ReactNode; children: ReactNode }) {
	return (
		<section className="grid gap-3 py-5 first:pt-0 last:pb-0">
			<div>
				<h3 className="text-[13px] font-semibold">{title}</h3>
				{hint && <p className="mt-0.5 text-xs text-muted">{hint}</p>}
			</div>
			{children}
		</section>
	);
}

export function OtherChannels() {
	const session = useAuthSession();
	const status = useSettingsChannels();
	const links = useAuthListLinks();
	if (status.isPending || links.isPending) return <Loader2 className="size-4 animate-spin text-muted" />;
	const s = status.data;
	if (!s) return <p className="text-sm text-muted">Could not load the channels.</p>;
	const user = session.data?.user;
	const isOwner = user?.role === "owner";
	const mine = (channel: DestinationInChannel) =>
		(links.data?.links ?? []).filter((l) => l.channel === channel);
	return (
		<div className="divide-y divide-line">
			<Block title="Email">
				<MailServer status={s} isOwner={isOwner} />
				{s.smtp.configured && <EmailDestination links={mine("email")} account={user?.email ?? ""} />}
			</Block>
			<Block title="ntfy" hint="Push notifications on your phone, through the ntfy app.">
				<NtfyServer status={s} isOwner={isOwner} />
				<NtfyTopic links={mine("ntfy")} />
			</Block>
			<Block
				title="Webhook"
				hint={
					<>
						One POST per reminder: Discord, Slack, Home Assistant…{" "}
						<a href={docs} target="_blank" rel="noopener" className="underline underline-offset-4">
							Examples
						</a>
					</>
				}
			>
				<Webhooks links={mine("webhook")} />
				{isOwner && <PrivateDestinations status={s} />}
			</Block>
			{isOwner && !s.publicUrl && (
				<p className="pt-4 text-xs text-muted">
					These messages have no link back to Exhale yet. Start the server with{" "}
					<code className="font-mono">--public-url</code> to add one.
				</p>
			)}
		</div>
	);
}

/** One destination of the signed-in person: what it is, a test and remove. */
function Destination({ link, label, name }: { link: LinkInfo; label: ReactNode; name: string }) {
	const qc = useQueryClient();
	const test = useAuthTestLink();
	const unlink = useAuthUnlink();
	const [error, setError] = useState<string>();
	return (
		<div className="grid gap-1.5">
			<div className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2">
				<span className="flex min-w-0 items-center gap-2 text-sm">
					<Check className="size-4 shrink-0 text-ok" />
					<span className="truncate">{label}</span>
				</span>
				<span className="flex shrink-0 items-center gap-1">
					<Button
						variant="ghost"
						size="sm"
						disabled={test.isPending}
						onClick={async () => {
							setError(undefined);
							try {
								await test.mutateAsync({ data: { id: link.id } });
								toast.success(`Test sent to ${name}`);
							} catch (err) {
								setError(errText(err, "The test could not be sent"));
							}
						}}
					>
						{test.isPending && <Loader2 className="animate-spin" />}
						Test
					</Button>
					<Button
						variant="ghost"
						size="icon"
						aria-label={`Remove ${name}`}
						disabled={unlink.isPending}
						onClick={async () => {
							await unlink.mutateAsync({ data: { id: link.id } });
							await qc.invalidateQueries();
							toast.success(`${name} removed`);
						}}
					>
						<Trash2 />
					</Button>
				</span>
			</div>
			<Problem>{error}</Problem>
		</div>
	);
}

/** Adds a destination; reports the server's reason when it is refused. */
function useAdd(channel: DestinationInChannel) {
	const qc = useQueryClient();
	const add = useAuthAddDestination();
	const [error, setError] = useState<string>();
	const run = async (destination: string, template?: string) => {
		setError(undefined);
		try {
			await add.mutateAsync({ data: { channel, destination, template } });
			await qc.invalidateQueries();
			return true;
		} catch (err) {
			setError(errText(err, "Could not add it"));
			return false;
		}
	};
	return { run, error, pending: add.isPending };
}

const security: Record<SMTPInputTls, string> = {
	starttls: "STARTTLS",
	tls: "TLS",
	none: "None (local relay)",
};
const ports: Record<SMTPInputTls, number> = { starttls: 587, tls: 465, none: 25 };

function MailServer({ status, isOwner }: { status: ChannelsStatus; isOwner: boolean }) {
	const qc = useQueryClient();
	const test = useSettingsTestSmtp();
	const remove = useSettingsRemoveSmtp();
	const [editing, setEditing] = useState(false);
	const [error, setError] = useState<string>();
	const smtp = status.smtp;
	if (!isOwner) {
		return smtp.configured ? null : <p className="text-sm text-muted">The owner hasn't set up email yet.</p>;
	}
	if (!smtp.configured || editing) {
		return <MailServerForm status={status} onDone={editing ? () => setEditing(false) : undefined} />;
	}
	const fromEnv = smtp.source === "env";
	return (
		<div className="grid gap-1.5">
			<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
				<p className="min-w-0 text-sm">
					<span className="font-medium">
						{smtp.host}:{smtp.port}
					</span>
					<span className="text-muted">
						{" "}
						· {security[smtp.tls ?? "starttls"]} · from {smtp.from}
						{fromEnv && " · set on the server"}
					</span>
				</p>
				<span className="flex items-center gap-1">
					<Button
						size="sm"
						disabled={test.isPending}
						onClick={async () => {
							setError(undefined);
							try {
								const out = await test.mutateAsync();
								toast.success(`Test sent to ${out.sentTo}`);
							} catch (err) {
								setError(errText(err, "The test could not be sent"));
							}
						}}
					>
						{test.isPending && <Loader2 className="animate-spin" />}
						Send test
					</Button>
					{!fromEnv && (
						<>
							<Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
								Edit
							</Button>
							<Button
								variant="ghost"
								size="icon"
								aria-label="Remove the mail server"
								disabled={remove.isPending}
								onClick={async () => {
									await remove.mutateAsync();
									await qc.invalidateQueries();
									toast.success("Mail server removed");
								}}
							>
								<Trash2 />
							</Button>
						</>
					)}
				</span>
			</div>
			<Problem>{error}</Problem>
		</div>
	);
}

function MailServerForm({ status, onDone }: { status: ChannelsStatus; onDone?: () => void }) {
	const qc = useQueryClient();
	const save = useSettingsSetSmtp();
	const smtp = status.smtp;
	const [host, setHost] = useState(smtp.host ?? "");
	const [tls, setTls] = useState<SMTPInputTls>(smtp.tls ?? "starttls");
	const [port, setPort] = useState(smtp.port ? String(smtp.port) : "");
	const [username, setUsername] = useState(smtp.username ?? "");
	const [password, setPassword] = useState("");
	const [from, setFrom] = useState(smtp.from ?? "");
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			await save.mutateAsync({
				data: { host, tls, port: port ? Number(port) : undefined, username, password, from },
			});
			await qc.invalidateQueries();
			toast.success("Email is ready");
			onDone?.();
		} catch (err) {
			setError(errText(err, "Could not save the mail server"));
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3">
			<div className="grid gap-3 sm:grid-cols-[2fr_1fr_1.4fr]">
				<Field label="Mail server">
					<Input
						value={host}
						onChange={(e) => setHost(e.target.value)}
						placeholder="smtp.example.com"
						required
						spellCheck={false}
						autoComplete="off"
					/>
				</Field>
				<Field label="Port">
					<Input
						value={port}
						onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))}
						placeholder={String(ports[tls])}
						inputMode="numeric"
						maxLength={5}
					/>
				</Field>
				<Field label="Security">
					<Select value={tls} onChange={(e) => setTls(e.target.value as SMTPInputTls)}>
						{Object.entries(security).map(([k, label]) => (
							<option key={k} value={k}>
								{label}
							</option>
						))}
					</Select>
				</Field>
			</div>
			<div className="grid gap-3 sm:grid-cols-2">
				<Field label="Username">
					<Input
						value={username}
						onChange={(e) => setUsername(e.target.value)}
						spellCheck={false}
						autoComplete="off"
					/>
				</Field>
				<Field label="Password">
					<PasswordInput
						value={password}
						onChange={(e) => setPassword(e.target.value)}
						placeholder={smtp.hasPassword ? "Saved. Type to change it" : undefined}
						autoComplete="new-password"
					/>
				</Field>
			</div>
			<div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
				<Field label="From">
					<Input
						type="email"
						value={from}
						onChange={(e) => setFrom(e.target.value)}
						placeholder="reminders@example.com"
						required
					/>
				</Field>
				<span className="flex items-center gap-2">
					<Button type="submit" variant="primary" disabled={save.isPending}>
						{save.isPending && <Loader2 className="animate-spin" />}
						Save
					</Button>
					{onDone && (
						<Button variant="ghost" onClick={onDone}>
							Cancel
						</Button>
					)}
				</span>
			</div>
			<Problem>{error}</Problem>
		</form>
	);
}

/** Email on or off for the signed-in person, to their account address by default. */
function EmailDestination({ links, account }: { links: LinkInfo[]; account: string }) {
	const add = useAdd("email");
	const [address, setAddress] = useState(account);
	if (links.length > 0) {
		return (
			<div className="grid gap-2">
				{links.map((l) => (
					<Destination key={l.id} link={l} label={l.destination} name={l.destination ?? "email"} />
				))}
			</div>
		);
	}
	return (
		<form
			onSubmit={async (e: FormEvent) => {
				e.preventDefault();
				if (await add.run(address)) toast.success(`Reminders will also go to ${address}`);
			}}
			className="grid gap-2"
		>
			<div className="grid gap-2 sm:grid-cols-[1fr_auto]">
				<Input
					type="email"
					value={address}
					onChange={(e) => setAddress(e.target.value)}
					aria-label="Email address for your reminders"
					placeholder="you@example.com"
					required
				/>
				<Button type="submit" disabled={add.pending}>
					Email me reminders
				</Button>
			</div>
			<Problem>{add.error}</Problem>
		</form>
	);
}

function host(url: string) {
	try {
		return new URL(url).host;
	} catch {
		return url;
	}
}

function NtfyServer({ status, isOwner }: { status: ChannelsStatus; isOwner: boolean }) {
	const qc = useQueryClient();
	const save = useSettingsSetNtfy();
	const reset = useSettingsRemoveNtfy();
	const ntfy = status.ntfy;
	const [editing, setEditing] = useState(false);
	const [server, setServer] = useState(ntfy.server);
	const [token, setToken] = useState("");
	const [error, setError] = useState<string>();
	if (!editing) {
		return (
			<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
				<p className="text-sm">
					<span className="text-muted">Server: </span>
					<a href={ntfy.server} target="_blank" rel="noopener" className="font-medium hover:underline">
						{host(ntfy.server)}
					</a>
					<span className="text-muted">
						{ntfy.hasToken && " · with access token"}
						{ntfy.source === "env" && " · set on the server"}
					</span>
				</p>
				{isOwner && ntfy.source !== "env" && (
					<Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
						Use another server
					</Button>
				)}
			</div>
		);
	}
	const done = async () => {
		await qc.invalidateQueries();
		setToken("");
		setEditing(false);
	};
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			await save.mutateAsync({ data: { server, token } });
			toast.success(`Using ${host(server)}`);
			await done();
		} catch (err) {
			setError(errText(err, "Could not save the server"));
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3">
			<div className="grid gap-3 sm:grid-cols-2">
				<Field label="Server address">
					<Input
						type="url"
						value={server}
						onChange={(e) => setServer(e.target.value)}
						placeholder="https://ntfy.example.com"
						required
						spellCheck={false}
					/>
				</Field>
				<Field label="Access token" hint="Only if the server needs one to publish.">
					<PasswordInput
						value={token}
						onChange={(e) => setToken(e.target.value)}
						placeholder={ntfy.hasToken ? "Saved. Type to change it" : "tk_…"}
						autoComplete="off"
					/>
				</Field>
			</div>
			<span className="flex flex-wrap items-center gap-2">
				<Button type="submit" variant="primary" disabled={save.isPending}>
					{save.isPending && <Loader2 className="animate-spin" />}
					Save
				</Button>
				<Button variant="ghost" onClick={() => setEditing(false)}>
					Cancel
				</Button>
				{ntfy.source === "settings" && (
					<Button
						variant="ghost"
						disabled={reset.isPending}
						onClick={async () => {
							await reset.mutateAsync();
							setServer("https://ntfy.sh");
							toast.success("Using ntfy.sh");
							await done();
						}}
					>
						Back to ntfy.sh
					</Button>
				)}
			</span>
			<Problem>{error}</Problem>
		</form>
	);
}

/** A topic nobody would guess: on a public server the name is the only lock. */
function suggestTopic() {
	const bytes = crypto.getRandomValues(new Uint8Array(6));
	return `exhale-${Array.from(bytes, (b) => b.toString(36).padStart(2, "0")).join("")}`;
}

function NtfyTopic({ links }: { links: LinkInfo[] }) {
	const add = useAdd("ntfy");
	const [topic, setTopic] = useState(suggestTopic);
	if (links.length > 0) {
		return (
			<div className="grid gap-2">
				{links.map((l) => (
					<Destination
						key={l.id}
						link={l}
						label={
							<>
								<span className="text-muted">Topic </span>
								<span className="font-mono text-[13px]">{l.destination}</span>
							</>
						}
						name={l.destination ?? "ntfy"}
					/>
				))}
			</div>
		);
	}
	return (
		<form
			onSubmit={async (e: FormEvent) => {
				e.preventDefault();
				if (await add.run(topic)) toast.success("Subscribe to the topic in the ntfy app");
			}}
			className="grid gap-2"
		>
			<div className="grid gap-2 sm:grid-cols-[1fr_auto] sm:items-start">
				<Field label="Your topic" hint="Subscribe to it in the ntfy app. Keep it hard to guess.">
					<Input
						value={topic}
						onChange={(e) => setTopic(e.target.value)}
						className="font-mono"
						required
						maxLength={64}
						spellCheck={false}
						autoComplete="off"
					/>
				</Field>
				<Button type="submit" disabled={add.pending} className="sm:mt-[26px]">
					Send me reminders
				</Button>
			</div>
			<Problem>{add.error}</Problem>
		</form>
	);
}

// Body templates: text variables go inside quotes. \n is JSON's line break.
const bodies = {
	default: { label: "Exhale's JSON", template: "" },
	discord: { label: "Discord", template: String.raw`{"content": "**{{title}}**\n{{message}}\n{{link}}"}` },
	slack: { label: "Slack", template: String.raw`{"text": "*{{title}}*\n{{message}}\n{{link}}"}` },
	custom: { label: "Custom", template: '{"title": "{{title}}", "message": "{{message}}"}' },
} as const;
type Body = keyof typeof bodies;

function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
	return (
		<textarea
			className={cn(
				"w-full rounded-lg border border-line bg-surface px-3 py-2 font-mono text-[12px] leading-relaxed text-fg placeholder:text-faint focus:border-accent focus:outline-2 focus:outline-accent/25",
				className,
			)}
			{...props}
		/>
	);
}

function Webhooks({ links }: { links: LinkInfo[] }) {
	const add = useAdd("webhook");
	const [url, setUrl] = useState("");
	const [body, setBody] = useState<Body>("default");
	const [template, setTemplate] = useState("");
	return (
		<div className="grid gap-3">
			{links.length > 0 && (
				<div className="grid gap-2">
					{links.map((l) => (
						<Destination
							key={l.id}
							link={l}
							label={
								<>
									{l.destination}
									{l.hasTemplate && <span className="text-muted"> · own body</span>}
								</>
							}
							name={l.destination ?? "webhook"}
						/>
					))}
				</div>
			)}
			<form
				onSubmit={async (e: FormEvent) => {
					e.preventDefault();
					if (await add.run(url, body === "default" ? "" : template)) {
						setUrl("");
						toast.success("Webhook added");
					}
				}}
				className="grid gap-3"
			>
				<div className="grid gap-3 sm:grid-cols-[1fr_10rem_auto] sm:items-end">
					<Field label="Address">
						<Input
							type="url"
							value={url}
							onChange={(e) => setUrl(e.target.value)}
							placeholder="https://discord.com/api/webhooks/…"
							required
							spellCheck={false}
							autoComplete="off"
						/>
					</Field>
					<Field label="Body">
						<Select
							value={body}
							onChange={(e) => {
								const next = e.target.value as Body;
								setBody(next);
								setTemplate(bodies[next].template);
							}}
						>
							{Object.entries(bodies).map(([k, b]) => (
								<option key={k} value={k}>
									{b.label}
								</option>
							))}
						</Select>
					</Field>
					<Button type="submit" disabled={add.pending}>
						Add webhook
					</Button>
				</div>
				{body !== "default" && (
					<Textarea
						value={template}
						onChange={(e) => setTemplate(e.target.value)}
						rows={3}
						aria-label="Body template"
						spellCheck={false}
					/>
				)}
				<Problem>{add.error}</Problem>
			</form>
		</div>
	);
}

/** Owners: let ntfy and webhooks reach addresses on the private network. */
function PrivateDestinations({ status }: { status: ChannelsStatus }) {
	const qc = useQueryClient();
	const set = useSettingsSetChannels();
	const fromEnv = status.allowPrivateSource === "env";
	return (
		<label className="flex items-start gap-2.5 text-sm">
			<input
				type="checkbox"
				className="mt-0.5 size-4 accent-fg"
				checked={status.allowPrivate}
				disabled={fromEnv || set.isPending}
				onChange={async (e) => {
					try {
						await set.mutateAsync({ data: { allowPrivate: e.target.checked } });
						await qc.invalidateQueries();
					} catch (err) {
						toast.error(errText(err, "Could not save"));
					}
				}}
			/>
			<span>
				Allow addresses on my private network
				<span className="block text-xs text-muted">
					For ntfy or a webhook on your LAN, like Home Assistant.{fromEnv && " Set on the server."}
				</span>
			</span>
		</label>
	);
}
