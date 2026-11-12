import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowUpCircle, ExternalLink, Loader2, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { CodeBlock } from "@/components/connect";
import { Button } from "@/components/ui/button";
import {
	getGetUpdateQueryKey,
	getUpdate,
	useApplyUpdate,
	useAuthSession,
	useGetUpdate,
	type VersionInfo,
} from "@/gen/api";
import { onWindowUpdate, updateFromWindow, type WindowUpdate, windowOS } from "@/lib/desktop";
import { ApiError } from "@/lib/fetcher";
import { locale, t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// Updating Exhale from the web (ADR 0033): the server checks GitHub, and
// where it can replace itself (the Mac and Windows apps, a binary it may
// write) one click installs the new version and restarts it. The Linux
// packages are updated by Exhale's window, which runs as the person and asks
// for their password (pkexec). Elsewhere this shows the command to run.

const busyPhases = new Set(["downloading", "installing", "restarting"]);

const isBusy = (u?: VersionInfo) => !!u && busyPhases.has(u.progress.phase);

/** The update status, for owners: read once an hour, every second while an
 * update runs (the server goes away for a moment while it restarts). */
function useUpdate(enabled = true, fast = false) {
	return useGetUpdate(undefined, {
		query: {
			enabled,
			staleTime: 60 * 60 * 1000,
			refetchInterval: (q) => (fast || isBusy(q.state.data) ? 1000 : 60 * 60 * 1000),
			retry: false,
		},
	});
}

/** A link in the sidebar, for owners, while a newer Exhale is out. */
export function UpdateNotice() {
	const session = useAuthSession();
	const owner = session.data?.user?.role === "owner";
	const q = useUpdate(owner);
	const u = q.data;
	if (!owner || !u?.available) return null;
	return (
		<Link
			to="/settings"
			search={{ tab: "general" }}
			hash="updates"
			className="mb-1 flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-fg transition-colors hover:bg-frame-hover"
		>
			<ArrowUpCircle className="size-4 text-ok" />
			<span className="min-w-0 truncate">
				{isBusy(u) ? t("Updating Exhale…") : t("Exhale {0} is out", u.latest ?? "")}
			</span>
		</Link>
	);
}

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

/** Settings → General → Updates. */
export function Updates() {
	const qc = useQueryClient();
	// An update run by the window (Linux packages).
	const [win, setWin] = useState<WindowUpdate | null>(null);
	const winBusy = !!win && busyPhases.has(win.phase);
	const q = useUpdate(true, win?.phase === "installing" || win?.phase === "restarting");
	const apply = useApplyUpdate();
	const [checking, setChecking] = useState(false);
	const u = q.data;
	// The version being installed, kept while the server restarts.
	const target = useRef("");
	if (u && isBusy(u) && u.progress.version) target.current = u.progress.version;

	// Back with the new version: load the new web UI with it.
	useEffect(() => {
		if (!u || !target.current || u.current !== target.current) return;
		target.current = "";
		toast.success(t("Exhale is up to date: {0}", u.current));
		setTimeout(() => window.location.reload(), 1200);
	}, [u]);

	useEffect(
		() =>
			onWindowUpdate((w) => {
				if (w.phase === "failed" || w.phase === "cancelled") target.current = "";
				setWin(w);
			}),
		[],
	);

	useEffect(() => {
		if (window.location.hash === "#updates") {
			document.getElementById("updates")?.scrollIntoView({ block: "start", behavior: "smooth" });
		}
	}, []);

	if (!u) {
		return q.isError ? <p className="text-sm text-muted">{t("Could not read the version.")}</p> : null;
	}

	const check = async () => {
		setChecking(true);
		try {
			const fresh = await getUpdate({ refresh: true });
			qc.setQueryData(getGetUpdateQueryKey(), fresh);
			if (!fresh.checkError && !fresh.available) toast.success(t("You have the newest version."));
		} catch (err) {
			toast.error(errText(err, t("Could not check for updates")));
		} finally {
			setChecking(false);
		}
	};
	// The window can install the Linux package: it asks for the password.
	const viaWindow = !u.canApply && u.available && u.method === "package" && windowOS === "linux";
	const start = async () => {
		if (viaWindow) {
			target.current = u.latest ?? "";
			setWin({ phase: "downloading" });
			updateFromWindow();
			return;
		}
		try {
			const res = await apply.mutateAsync({ data: {} });
			if (res.progress.version) target.current = res.progress.version;
			qc.setQueryData(getGetUpdateQueryKey(), res);
		} catch (err) {
			toast.error(errText(err, t("Could not start the update")));
		}
	};

	const busy =
		winBusy ||
		isBusy(u) ||
		(!!target.current && u.current !== target.current && u.progress.phase !== "failed");
	const shown: VersionInfo =
		winBusy && win
			? { ...u, progress: { phase: win.phase as VersionInfo["progress"]["phase"], done: 0, total: 0 } }
			: u;
	return (
		<div className="grid gap-4">
			<VersionLine u={u} />
			{busy ? (
				<UpdateProgress u={shown} target={target.current} />
			) : u.available ? (
				<div className="grid gap-4">
					<ReleaseNotes u={u} />
					{(u.progress.phase === "failed" || win?.phase === "failed") && (
						<p className="text-sm text-bad" role="alert">
							{t("The update failed: {0}", (win?.phase === "failed" ? win.error : u.progress.error) ?? "")}{" "}
							<span className="text-muted">{t("Exhale {0} keeps running.", u.current)}</span>
						</p>
					)}
					{win?.phase === "cancelled" && (
						<p className="text-sm text-muted">{t("The update was cancelled. Nothing changed.")}</p>
					)}
					{u.canApply || viaWindow ? (
						<div className="flex flex-wrap items-center gap-3">
							<Button variant="primary" onClick={start} disabled={apply.isPending}>
								{apply.isPending ? <Loader2 className="animate-spin" /> : <ArrowUpCircle />}
								{u.progress.phase === "failed" ? t("Try again") : t("Update to {0}", u.latest ?? "")}
							</Button>
							<span className="text-xs text-muted">
								{viaWindow
									? t(
											"Your password is asked to install it. Exhale restarts by itself; your data stays as it is.",
										)
									: t("Exhale restarts by itself in a few seconds. Your data stays as it is.")}
							</span>
						</div>
					) : (
						<ManualUpdate u={u} />
					)}
				</div>
			) : null}
			{!busy && (
				<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t border-line pt-4">
					<span className="text-xs text-muted">
						{u.checkError
							? t("Could not check: {0}", u.checkError)
							: u.checkedAt
								? t("Checked {0}", when(u.checkedAt))
								: t("Not checked yet.")}
					</span>
					<Button size="sm" variant="ghost" onClick={check} disabled={checking}>
						{checking ? <Loader2 className="animate-spin" /> : <RefreshCw />}
						{t("Check now")}
					</Button>
				</div>
			)}
		</div>
	);
}

function VersionLine({ u }: { u: VersionInfo }) {
	let tone: "ok" | "new" | "muted" = "muted";
	let text: string;
	if (u.current === "dev") {
		text = t("A development build: it updates from its source code, not from here.");
	} else if (u.available) {
		tone = "new";
		text = t("Exhale {0} is out. You have {1}.", u.latest ?? "", u.current);
	} else if (u.latest) {
		tone = "ok";
		text = t("You have the newest version, Exhale {0}.", u.current);
	} else {
		text = t("You have Exhale {0}.", u.current);
	}
	return (
		<p className="flex items-start gap-2 text-sm" role="status">
			<span
				aria-hidden
				className={cn(
					"mt-1.5 size-2 shrink-0 rounded-full",
					tone === "ok" ? "bg-ok" : tone === "new" ? "bg-brand" : "bg-faint",
				)}
			/>
			<span>{text}</span>
		</p>
	);
}

/** What changed: the release's list of features and fixes, without the
 * links and commit ids release-please adds. */
function ReleaseNotes({ u }: { u: VersionInfo }) {
	const items = notes(u.notes ?? "").slice(0, 8);
	return (
		<div className="grid gap-2">
			{items.length > 0 && (
				<ul className="grid list-disc gap-1 pl-5 text-sm text-muted marker:text-faint">
					{items.map((x) => (
						<li key={x}>{x}</li>
					))}
				</ul>
			)}
			{u.notesUrl && (
				<a
					href={u.notesUrl}
					target="_blank"
					rel="noreferrer"
					className="inline-flex w-fit items-center gap-1.5 text-sm text-fg underline-offset-4 hover:underline"
				>
					{t("What's new in {0}", u.latest ?? "")}
					<ExternalLink className="size-3.5 text-muted" />
				</a>
			)}
		</div>
	);
}

/** The bullet points of release notes in Markdown. */
export function notes(md: string): string[] {
	return md
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l.startsWith("* ") || l.startsWith("- "))
		.map((l) =>
			l
				.slice(2)
				.replace(/\s*\(\[[^\]]*\]\([^)]*\)\)/g, "") // ([#25](…)) ([abc1234](…))
				.replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
				.replace(/^\*\*[^*]+:\*\*\s*/, "") // **scope:**
				.replace(/\*\*|__|`/g, "")
				.trim(),
		)
		.filter(Boolean);
}

function UpdateProgress({ u, target }: { u: VersionInfo; target: string }) {
	const p = u.progress;
	const version = p.version || target;
	const pct = p.total > 0 ? Math.min(100, Math.round((p.done / p.total) * 100)) : null;
	const text =
		p.phase === "downloading"
			? t("Downloading Exhale {0}…", version)
			: p.phase === "installing"
				? t("Installing Exhale {0}…", version)
				: t("Restarting with Exhale {0}… This page reloads by itself.", version);
	return (
		<div className="grid gap-2" role="status" aria-live="polite">
			<p className="flex items-center gap-2 text-sm">
				<Loader2 className="size-4 animate-spin text-muted" />
				{text}
				{p.phase === "downloading" && pct !== null && <span className="text-muted">{pct}%</span>}
			</p>
			<div className="h-1.5 overflow-hidden rounded-full bg-raised">
				<div
					className={cn(
						"h-full rounded-full bg-brand transition-[width] duration-500",
						p.phase !== "downloading" && "animate-pulse",
					)}
					style={{ width: `${p.phase === "downloading" ? (pct ?? 30) : 100}%` }}
				/>
			</div>
		</div>
	);
}

/** Where Exhale cannot replace itself: what to run, or what to download. */
function ManualUpdate({ u }: { u: VersionInfo }) {
	const why =
		u.reason === "manual" && u.method === "docker"
			? t("Exhale runs in a container: update it from where it runs, with:")
			: u.reason === "manual"
				? t("Exhale was installed with the system's package manager: update it with:")
				: u.reason === "read-only"
					? t("Exhale may not write where it is installed. Update it with:")
					: u.reason === "development"
						? t("A development build updates from its source code.")
						: t("Exhale cannot update itself on this system. Download the new version:");
	return (
		<div className="grid gap-2">
			<p className="text-sm text-muted">{why}</p>
			{u.command ? (
				<CodeBlock code={u.command} />
			) : (
				u.download && (
					<a
						href={u.download}
						target="_blank"
						rel="noreferrer"
						className="inline-flex w-fit items-center gap-1.5 text-sm text-fg underline-offset-4 hover:underline"
					>
						{t("Download Exhale {0}", u.latest ?? "")}
						<ExternalLink className="size-3.5 text-muted" />
					</a>
				)
			)}
		</div>
	);
}

/** "5 min ago", "yesterday", in the page's language. */
function when(iso: string): string {
	const ms = new Date(iso).getTime() - Date.now();
	if (Number.isNaN(ms)) return "";
	const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
	const min = Math.round(ms / 60000);
	if (Math.abs(min) < 60) return rtf.format(min, "minute");
	const h = Math.round(min / 60);
	if (Math.abs(h) < 24) return rtf.format(h, "hour");
	return rtf.format(Math.round(h / 24), "day");
}
