import { useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { ArrowRight, Loader2 } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { Layout, Logo } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Field, Input, PasswordInput } from "@/components/ui/field";
import {
	type AuthSession,
	getAuthSessionQueryKey,
	useAuthGetInvite,
	useAuthJoin,
	useAuthLogin,
	useAuthSession,
	useAuthSetup,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { browserLocale, browserTimezone, guessCurrency } from "@/lib/locale";
import { applySession } from "@/lib/session";

/**
 * Root of the web UI (ADR 0006): sign-up on a fresh instance, sign-in
 * without a session, invite pages for anyone, and the app once signed in.
 */
export function Root() {
	const session = useAuthSession({ query: { staleTime: Number.POSITIVE_INFINITY, retry: 2 } });
	const path = useRouterState({ select: (s) => s.location.pathname });
	if (session.isPending) return <Splash />;
	if (session.isError) {
		return (
			<AuthFrame title="Exhale is not answering" subtitle="Check that the server is running, then try again.">
				<Button variant="primary" className="w-full" onClick={() => session.refetch()}>
					Try again
				</Button>
			</AuthFrame>
		);
	}
	if (path.startsWith("/join/")) return <Outlet />;
	if (session.data.setupRequired) return <SignUp session={session.data} />;
	if (!session.data.user) return <SignIn session={session.data} />;
	return <Layout />;
}

/** The loading screen: the only place for the tagline. */
export function Splash() {
	return (
		<div className="grid min-h-dvh place-items-center bg-bg px-4">
			<div className="flex flex-col items-center gap-5">
				<Logo />
				<p className="text-sm text-muted">Breathe out. Your subscriptions are handled.</p>
				<Loader2 className="size-4 animate-spin text-faint" aria-label="Loading" />
			</div>
		</div>
	);
}

export function AuthFrame({
	title,
	subtitle,
	children,
}: {
	title: string;
	subtitle?: ReactNode;
	children: ReactNode;
}) {
	return (
		<div className="relative grid min-h-dvh place-items-center overflow-hidden bg-bg px-4 py-10">
			<div
				aria-hidden
				className="bg-brand pointer-events-none absolute -top-48 left-1/2 h-96 w-[40rem] -translate-x-1/2 rounded-full opacity-[0.12] blur-3xl"
			/>
			<div className="relative w-full max-w-[380px]">
				<div className="mb-10 flex justify-center">
					<Logo />
				</div>
				<h1 className="text-center text-2xl font-semibold tracking-tight">{title}</h1>
				{subtitle && <p className="mt-2 text-center text-sm text-muted">{subtitle}</p>}
				<div className="mt-8">{children}</div>
			</div>
		</div>
	);
}

export function errorText(e: unknown, fallback: string) {
	if (e instanceof ApiError) {
		const details = e.errors.map((x) => x.message).filter(Boolean);
		return details.length ? details.join(" · ") : e.detail;
	}
	return fallback;
}

export function FormError({ message }: { message?: string }) {
	if (!message) return null;
	return (
		<p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-[13px] text-bad">
			{message}
		</p>
	);
}

/** Why a single sign-on attempt came back with `?error=…` (internal/httpapi/oidc.go). */
const ssoErrors: Record<string, string> = {
	sso_failed: "The sign-in did not complete. Try again.",
	sso_expired: "That sign-in took too long. Try again.",
	sso_denied: "The sign-in was cancelled.",
	sso_provider: "The sign-in provider is not answering. Try again in a moment.",
	sso_config: "Single sign-on is not set up for this address. The server needs EXHALE_PUBLIC_URL.",
	sso_throttled: "Too many attempts. Wait a minute and try again.",
	sso_unverified: "Your provider has not verified your email, so it cannot be matched to your account here.",
	sso_email_taken: "Your email belongs to an account that signs in with another identity.",
	sso_no_account: "There is no account for you here yet. Ask the owner for an invite.",
	sso_invite: "This invite has expired or was already used. Ask for a new one.",
	sso_setup_code: "The setup code is wrong or expired.",
	sso_setup: "Could not create your account. Try again from this page.",
};

/** The message for the `?error=` a single sign-on attempt came back with; the URL is cleaned once read. */
function useSsoError() {
	const [message] = useState(() => {
		const code = new URLSearchParams(window.location.search).get("error");
		return code?.startsWith("sso_") ? (ssoErrors[code] ?? ssoErrors.sso_failed) : undefined;
	});
	useEffect(() => {
		if (!message) return;
		const url = new URL(window.location.href);
		url.searchParams.delete("error");
		window.history.replaceState(window.history.state, "", url);
	}, [message]);
	return message;
}

/** The page to come back to after signing in, when it is not the home page. */
function currentPath() {
	const url = new URL(window.location.href);
	url.searchParams.delete("error");
	const path = url.pathname + url.search;
	return path === "/" ? undefined : path;
}

/**
 * "Continue with <provider>": a full-page navigation, since the provider's
 * pages take over the tab. `params` travel with the attempt (invite, setup).
 */
function SsoButton({
	name,
	params,
	primary,
	before,
}: {
	name: string;
	params?: Record<string, string | undefined>;
	primary?: boolean;
	/** Return false to stay on the page (e.g. a required field is empty). */
	before?: () => boolean;
}) {
	const [leaving, setLeaving] = useState(false);
	// Coming back with the browser's Back button restores the page as it was left.
	useEffect(() => {
		const reset = () => setLeaving(false);
		window.addEventListener("pageshow", reset);
		return () => window.removeEventListener("pageshow", reset);
	}, []);
	const go = () => {
		if (before && !before()) return;
		const query = new URLSearchParams();
		for (const [k, v] of Object.entries(params ?? {})) if (v) query.set(k, v);
		setLeaving(true);
		window.location.assign(`/api/v1/auth/oidc/start?${query}`);
	};
	return (
		<Button
			variant={primary ? "primary" : "secondary"}
			className="h-10 w-full"
			onClick={go}
			disabled={leaving}
		>
			{leaving ? <Loader2 className="animate-spin" /> : null}
			Continue with {name}
			{primary && !leaving && <ArrowRight />}
		</Button>
	);
}

function Or() {
	return (
		<div className="flex items-center gap-3 text-xs text-faint">
			<span className="h-px flex-1 bg-line" />
			or
			<span className="h-px flex-1 bg-line" />
		</div>
	);
}

/**
 * First run: one step. Currency, time zone and language come from the
 * browser; the rest of the setup happens on the home screen.
 */
function SignUp({ session }: { session: AuthSession }) {
	const qc = useQueryClient();
	const navigate = useNavigate();
	const setup = useAuthSetup();
	const ssoError = useSsoError();
	const [error, setError] = useState<string>();
	const [v, setV] = useState({ code: "", name: "", email: "", password: "" });
	const passwords = !session.passwordLoginDisabled;
	const sso = session.sso && (
		<SsoButton
			name={session.sso.name}
			primary={!passwords}
			params={{
				setup_code: session.setupNeedsCode ? v.code.trim() : undefined,
				currency: guessCurrency(),
				timezone: browserTimezone(),
				locale: browserLocale(),
			}}
			before={() => {
				if (!session.setupNeedsCode || v.code.trim()) return true;
				setError("Enter the setup code first.");
				return false;
			}}
		/>
	);
	const set = (k: keyof typeof v) => (e: { target: { value: string } }) =>
		setV({ ...v, [k]: e.target.value });

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		if (!passwords) return;
		if (v.password.length < 10) {
			setError("Use at least 10 characters for the password.");
			return;
		}
		setError(undefined);
		try {
			const s = await setup.mutateAsync({
				data: {
					name: v.name,
					email: v.email,
					password: v.password,
					code: session.setupNeedsCode ? v.code.trim() : undefined,
					currency: guessCurrency(),
					timezone: browserTimezone(),
					locale: browserLocale(),
				},
			});
			qc.setQueryData(getAuthSessionQueryKey(), s);
			navigate({ to: "/" });
		} catch (err) {
			setError(errorText(err, "Could not create your account"));
		}
	};

	return (
		<AuthFrame
			title={session.hasHousehold ? "Create your login" : "Welcome to Exhale"}
			subtitle={
				session.hasHousehold
					? "Your data is already here. Create the login to open it."
					: "Create your account. The rest happens inside."
			}
		>
			<form onSubmit={onSubmit} className="grid gap-4">
				{session.setupNeedsCode && (
					<Field
						label="Setup code"
						hint={
							<>
								Printed in the server log, or run <code className="font-mono">exhale setup-code</code>.
							</>
						}
					>
						<Input
							value={v.code}
							onChange={set("code")}
							required
							autoComplete="one-time-code"
							className="font-mono"
						/>
					</Field>
				)}
				{passwords && (
					<>
						<Field label="Name">
							<Input
								value={v.name}
								onChange={set("name")}
								required
								autoComplete="name"
								maxLength={80}
								autoFocus
							/>
						</Field>
						<Field label="Email">
							<Input type="email" value={v.email} onChange={set("email")} required autoComplete="email" />
						</Field>
						<Field label="Password" hint="At least 10 characters.">
							<PasswordInput
								value={v.password}
								onChange={set("password")}
								required
								minLength={10}
								autoComplete="new-password"
							/>
						</Field>
					</>
				)}
				<FormError message={error ?? ssoError} />
				{passwords && (
					<Button type="submit" variant="primary" className="mt-2 h-10" disabled={setup.isPending}>
						{setup.isPending ? <Loader2 className="animate-spin" /> : null}
						Create account
						{!setup.isPending && <ArrowRight />}
					</Button>
				)}
				{passwords && sso && <Or />}
				{sso}
			</form>
		</AuthFrame>
	);
}

function SignIn({ session }: { session: AuthSession }) {
	const qc = useQueryClient();
	const login = useAuthLogin();
	const ssoError = useSsoError();
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [error, setError] = useState<string>();
	const sso = session.sso && (
		<SsoButton
			name={session.sso.name}
			primary={session.passwordLoginDisabled}
			params={{ next: currentPath() }}
		/>
	);
	if (session.passwordLoginDisabled) {
		return (
			<AuthFrame title="Sign in">
				<div className="grid gap-4">
					<FormError message={ssoError} />
					{sso}
				</div>
			</AuthFrame>
		);
	}
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			applySession(qc, await login.mutateAsync({ data: { email, password } }));
		} catch (err) {
			setError(errorText(err, "Could not sign in"));
		}
	};
	return (
		<AuthFrame title="Sign in">
			<form onSubmit={onSubmit} className="grid gap-4">
				<Field label="Email">
					<Input
						type="email"
						value={email}
						onChange={(e) => setEmail(e.target.value)}
						required
						autoComplete="username"
						autoFocus
					/>
				</Field>
				<Field label="Password">
					<PasswordInput
						value={password}
						onChange={(e) => setPassword(e.target.value)}
						required
						autoComplete="current-password"
					/>
				</Field>
				<FormError message={error ?? ssoError} />
				<Button type="submit" variant="primary" className="mt-2 h-10" disabled={login.isPending}>
					{login.isPending ? <Loader2 className="animate-spin" /> : null}
					Sign in
				</Button>
				{sso && <Or />}
				{sso}
				<p className="text-center text-xs text-faint">
					Forgot it? On the server: <code className="font-mono">exhale user reset-password --email …</code>
				</p>
			</form>
		</AuthFrame>
	);
}

/** /join/$code: accept an invite and create your account. */
export function JoinPage() {
	const { code } = useParams({ from: "/join/$code" });
	const qc = useQueryClient();
	const navigate = useNavigate();
	const invite = useAuthGetInvite(code, { query: { retry: false } });
	const session = useAuthSession({ query: { staleTime: Number.POSITIVE_INFINITY } }).data;
	const join = useAuthJoin();
	const ssoError = useSsoError();
	const [v, setV] = useState({ name: "", email: "", password: "" });
	const [error, setError] = useState<string>();
	if (invite.isPending) return <Splash />;
	if (invite.isError) {
		return (
			<AuthFrame
				title="This invite does not work"
				subtitle="It has expired or was already used. Ask for a new one."
			>
				<Button className="w-full" onClick={() => navigate({ to: "/" })}>
					Go to sign in
				</Button>
			</AuthFrame>
		);
	}
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			applySession(qc, await join.mutateAsync({ data: { code, ...v } }));
			navigate({ to: "/" });
		} catch (err) {
			setError(errorText(err, "Could not create your account"));
		}
	};
	const subtitle = `${invite.data.invitedBy} wants to share their Exhale with you. You get your own login; private subscriptions stay private.`;
	const sso = session?.sso && (
		<SsoButton name={session.sso.name} primary={session.passwordLoginDisabled} params={{ invite: code }} />
	);
	if (session?.passwordLoginDisabled) {
		return (
			<AuthFrame title="You're invited" subtitle={subtitle}>
				<div className="grid gap-4">
					<FormError message={ssoError} />
					{sso}
				</div>
			</AuthFrame>
		);
	}
	return (
		<AuthFrame title="You're invited" subtitle={subtitle}>
			<form onSubmit={onSubmit} className="grid gap-4">
				<Field label="Name">
					<Input
						value={v.name}
						onChange={(e) => setV({ ...v, name: e.target.value })}
						required
						autoComplete="name"
						autoFocus
					/>
				</Field>
				<Field label="Email">
					<Input
						type="email"
						value={v.email}
						onChange={(e) => setV({ ...v, email: e.target.value })}
						required
						autoComplete="email"
					/>
				</Field>
				<Field label="Password" hint="At least 10 characters.">
					<PasswordInput
						value={v.password}
						onChange={(e) => setV({ ...v, password: e.target.value })}
						required
						minLength={10}
						autoComplete="new-password"
					/>
				</Field>
				<FormError message={error ?? ssoError} />
				<Button type="submit" variant="primary" className="mt-2 h-10" disabled={join.isPending}>
					Create account
				</Button>
				{sso && <Or />}
				{sso}
			</form>
		</AuthFrame>
	);
}
