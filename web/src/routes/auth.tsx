import { useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, Loader2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { Layout, Logo } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/field";
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
import { applySession } from "@/lib/session";

/**
 * Root of the web UI (ADR 0006): the setup wizard on a fresh instance, the
 * sign-in page without a session, invite pages for anyone, and the app once
 * signed in.
 */
export function Root() {
	const session = useAuthSession({ query: { staleTime: Number.POSITIVE_INFINITY, retry: 2 } });
	const path = useRouterState({ select: (s) => s.location.pathname });
	if (session.isPending) return <Splash />;
	if (session.isError) {
		return (
			<AuthFrame title="Exhale is not answering" subtitle="Check that the server is running, then try again.">
				<Button variant="primary" onClick={() => session.refetch()}>
					Try again
				</Button>
			</AuthFrame>
		);
	}
	if (path.startsWith("/join/")) return <Outlet />;
	if (session.data.setupRequired) return <SetupWizard session={session.data} />;
	if (!session.data.user) return <SignIn />;
	return <Layout />;
}

function Splash() {
	return (
		<div className="grid min-h-dvh place-items-center">
			<Loader2 className="size-5 animate-spin text-muted" aria-label="Loading" />
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
		<div className="grid min-h-dvh place-items-center bg-bg px-4 py-10">
			<div className="w-full max-w-[400px]">
				<div className="mb-8 flex justify-center">
					<Logo />
				</div>
				<div className="rounded-2xl border border-line bg-surface p-6 shadow-sm sm:p-8">
					<h1 className="text-xl font-semibold tracking-tight">{title}</h1>
					{subtitle && <p className="mt-1.5 text-sm text-muted">{subtitle}</p>}
					<div className="mt-6">{children}</div>
				</div>
				<p className="mt-6 text-center text-xs text-faint">Breathe out. Your subscriptions are handled.</p>
			</div>
		</div>
	);
}

function errorText(e: unknown, fallback: string) {
	if (e instanceof ApiError) {
		const details = e.errors.map((x) => x.message).filter(Boolean);
		return details.length ? details.join(" · ") : e.detail;
	}
	return fallback;
}

function FormError({ message }: { message?: string }) {
	if (!message) return null;
	return (
		<p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-[13px] text-bad">
			{message}
		</p>
	);
}

const currencies = [
	"EUR",
	"USD",
	"GBP",
	"CHF",
	"MXN",
	"ARS",
	"COP",
	"CLP",
	"BRL",
	"CAD",
	"AUD",
	"JPY",
	"SEK",
	"NOK",
	"DKK",
	"PLN",
];

function browserTimezone() {
	try {
		return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
	} catch {
		return "UTC";
	}
}

function browserLocale() {
	return navigator.language?.toLowerCase().startsWith("es") ? "es" : "en";
}

function guessCurrency() {
	const tz = browserTimezone();
	if (tz.startsWith("Europe/London")) return "GBP";
	if (tz.startsWith("Europe/Zurich")) return "CHF";
	if (tz.startsWith("Europe/")) return "EUR";
	if (tz === "America/Mexico_City") return "MXN";
	if (tz.startsWith("America/Argentina")) return "ARS";
	if (tz === "America/Bogota") return "COP";
	if (tz === "America/Santiago") return "CLP";
	if (tz === "America/Sao_Paulo") return "BRL";
	if (tz.startsWith("America/")) return "USD";
	return "EUR";
}

function timezones(): string[] {
	try {
		return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf("timeZone");
	} catch {
		return [browserTimezone()];
	}
}

/** First run: create your account, then your household. */
function SetupWizard({ session }: { session: AuthSession }) {
	const qc = useQueryClient();
	const navigate = useNavigate();
	const setup = useAuthSetup();
	const [step, setStep] = useState<"account" | "household">("account");
	const [error, setError] = useState<string>();
	const [v, setV] = useState({
		code: "",
		name: "",
		email: "",
		password: "",
		householdName: "Home",
		currency: guessCurrency(),
		timezone: browserTimezone(),
		locale: browserLocale(),
	});
	const set = (k: keyof typeof v) => (e: { target: { value: string } }) =>
		setV({ ...v, [k]: e.target.value });

	const submit = async () => {
		setError(undefined);
		try {
			const s = await setup.mutateAsync({
				data: { ...v, code: session.setupNeedsCode ? v.code.trim() : undefined },
			});
			qc.setQueryData(getAuthSessionQueryKey(), s);
			navigate({ to: "/welcome" });
		} catch (e) {
			setError(errorText(e, "Could not create your account"));
		}
	};

	const onAccount = (e: FormEvent) => {
		e.preventDefault();
		if (v.password.length < 10) {
			setError("Use at least 10 characters for the password.");
			return;
		}
		setError(undefined);
		if (session.hasHousehold) submit();
		else setStep("household");
	};
	const onHousehold = (e: FormEvent) => {
		e.preventDefault();
		submit();
	};

	if (step === "household") {
		return (
			<AuthFrame title="Your household" subtitle="Step 2 of 2 · You can change all of this later.">
				<form onSubmit={onHousehold} className="grid gap-4">
					<Field label="Household name" hint="Shown in reminders and to the people you invite.">
						<Input value={v.householdName} onChange={set("householdName")} required maxLength={60} />
					</Field>
					<div className="grid grid-cols-2 gap-3">
						<Field label="Main currency" hint="Totals are shown in it.">
							<Select value={v.currency} onChange={set("currency")}>
								{[...new Set([v.currency, ...currencies])].map((c) => (
									<option key={c}>{c}</option>
								))}
							</Select>
						</Field>
						<Field label="Language" hint="For reminders.">
							<Select value={v.locale} onChange={set("locale")}>
								<option value="en">English</option>
								<option value="es">Español</option>
							</Select>
						</Field>
					</div>
					<Field label="Time zone" hint="Reminders arrive at 09:00 in this zone.">
						<Select value={v.timezone} onChange={set("timezone")}>
							{timezones().map((z) => (
								<option key={z}>{z}</option>
							))}
						</Select>
					</Field>
					<FormError message={error} />
					<div className="mt-2 flex items-center justify-between gap-3">
						<Button variant="ghost" onClick={() => setStep("account")}>
							<ArrowLeft />
							Back
						</Button>
						<Button type="submit" variant="primary" disabled={setup.isPending}>
							{setup.isPending ? <Loader2 className="animate-spin" /> : null}
							Create household
						</Button>
					</div>
				</form>
			</AuthFrame>
		);
	}

	return (
		<AuthFrame
			title="Welcome to Exhale"
			subtitle={
				session.hasHousehold
					? "Your household is ready. Create the owner's login to open it."
					: "Step 1 of 2 · Create your account. You will be the owner of this household."
			}
		>
			<form onSubmit={onAccount} className="grid gap-4">
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
				<Field label="Your name">
					<Input
						value={v.name}
						onChange={set("name")}
						required
						autoComplete="name"
						maxLength={80}
						autoFocus
					/>
				</Field>
				<Field label="Email" hint="You sign in with it. Nothing is ever sent to it.">
					<Input type="email" value={v.email} onChange={set("email")} required autoComplete="email" />
				</Field>
				<Field label="Password" hint="At least 10 characters. A short phrase works well.">
					<Input
						type="password"
						value={v.password}
						onChange={set("password")}
						required
						minLength={10}
						autoComplete="new-password"
					/>
				</Field>
				<FormError message={error} />
				<Button type="submit" variant="primary" className="mt-2" disabled={setup.isPending}>
					{session.hasHousehold ? "Create login" : "Continue"}
					<ArrowRight />
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
			const s = await login.mutateAsync({ data: { email, password } });
			applySession(qc, s);
		} catch (err) {
			setError(errorText(err, "Could not sign in"));
		}
	};
	return (
		<AuthFrame title="Sign in" subtitle="Welcome back.">
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
				<Field
					label="Password"
					hint={
						<>
							Forgot it? On the server run{" "}
							<code className="font-mono">exhale user reset-password --email …</code>
						</>
					}
				>
					<Input
						type="password"
						value={password}
						onChange={(e) => setPassword(e.target.value)}
						required
						autoComplete="current-password"
					/>
				</Field>
				<FormError message={error} />
				<Button type="submit" variant="primary" className="mt-2" disabled={login.isPending}>
					{login.isPending ? <Loader2 className="animate-spin" /> : null}
					Sign in
				</Button>
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
				<Button onClick={() => navigate({ to: "/" })}>Go to sign in</Button>
			</AuthFrame>
		);
	}
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			const s = await join.mutateAsync({ data: { code, ...v } });
			applySession(qc, s);
			navigate({ to: "/welcome" });
		} catch (err) {
			setError(errorText(err, "Could not create your account"));
		}
	};
	return (
		<AuthFrame
			title={`Join ${invite.data.householdName}`}
			subtitle={`${invite.data.invitedBy} invited you as ${invite.data.role === "owner" ? "an owner" : "a member"}.`}
		>
			<form onSubmit={onSubmit} className="grid gap-4">
				<Field label="Your name">
					<Input
						value={v.name}
						onChange={(e) => setV({ ...v, name: e.target.value })}
						required
						autoComplete="name"
						autoFocus
					/>
				</Field>
				<Field label="Email" hint="You sign in with it.">
					<Input
						type="email"
						value={v.email}
						onChange={(e) => setV({ ...v, email: e.target.value })}
						required
						autoComplete="email"
					/>
				</Field>
				<Field label="Password" hint="At least 10 characters.">
					<Input
						type="password"
						value={v.password}
						onChange={(e) => setV({ ...v, password: e.target.value })}
						required
						minLength={10}
						autoComplete="new-password"
					/>
				</Field>
				<FormError message={error} />
				<Button type="submit" variant="primary" className="mt-2" disabled={join.isPending}>
					Create account
				</Button>
			</form>
		</AuthFrame>
	);
}
