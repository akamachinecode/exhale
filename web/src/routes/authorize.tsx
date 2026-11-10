import { Link } from "@tanstack/react-router";
import { BadgeCheck, Loader2, MonitorSmartphone, ShieldAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { type OAuthRequestInfo, useAuthOauthDecide, useAuthOauthRequest } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { AuthFrame, errorText, FormError } from "./auth";

/** Why /oauth/authorize could not show the consent screen (internal/httpapi/oauth.go). */
const errors: Record<string, string> = {
	client: t(
		"Exhale could not identify the application. Start again from the application; if it keeps happening, it may not support Exhale yet.",
	),
	request: t(
		"The application sent a request Exhale cannot accept. Start again from the application; if it keeps happening, it may not support Exhale yet.",
	),
	config: t("Remote connections are not set up on this server. It needs EXHALE_PUBLIC_URL."),
	throttled: t("Too many attempts. Wait a minute and try again."),
	server: t("Something went wrong on the server. Try again."),
};

function params() {
	const q = new URLSearchParams(window.location.search);
	return { request: q.get("request") ?? "", error: q.get("error") ?? "" };
}

/**
 * The consent screen of remote MCP (ADR 0024): an application asks to act
 * as the signed-in member; the member sees who asks and where the answer
 * goes, picks read or read and change, and allows or denies.
 */
export function AuthorizePage() {
	const [{ request, error }] = useState(params);
	if (error || !request) {
		return (
			<AuthFrame title={t("Could not connect")}>
				<FormError message={errors[error] ?? errors.request} />
				<BackHome />
			</AuthFrame>
		);
	}
	return <Consent id={request} />;
}

function BackHome() {
	return (
		<Link to="/" className="mt-6 block text-center text-sm text-muted hover:text-fg">
			{t("Go to Exhale")}
		</Link>
	);
}

function Consent({ id }: { id: string }) {
	const info = useAuthOauthRequest(id, { query: { staleTime: Number.POSITIVE_INFINITY, retry: false } });
	if (info.isPending) {
		return (
			<AuthFrame title={t("Connect an application")}>
				<Loader2 className="mx-auto size-4 animate-spin text-faint" aria-label={t("Loading")} />
			</AuthFrame>
		);
	}
	if (info.isError) {
		const gone = info.error instanceof ApiError && info.error.status === 404;
		return (
			<AuthFrame title={t("Could not connect")}>
				<FormError
					message={
						gone
							? t("This request has expired or was already answered. Start again from the application.")
							: errorText(info.error, t("Could not load the request"))
					}
				/>
				<BackHome />
			</AuthFrame>
		);
	}
	return <Decide id={id} info={info.data} />;
}

function Decide({ id, info }: { id: string; info: OAuthRequestInfo }) {
	const decide = useAuthOauthDecide();
	const [scope, setScope] = useState<"read" | "write">(info.maxScope);
	const [leaving, setLeaving] = useState(false);
	const [error, setError] = useState<string>();
	// Coming back with the browser's Back button restores the page as it was left.
	useEffect(() => {
		const reset = () => setLeaving(false);
		window.addEventListener("pageshow", reset);
		return () => window.removeEventListener("pageshow", reset);
	}, []);
	const answer = async (approve: boolean) => {
		setError(undefined);
		setLeaving(true);
		try {
			const out = await decide.mutateAsync({ id, data: approve ? { approve, scope } : { approve } });
			window.location.assign(out.redirectTo);
		} catch (err) {
			setLeaving(false);
			setError(
				err instanceof ApiError && err.status === 404
					? t("This request has expired or was already answered. Start again from the application.")
					: errorText(err, t("Could not send the answer")),
			);
		}
	};
	const choices = [
		{
			value: "read" as const,
			title: t("Read only"),
			text: t("Sees subscriptions, charges, spending and settings. Changes nothing."),
		},
		...(info.maxScope === "write"
			? [
					{
						value: "write" as const,
						title: t("Read and change"),
						text: t("Can also make changes. Destructive ones ask you first, and you can undo them."),
					},
				]
			: []),
	];
	return (
		<AuthFrame
			title={t("Connect {0}?", info.clientName)}
			subtitle={t("It will act as you, {0}, in Exhale.", info.memberName)}
		>
			<div className="grid gap-5">
				<div className="grid gap-3 rounded-xl border border-line bg-surface p-4 text-sm">
					<div className="flex items-start gap-3">
						{info.verified ? (
							<BadgeCheck className="mt-0.5 size-4 shrink-0 text-ok" />
						) : (
							<ShieldAlert className="mt-0.5 size-4 shrink-0 text-warn" />
						)}
						<div className="min-w-0">
							<p className="flex flex-wrap items-center gap-2 font-medium">
								<span className="truncate">{info.clientName}</span>
								{info.verified ? (
									<Badge tone="ok">{info.clientHost}</Badge>
								) : (
									<Badge tone="warn">{t("Unverified")}</Badge>
								)}
							</p>
							<p className="mt-0.5 text-[13px] text-muted">
								{info.verified
									? t("Identified by its address, {0}.", info.clientHost ?? "")
									: t("It registered itself, so Exhale cannot confirm its name.")}
							</p>
						</div>
					</div>
					<div className="flex items-start gap-3">
						<MonitorSmartphone className="mt-0.5 size-4 shrink-0 text-faint" />
						<p className="text-[13px] text-muted">
							{t("After you answer, you go back to")}{" "}
							<span className="font-medium text-fg">{info.redirectHost}</span>
						</p>
					</div>
					{info.loopback && (
						<p className="rounded-lg bg-warn-soft px-3 py-2 text-[13px] text-warn">
							{t(
								"That is a program on this computer. Continue only if you started this from an app you trust here.",
							)}
						</p>
					)}
				</div>

				<fieldset className="grid gap-2">
					<legend className="mb-2 text-[13px] font-medium">{t("Access")}</legend>
					{choices.map((c) => (
						<label
							key={c.value}
							className={cn(
								"flex cursor-pointer gap-3 rounded-xl border border-line px-4 py-3 transition-colors hover:bg-raised",
								scope === c.value && "border-accent bg-accent-soft/40",
							)}
						>
							<input
								type="radio"
								name="scope"
								value={c.value}
								checked={scope === c.value}
								onChange={() => setScope(c.value)}
								className="mt-1 accent-[var(--color-accent)]"
							/>
							<span>
								<span className="block text-sm font-medium">{c.title}</span>
								<span className="block text-[13px] text-muted">{c.text}</span>
							</span>
						</label>
					))}
				</fieldset>

				<FormError message={error} />
				<div className="grid grid-cols-2 gap-3">
					<Button className="h-10" onClick={() => answer(false)} disabled={leaving}>
						{t("Deny")}
					</Button>
					<Button variant="primary" className="h-10" onClick={() => answer(true)} disabled={leaving}>
						{leaving ? <Loader2 className="animate-spin" /> : null}
						{t("Allow")}
					</Button>
				</div>
				<p className="text-center text-xs text-faint">
					{t("You can disconnect it at any time on the AI agents page.")}
				</p>
			</div>
		</AuthFrame>
	);
}
