import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";
import { OtherChannels } from "@/components/channels";
import { PreferencesForm, ReminderTime, Sharing, TelegramSetup, TokensManager } from "@/components/connect";
import { PageHeader } from "@/components/layout";
import { SignOut } from "@/components/sign-out";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Field, PasswordInput } from "@/components/ui/field";
import { useAuthChangePassword, useAuthSession } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";

export function SettingsPage() {
	const session = useAuthSession();
	const user = session.data?.user;
	return (
		<div className="mx-auto max-w-[760px]">
			<PageHeader title="Settings" />
			<div className="grid gap-5">
				<Section
					title="Account"
					description={user ? `${user.name} · ${user.email}` : undefined}
					action={<SignOut />}
				>
					<ChangePassword />
				</Section>
				<Section
					title="Preferences"
					description="Owners can change them; they apply to everyone you share with."
				>
					<div className="grid gap-5">
						<PreferencesForm />
						<ReminderTime withTimezone={false} />
					</div>
				</Section>
				<Section title="Telegram" description="Reminders you act on, and the whole app inside one message.">
					<TelegramSetup />
				</Section>
				<Section
					title="Other channels"
					description="Reminders by email, ntfy or webhook, with a link back here."
				>
					<OtherChannels />
				</Section>
				<Section title="Sharing">
					<Sharing />
				</Section>
				<Section
					title="API tokens"
					description="For Claude and other AI agents over MCP, and for scripts. Each token acts as you."
				>
					<TokensManager />
				</Section>
			</div>
		</div>
	);
}

function Section({
	title,
	description,
	action,
	children,
}: {
	title: string;
	description?: string;
	action?: ReactNode;
	children: ReactNode;
}) {
	return (
		<Card>
			<CardHeader title={title} description={description} action={action} />
			<div className="border-t border-line px-5 py-4">{children}</div>
		</Card>
	);
}

function ChangePassword() {
	const change = useAuthChangePassword();
	const [current, setCurrent] = useState("");
	const [next, setNext] = useState("");
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			await change.mutateAsync({ data: { current, new: next } });
			setCurrent("");
			setNext("");
			toast.success("Password changed. Your other sessions were signed out.");
		} catch (err) {
			setError(err instanceof ApiError ? err.detail : "Could not change the password");
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
			<Field label="Current password">
				<PasswordInput
					value={current}
					onChange={(e) => setCurrent(e.target.value)}
					required
					autoComplete="current-password"
				/>
			</Field>
			<Field label="New password">
				<PasswordInput
					value={next}
					onChange={(e) => setNext(e.target.value)}
					required
					minLength={10}
					autoComplete="new-password"
				/>
			</Field>
			<Button type="submit" disabled={change.isPending}>
				Change password
			</Button>
			{error && <p className="text-xs text-bad sm:col-span-3">{error}</p>}
		</form>
	);
}
