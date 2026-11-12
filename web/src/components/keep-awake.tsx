import { useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/field";
import {
	type KeepAwakeInfo,
	type KeepAwakeSessionInput,
	type SetKeepAwakeInput,
	useGetKeepAwake,
	useKeepAwakeSession,
	useSetKeepAwake,
	useSetupLidClosed,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { locale, t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

/** The battery levels offered; 0 is "never stop". */
const floors = [0, 10, 20, 30, 50];

const percent = (n: number) => new Intl.NumberFormat(locale, { style: "percent" }).format(n / 100);

/** The time of day of an RFC 3339 instant in the household's zone, with
 * the weekday when it is not today. */
function clock(at: string, zone: string, now: number): string {
	const d = new Date(at);
	if (Number.isNaN(d.getTime())) return "";
	const day = (x: Date) => new Intl.DateTimeFormat("en-CA", { timeZone: zone }).format(x);
	const opts: Intl.DateTimeFormatOptions = { hour: "2-digit", minute: "2-digit", timeZone: zone };
	if (day(d) !== day(new Date(now))) opts.weekday = "short";
	try {
		return new Intl.DateTimeFormat(locale, opts).format(d);
	} catch {
		return new Intl.DateTimeFormat(locale, { ...opts, timeZone: undefined }).format(d);
	}
}

/** Time left in words: "2 h 10 min", "45 min". */
function left(ms: number): string {
	const m = Math.max(1, Math.ceil(ms / 60000));
	const h = Math.floor(m / 60);
	const r = m % 60;
	if (!h) return t("{0} min", r);
	return r ? t("{0} h {1} min", h, r) : t("{0} h", h);
}

/** The current time, every half minute. */
function useNow(): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const id = setInterval(() => setNow(Date.now()), 30_000);
		return () => clearInterval(id);
	}, []);
	return now;
}

/** Owners: keep the computer Exhale runs on from sleeping (ADR 0028, 0032). */
export function KeepAwake() {
	const q = useGetKeepAwake();
	const qc = useQueryClient();
	const set = useSetKeepAwake();
	const session = useKeepAwakeSession();
	const lid = useSetupLidClosed();
	// The clock, at least as late as the data shown (a session just started).
	const now = Math.max(useNow(), q.dataUpdatedAt);
	const k = q.data;
	const ends = k?.status.until || k?.session?.until;
	// When it stops by itself, ask the server what happens next.
	useEffect(() => {
		if (!ends) return;
		const ms = new Date(ends).getTime() - Date.now();
		if (Number.isNaN(ms) || ms > 2 ** 31 - 1) return;
		const id = setTimeout(() => qc.invalidateQueries(), Math.max(0, ms) + 1500);
		return () => clearTimeout(id);
	}, [ends, qc]);
	if (!k) return null;
	const zone = k.timezone.replaceAll("_", " ");
	const busy = set.isPending || session.isPending;
	const save = async (data: SetKeepAwakeInput) => {
		try {
			await set.mutateAsync({ data });
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not save")));
		}
	};
	const act = async (data: KeepAwakeSessionInput) => {
		try {
			await session.mutateAsync({ data });
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not save")));
		}
	};
	const setup = async () => {
		try {
			await lid.mutateAsync({ data: {} });
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not save")));
		}
	};
	const time = (field: "from" | "to", value: string, label: string) => (
		<Input
			type="time"
			aria-label={label}
			className="w-32"
			defaultValue={value}
			key={value}
			disabled={busy}
			onBlur={(e) => {
				if (e.target.value && e.target.value !== value) save({ [field]: e.target.value });
			}}
		/>
	);
	const power = k.status.power;
	return (
		<div className="grid gap-5">
			<div className="grid gap-3">
				<p className="text-sm text-muted">
					{t(
						"While this computer sleeps, reminders wait and the buttons in Telegram and Slack stop answering.",
					)}
				</p>
				<KeepAwakeStatusLine k={k} now={now} />
			</div>
			<Now k={k} now={now} busy={busy} act={act} />
			<div className="grid gap-4">
				<div className="grid gap-2">
					<Field label={t("Keep this computer awake")}>
						<Select
							className="max-w-64"
							value={k.mode}
							disabled={busy}
							onChange={(e) => save({ mode: e.target.value as SetKeepAwakeInput["mode"] })}
						>
							<option value="off">{t("Off")}</option>
							<option value="always">{t("Always")}</option>
							<option value="hours">{t("Only between two times")}</option>
						</Select>
					</Field>
					{k.mode === "hours" && (
						<div className="flex flex-wrap items-center gap-2 text-sm">
							<span aria-hidden>{t("Between")}</span>
							{time("from", k.from, t("Start time"))}
							<span aria-hidden>{t("and")}</span>
							{time("to", k.to, t("End time"))}
							<span className="text-xs text-muted">
								{k.to < k.from ? t("the next day, {0} time", zone) : t("{0} time", zone)}
							</span>
						</div>
					)}
				</div>
				<Check
					label={t("Let the display sleep")}
					hint={t("Only the computer stays on; the screen turns off as usual.")}
					checked={!k.keepDisplayOn}
					disabled={busy}
					onChange={(v) => save({ keepDisplayOn: !v })}
				/>
			</div>
			<div className="grid gap-4 border-t border-line pt-5">
				<div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
					<h3 className="text-[13px] font-medium">{t("On a laptop")}</h3>
					<span className="text-xs text-muted">
						{power?.hasBattery
							? power.onBattery
								? t("On battery, {0}", percent(power.percent))
								: t("Plugged in, battery at {0}", percent(power.percent))
							: t("Only matters on a laptop.")}
					</span>
				</div>
				<div className="grid gap-2">
					<Check
						label={t("Keep going with the lid closed")}
						hint={t("Closing the lid does not send it to sleep while it is kept awake.")}
						checked={k.lidClosed}
						disabled={busy}
						onChange={(v) => save({ lidClosed: v })}
					/>
					<LidState k={k} pending={lid.isPending} onSetup={setup} />
				</div>
				<Check
					label={t("Only while plugged in")}
					hint={t("On battery it sleeps as usual.")}
					checked={k.onlyOnPower}
					disabled={busy}
					onChange={(v) => save({ onlyOnPower: v })}
				/>
				<Field
					label={t("On battery, stop at")}
					hint={
						k.onlyOnPower
							? t("Not used: it only stays awake while plugged in.")
							: t("Below this charge it sleeps as usual, so the battery is not drained.")
					}
				>
					<Select
						className="max-w-40"
						value={String(k.batteryFloor)}
						disabled={busy || k.onlyOnPower}
						onChange={(e) => save({ batteryFloor: Number(e.target.value) })}
					>
						{[...new Set([...floors, k.batteryFloor])]
							.sort((a, b) => a - b)
							.map((n) => (
								<option key={n} value={n}>
									{n === 0 ? t("Never") : percent(n)}
								</option>
							))}
					</Select>
				</Field>
			</div>
		</div>
	);
}

function Check({
	label,
	hint,
	checked,
	disabled,
	onChange,
}: {
	label: string;
	hint: string;
	checked: boolean;
	disabled: boolean;
	onChange: (checked: boolean) => void;
}) {
	return (
		<label className="flex items-start gap-2.5 text-sm">
			<input
				type="checkbox"
				className="mt-0.5 size-4 accent-fg"
				checked={checked}
				disabled={disabled}
				onChange={(e) => onChange(e.target.checked)}
			/>
			<span>
				{label}
				<span className="block text-xs text-muted">{hint}</span>
			</span>
		</label>
	);
}

/** A session on top of the rule: keep it awake now, for a while. */
function Now({
	k,
	now,
	busy,
	act,
}: {
	k: KeepAwakeInfo;
	now: number;
	busy: boolean;
	act: (data: KeepAwakeSessionInput) => Promise<void>;
}) {
	const [picking, setPicking] = useState(false);
	const [until, setUntil] = useState("");
	const s = k.session;
	const end = s?.until ? new Date(s.until).getTime() : 0;
	const active = !!s?.active && (!s.until || end > now);
	let body: ReactNode;
	if (active) {
		body = (
			<>
				<div className="grid min-w-0 gap-0.5">
					<span className="text-sm font-medium">
						{s?.until ? t("Awake until {0}", clock(s.until, k.timezone, now)) : t("Awake until you stop it")}
					</span>
					<span className="text-xs text-muted">
						{s?.until ? t("{0} left", left(end - now)) : t("Then the rule below applies again.")}
					</span>
				</div>
				<div className="flex gap-2">
					{s?.until && (
						<Button size="sm" disabled={busy} onClick={() => act({ action: "extend", minutes: 60 })}>
							{t("+1 h")}
						</Button>
					)}
					<Button size="sm" disabled={busy} onClick={() => act({ action: "stop" })}>
						{t("Stop")}
					</Button>
				</div>
			</>
		);
	} else if (picking) {
		body = (
			<form
				className="flex w-full flex-wrap items-center gap-2"
				onSubmit={async (e) => {
					e.preventDefault();
					if (!until) return;
					await act({ action: "start", until });
					setPicking(false);
					setUntil("");
				}}
			>
				<span className="text-sm">{t("Keep it awake until")}</span>
				<Input
					type="time"
					aria-label={t("Until")}
					className="h-8 w-32"
					required
					autoFocus
					value={until}
					disabled={busy}
					onChange={(e) => setUntil(e.target.value)}
				/>
				<div className="flex gap-2">
					<Button size="sm" type="submit" variant="primary" disabled={busy || !until}>
						{t("Start")}
					</Button>
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => setPicking(false)}>
						{t("Cancel")}
					</Button>
				</div>
			</form>
		);
	} else {
		body = (
			<>
				<span className="text-sm font-medium">{t("Keep it awake now")}</span>
				<div className="flex flex-wrap gap-2">
					{[60, 120, 480].map((m) => (
						<Button key={m} size="sm" disabled={busy} onClick={() => act({ action: "start", minutes: m })}>
							{t("{0} h", m / 60)}
						</Button>
					))}
					<Button size="sm" disabled={busy} onClick={() => setPicking(true)}>
						{t("Until…")}
					</Button>
					<Button size="sm" disabled={busy} onClick={() => act({ action: "start" })}>
						{t("Until I stop it")}
					</Button>
				</div>
			</>
		);
	}
	return (
		<div
			className={cn(
				"flex flex-wrap items-center justify-between gap-3 rounded-xl border border-line px-4 py-3",
				active ? "bg-raised" : "bg-surface",
			)}
			data-session={active ? "active" : "off"}
		>
			{body}
		</div>
	);
}

/** Whether a closed lid is kept awake, and the Mac's one-time setup. */
function LidState({ k, pending, onSetup }: { k: KeepAwakeInfo; pending: boolean; onSetup: () => void }) {
	const s = k.status;
	if (s.lid === "setup") {
		return (
			<div className="ml-6.5 grid gap-3 rounded-xl border border-line bg-raised p-3 text-sm">
				<p className="text-muted">
					{t(
						"A Mac asks for an administrator's permission once. The password dialog appears on the Mac that runs Exhale, so someone has to be at it.",
					)}
				</p>
				<div className="flex flex-wrap items-center gap-3">
					<Button size="sm" variant="primary" disabled={pending} onClick={onSetup}>
						{pending && <Loader2 className="animate-spin" />}
						{t("Allow on this Mac")}
					</Button>
					{pending && (
						<span className="text-xs text-muted" role="status">
							{t("Waiting for the password on the Mac…")}
						</span>
					)}
				</div>
			</div>
		);
	}
	if (!k.lidClosed) return null;
	if (s.lid === "active")
		return <Note tone="ok">{t("Active now: closing the lid does not send it to sleep.")}</Note>;
	if (s.lid === "unavailable")
		return <Note tone="bad">{t("Not available here: {0}", s.lidError ?? "")}</Note>;
	if (s.lid === "failed") return <Note tone="bad">{t("The system refused: {0}", s.lidError ?? "")}</Note>;
	return null;
}

function Note({ tone, children }: { tone: "ok" | "bad"; children: ReactNode }) {
	return (
		<p className={cn("ml-6.5 flex items-start gap-2 text-xs", tone === "bad" ? "text-bad" : "text-muted")}>
			<span
				aria-hidden
				className={cn("mt-1 size-1.5 shrink-0 rounded-full", tone === "ok" ? "bg-ok" : "bg-bad")}
			/>
			{children}
		</p>
	);
}

function KeepAwakeStatusLine({ k, now }: { k: KeepAwakeInfo; now: number }) {
	const s = k.status;
	let tone: "ok" | "muted" | "bad" | "warn" = "muted";
	let text: string;
	switch (s.state) {
		case "active":
			tone = "ok";
			// A session shows its own end in the row below.
			text =
				s.until && !s.session
					? t("Active now: this computer stays awake until {0}.", clock(s.until, k.timezone, now))
					: t("Active now: this computer stays awake.");
			break;
		case "waiting":
			text = t("Not active now: outside the hours. It starts again at {0}.", k.from);
			break;
		case "paused":
			tone = "warn";
			text =
				s.reason === "battery"
					? t(
							"Paused: the battery is at {0}, at or below {1}. It sleeps as usual until it is charged.",
							percent(s.power?.percent ?? 0),
							percent(k.batteryFloor),
						)
					: t("Paused: on battery, and set to stay awake only while plugged in.");
			break;
		case "unavailable":
			tone = "bad";
			text =
				s.reason === "container"
					? t(
							"Not available here: Exhale runs in a container. Keep the computer that runs it awake in its own settings.",
						)
					: s.reason === "unsupported"
						? t("Not available on this operating system.")
						: t("Not available here: {0}", s.error ?? "");
			break;
		case "failed":
			tone = "bad";
			text = t("The system refused: {0}", s.error ?? "");
			break;
		case "unknown":
			text = t("Exhale is not running as a server here, so nothing is applied.");
			break;
		default:
			text = t("Off: this computer sleeps as usual.");
	}
	const battery =
		s.state === "active" && s.power?.onBattery ? t("On battery, {0}", percent(s.power.percent)) : "";
	return (
		<p className="flex items-start gap-2 text-sm" role="status" data-state={s.state}>
			<span
				aria-hidden
				className={cn(
					"mt-1.5 size-2 shrink-0 rounded-full",
					tone === "ok" ? "bg-ok" : tone === "bad" ? "bg-bad" : tone === "warn" ? "bg-warn" : "bg-faint",
				)}
			/>
			<span className={cn(tone === "bad" && "text-bad")}>
				{text}
				{battery && <span className="text-muted"> · {battery}</span>}
			</span>
		</p>
	);
}
