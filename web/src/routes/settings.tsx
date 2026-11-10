import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";
import { AssistantSettings } from "@/components/assistant";
import { OtherChannels } from "@/components/channels";
import { PreferencesForm, ReminderTime, Sharing, TelegramSetup, TokensManager } from "@/components/connect";
import { CalendarLink, ExportData, ImportData } from "@/components/data";
import { PageHeader } from "@/components/layout";
import { Categories, Payday, PaymentMethods } from "@/components/manage";
import { SignOut } from "@/components/sign-out";
import { SlackSetup } from "@/components/slack";
import { TwoFactorSettings } from "@/components/two-factor";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Field, PasswordInput } from "@/components/ui/field";
import { useAuthChangePassword, useAuthSession } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const tabs = [
	{ id: "you", label: t("You") },
	{ id: "general", label: t("General") },
	{ id: "channels", label: t("Channels") },
	{ id: "sharing", label: t("Sharing") },
	{ id: "data", label: t("Data") },
	{ id: "agents", label: t("Agents") },
] as const;
export type SettingsTab = (typeof tabs)[number]["id"];

export function SettingsPage() {
	const session = useAuthSession();
	const user = session.data?.user;
	const search = useSearch({ from: "/settings" });
	const navigate = useNavigate({ from: "/settings" });
	const tab: SettingsTab = tabs.some((x) => x.id === search.tab) ? (search.tab as SettingsTab) : "you";
	return (
		<div className="mx-auto max-w-[760px]">
			<PageHeader title={t("Settings")} />
			<div
				className="mb-5 flex gap-1 overflow-x-auto rounded-lg bg-raised p-1"
				role="tablist"
				aria-label={t("Settings")}
			>
				{tabs.map((x) => (
					<button
						key={x.id}
						type="button"
						role="tab"
						aria-selected={tab === x.id}
						onClick={() => navigate({ search: { tab: x.id }, replace: true })}
						className={cn(
							"flex-1 rounded-md px-3 py-1.5 text-[13px] font-medium whitespace-nowrap text-muted transition-colors",
							tab === x.id && "bg-surface text-fg shadow-sm",
						)}
					>
						{x.label}
					</button>
				))}
			</div>
			<div className="grid gap-5">
				{tab === "you" && (
					<>
						<Section
							title={t("Account")}
							description={user ? `${user.name} · ${user.email}` : undefined}
							action={<SignOut />}
						>
							<AccountLogin />
						</Section>
						{user?.hasPassword && !session.data?.passwordLoginDisabled && (
							<Section
								title={t("Two-factor sign-in")}
								description={t("After your password, a code from an authenticator app.")}
							>
								<TwoFactorSettings />
							</Section>
						)}
						<p className="flex gap-4 text-[13px] md:hidden">
							<Link to="/activity" className="font-medium hover:underline">
								{t("Activity")}
							</Link>
							<Link to="/agents" className="font-medium hover:underline">
								{t("AI agents")}
							</Link>
						</p>
					</>
				)}
				{tab === "general" && (
					<>
						<Section
							title={t("Preferences")}
							description={t("Owners can change them; they apply to everyone you share with.")}
						>
							<div className="grid gap-5">
								<PreferencesForm />
								<ReminderTime withTimezone={false} />
								<Payday />
							</div>
						</Section>
						<Section
							title={t("Payment methods")}
							description={t("The cards and accounts that pay your subscriptions.")}
						>
							<PaymentMethods />
						</Section>
						<Section title={t("Categories")}>
							<Categories />
						</Section>
					</>
				)}
				{tab === "channels" && (
					<>
						<Section
							title="Telegram"
							description={t("Reminders you act on, and the whole app inside one message.")}
						>
							<TelegramSetup />
						</Section>
						<Section
							title="Slack"
							description={t("For a team: renewals in a channel, and the same app in Slack.")}
						>
							<SlackSetup />
						</Section>
						<Section
							title={t("Other channels")}
							description={t("Reminders by email, ntfy or webhook, with a link back here.")}
						>
							<OtherChannels />
						</Section>
					</>
				)}
				{tab === "sharing" && (
					<Section
						title={t("Sharing")}
						description={t("Everyone gets their own login and their own reminders.")}
					>
						<Sharing />
					</Section>
				)}
				{tab === "data" && (
					<>
						<Section title={t("Calendar")}>
							<CalendarLink />
						</Section>
						<Section title={t("Import")}>
							<ImportData />
						</Section>
						<Section title={t("Export")}>
							<ExportData />
						</Section>
					</>
				)}
				{tab === "agents" && (
					<Section
						title={t("API tokens")}
						description={t(
							"For Claude and other AI agents over MCP, and for scripts. Each token acts as you.",
						)}
					>
						<TokensManager />
					</Section>
				)}
				{tab === "agents" && (
					<Section
						title={t("Built-in assistant")}
						description={t("Ask in plain language, here and in chat. Bring your own model.")}
					>
						<AssistantSettings />
					</Section>
				)}
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

/** How this account signs in: the linked identity provider, the password, or both. */
function AccountLogin() {
	const session = useAuthSession();
	const user = session.data?.user;
	const password = user?.hasPassword && !session.data?.passwordLoginDisabled;
	return (
		<div className="grid gap-4">
			{user?.ssoHost && (
				<p className="text-sm text-muted">
					{t("Single sign-on ·")} <span className="font-mono text-[13px] text-fg">{user.ssoHost}</span>
				</p>
			)}
			{password && <ChangePassword />}
			{!password && !user?.ssoHost && (
				<p className="text-sm text-muted">{t("Password sign-in is turned off on this server.")}</p>
			)}
		</div>
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
			toast.success(t("Password changed. Your other sessions were signed out."));
		} catch (err) {
			setError(err instanceof ApiError ? err.detail : t("Could not change the password"));
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
			<Field label={t("Current password")}>
				<PasswordInput
					value={current}
					onChange={(e) => setCurrent(e.target.value)}
					required
					autoComplete="current-password"
				/>
			</Field>
			<Field label={t("New password")}>
				<PasswordInput
					value={next}
					onChange={(e) => setNext(e.target.value)}
					required
					minLength={10}
					autoComplete="new-password"
				/>
			</Field>
			<Button type="submit" disabled={change.isPending}>
				{t("Change password")}
			</Button>
			{error && <p className="text-xs text-bad sm:col-span-3">{error}</p>}
		</form>
	);
}
