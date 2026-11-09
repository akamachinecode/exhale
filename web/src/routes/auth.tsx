import { useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { ArrowRight, Loader2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
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
	if (!session.data.user) return <SignIn />;
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

/**
 * First run: one step. Currency, time zone and language come from the
 * browser; the rest of the setup happens on the home screen.
 */
function SignUp({ session }: { session: AuthSession }) {
	const qc = useQueryClient();
	const navigate = useNavigate();
	const setup = useAuthSetup();
	const [error, setError] = useState<string>();
	const [v, setV] = useState({ code: "", name: "", email: "", password: "" });
	const set = (k: keyof typeof v) => (e: { target: { value: string } }) =>
		setV({ ...v, [k]: e.target.value });

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
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
				<FormError message={error} />
				<Button type="submit" variant="primary" className="mt-2 h-10" disabled={setup.isPending}>
					{setup.isPending ? <Loader2 className="animate-spin" /> : null}
					Create account
					{!setup.isPending && <ArrowRight />}
				</Button>
			</form>
		</AuthFrame>
	);
}

function SignIn() {
	const qc = useQueryClient();
	const login = useAuthLogin();
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [error, setError] = useState<string>();
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
				<FormError message={error} />
				<Button type="submit" variant="primary" className="mt-2 h-10" disabled={login.isPending}>
					{login.isPending ? <Loader2 className="animate-spin" /> : null}
					Sign in
				</Button>
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
	const join = useAuthJoin();
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
	return (
		<AuthFrame
			title="You're invited"
			subtitle={`${invite.data.invitedBy} wants to share their Exhale with you. You get your own login; private subscriptions stay private.`}
		>
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
				<FormError message={error} />
				<Button type="submit" variant="primary" className="mt-2 h-10" disabled={join.isPending}>
					Create account
				</Button>
			</form>
		</AuthFrame>
	);
}
