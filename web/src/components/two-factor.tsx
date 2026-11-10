import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { CodeBlock, QrCode } from "@/components/connect";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, PasswordInput } from "@/components/ui/field";
import {
	type TwoFactorSetup,
	useAuthTwoFactor,
	useAuthTwoFactorBackupCodes,
	useAuthTwoFactorDisable,
	useAuthTwoFactorEnable,
	useAuthTwoFactorSetup,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t, tn } from "@/lib/i18n";

// Two-factor sign-in (ADR 0025): enrol with an authenticator app, keep the
// backup codes, turn it off. Only password sign-in asks for the code.

type Step =
	| { kind: "idle" }
	| { kind: "password"; next: "setup" | "codes" }
	| { kind: "scan"; setup: TwoFactorSetup }
	| { kind: "codes"; codes: string[] }
	| { kind: "disable" };

function errText(e: unknown, fallback: string) {
	return e instanceof ApiError ? e.detail : fallback;
}

export function TwoFactorSettings() {
	const qc = useQueryClient();
	const status = useAuthTwoFactor();
	const [step, setStep] = useState<Step>({ kind: "idle" });
	const refresh = () => qc.invalidateQueries({ queryKey: status.queryKey });
	if (status.isPending) return null;
	const on = status.data?.enabled ?? false;

	if (step.kind === "password") {
		return (
			<PasswordStep
				next={step.next}
				onCancel={() => setStep({ kind: "idle" })}
				onSetup={(setup) => setStep({ kind: "scan", setup })}
				onCodes={(codes) => {
					refresh();
					setStep({ kind: "codes", codes });
				}}
			/>
		);
	}
	if (step.kind === "scan") {
		return (
			<ScanStep
				setup={step.setup}
				onCancel={() => setStep({ kind: "idle" })}
				onDone={(codes) => {
					refresh();
					setStep({ kind: "codes", codes });
				}}
			/>
		);
	}
	if (step.kind === "codes") {
		return <BackupCodes codes={step.codes} onDone={() => setStep({ kind: "idle" })} />;
	}
	if (step.kind === "disable") {
		return (
			<DisableStep
				onCancel={() => setStep({ kind: "idle" })}
				onDone={() => {
					refresh();
					setStep({ kind: "idle" });
				}}
			/>
		);
	}
	const left = status.data?.backupCodesLeft ?? 0;
	return (
		<div className="flex flex-wrap items-center justify-between gap-3">
			<p className="flex min-w-0 items-center gap-2 text-sm">
				{on ? <Badge tone="ok">{t("On")}</Badge> : <Badge>{t("Off")}</Badge>}
				{on && (
					<span className="text-muted">{tn(left, "{n} backup code left", "{n} backup codes left")}</span>
				)}
			</p>
			{on ? (
				<div className="flex gap-2">
					<Button size="sm" onClick={() => setStep({ kind: "password", next: "codes" })}>
						{t("New backup codes")}
					</Button>
					<Button size="sm" variant="danger" onClick={() => setStep({ kind: "disable" })}>
						{t("Turn off")}
					</Button>
				</div>
			) : (
				<Button size="sm" onClick={() => setStep({ kind: "password", next: "setup" })}>
					{t("Turn on")}
				</Button>
			)}
		</div>
	);
}

/** The password again before changing how the account signs in. */
function PasswordStep({
	next,
	onCancel,
	onSetup,
	onCodes,
}: {
	next: "setup" | "codes";
	onCancel: () => void;
	onSetup: (s: TwoFactorSetup) => void;
	onCodes: (codes: string[]) => void;
}) {
	const setup = useAuthTwoFactorSetup();
	const codes = useAuthTwoFactorBackupCodes();
	const [password, setPassword] = useState("");
	const [error, setError] = useState<string>();
	const busy = setup.isPending || codes.isPending;
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			if (next === "setup") onSetup(await setup.mutateAsync({ data: { password } }));
			else onCodes((await codes.mutateAsync({ data: { password } })).backupCodes ?? []);
		} catch (err) {
			setError(errText(err, t("Could not save")));
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end">
			<Field
				label={t("Your password")}
				hint={next === "codes" ? t("Your current backup codes stop working.") : undefined}
			>
				<PasswordInput
					value={password}
					onChange={(e) => setPassword(e.target.value)}
					required
					autoFocus
					autoComplete="current-password"
				/>
			</Field>
			<Button type="submit" variant="primary" disabled={busy}>
				{next === "setup" ? t("Continue") : t("New backup codes")}
			</Button>
			<Button onClick={onCancel}>{t("Cancel")}</Button>
			{error && <p className="text-xs text-bad sm:col-span-3">{error}</p>}
		</form>
	);
}

function ScanStep({
	setup,
	onCancel,
	onDone,
}: {
	setup: TwoFactorSetup;
	onCancel: () => void;
	onDone: (codes: string[]) => void;
}) {
	const enable = useAuthTwoFactorEnable();
	const [code, setCode] = useState("");
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			onDone((await enable.mutateAsync({ data: { code } })).backupCodes ?? []);
			toast.success(t("Two-factor sign-in is on"));
		} catch (err) {
			setError(errText(err, t("Could not save")));
		}
	};
	return (
		<div className="grid gap-4">
			<div className="flex flex-wrap items-start gap-5">
				<QrCode value={setup.uri} size={152} label={t("QR code for your authenticator app")} />
				<div className="grid min-w-0 flex-1 gap-2 text-sm">
					<p>
						{t(
							"Scan it with an authenticator app (1Password, Bitwarden, Google Authenticator, Aegis…). Or type this key:",
						)}
					</p>
					<CodeBlock code={setup.secret} />
				</div>
			</div>
			<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end">
				<Field label={t("Code from the app")}>
					<Input
						value={code}
						onChange={(e) => setCode(e.target.value)}
						required
						autoFocus
						inputMode="numeric"
						autoComplete="one-time-code"
						maxLength={8}
						className="font-mono"
					/>
				</Field>
				<Button type="submit" variant="primary" disabled={enable.isPending}>
					{t("Turn on")}
				</Button>
				<Button onClick={onCancel}>{t("Cancel")}</Button>
				{error && <p className="text-xs text-bad sm:col-span-3">{error}</p>}
			</form>
		</div>
	);
}

/** Shown once: after turning it on, and after asking for new ones. */
function BackupCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
	return (
		<div className="grid gap-3">
			<p className="text-sm font-medium">{t("Save your backup codes")}</p>
			<p className="text-[13px] text-muted">
				{t(
					"If your phone is not at hand, each of these signs you in once. Keep them somewhere safe, like your password manager: they are not shown again.",
				)}
			</p>
			<CodeBlock code={codes.join("\n")} className="sm:max-w-sm" />
			<div>
				<Button variant="primary" size="sm" onClick={onDone}>
					{t("I saved them")}
				</Button>
			</div>
		</div>
	);
}

function DisableStep({ onCancel, onDone }: { onCancel: () => void; onDone: () => void }) {
	const disable = useAuthTwoFactorDisable();
	const [password, setPassword] = useState("");
	const [code, setCode] = useState("");
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			await disable.mutateAsync({ data: { password, code } });
			toast.success(t("Two-factor sign-in is off"));
			onDone();
		} catch (err) {
			setError(errText(err, t("Could not save")));
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-end">
			<Field label={t("Your password")}>
				<PasswordInput
					value={password}
					onChange={(e) => setPassword(e.target.value)}
					required
					autoFocus
					autoComplete="current-password"
				/>
			</Field>
			<Field label={t("Code or backup code")}>
				<Input
					value={code}
					onChange={(e) => setCode(e.target.value)}
					required
					autoComplete="one-time-code"
					maxLength={32}
					className="font-mono"
				/>
			</Field>
			<Button type="submit" variant="danger" disabled={disable.isPending}>
				{t("Turn off")}
			</Button>
			<Button onClick={onCancel}>{t("Cancel")}</Button>
			{error && <p className="text-xs text-bad sm:col-span-4">{error}</p>}
		</form>
	);
}
