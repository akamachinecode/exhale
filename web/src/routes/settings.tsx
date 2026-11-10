import { Link } from "@tanstack/react-router";
import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";
import { AssistantSettings } from "@/components/assistant";
import { PreferencesForm, ReminderTime, Sharing, TelegramSetup, TokensManager } from "@/components/connect";
import { CalendarLink, ExportData, ImportData } from "@/components/data";
import { PageHeader } from "@/components/layout";
import { Categories, Payday, PaymentMethods } from "@/components/manage";
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
			<p className="mb-5 flex gap-4 text-[13px] md:hidden">
				<Link to="/activity" className="font-medium hover:underline">
					Activity
				</Link>
				<Link to="/agents" className="font-medium hover:underline">
					AI agents
				</Link>
			</p>
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
						<Payday />
					</div>
				</Section>
				<Section title="Payment methods" description="The cards and accounts that pay your subscriptions.">
					<PaymentMethods />
				</Section>
				<Section title="Categories">
					<Categories />
				</Section>
				<Section title="Telegram" description="Reminders you act on, and the whole app inside one message.">
					<TelegramSetup />
				</Section>
				<Section
					title="Built-in assistant"
					description="Ask in plain language, here and in chat. Bring your own model."
				>
					<AssistantSettings />
				</Section>
				<Section title="Sharing">
					<Sharing />
				</Section>
				<Section title="Calendar">
					<CalendarLink />
				</Section>
				<Section title="Import">
					<ImportData />
				</Section>
				<Section title="Export">
					<ExportData />
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
