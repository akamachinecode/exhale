import { UserPlus } from "lucide-react";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { CodeBlock, TelegramConnect, TokensManager } from "@/components/connect";
import { PageHeader } from "@/components/layout";
import { SignOut } from "@/components/sign-out";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/field";
import { useAuthChangePassword, useAuthCreateInvite, useAuthSession, useGetHousehold } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";

export function SettingsPage() {
	const session = useAuthSession();
	const user = session.data?.user;
	return (
		<div className="mx-auto max-w-[760px]">
			<PageHeader
				title="Settings"
				description="Your account, your household and what is connected to Exhale."
			/>
			<div className="grid gap-5">
				<Card>
					<CardHeader
						title="Account"
						description={user ? `${user.name} · ${user.email}` : undefined}
						action={<SignOut />}
					/>
					<div className="border-t border-line px-5 py-4">
						<ChangePassword />
					</div>
				</Card>
				<Household isOwner={user?.role === "owner"} />
				<Card>
					<CardHeader
						title="Telegram"
						description="Reminders you can act on, and the whole app inside one message."
					/>
					<div className="border-t border-line px-5 py-4">
						<TelegramConnect />
					</div>
				</Card>
				<Card>
					<CardHeader
						title="API tokens"
						description="For Claude and other AI agents over MCP, and for scripts. Each token acts as you."
					/>
					<div className="border-t border-line px-5 py-4">
						<TokensManager />
					</div>
				</Card>
			</div>
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
			toast.success("Password changed. Your other sessions were signed out.");
		} catch (err) {
			setError(err instanceof ApiError ? err.detail : "Could not change the password");
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
			<Field label="Current password">
				<Input
					type="password"
					value={current}
					onChange={(e) => setCurrent(e.target.value)}
					required
					autoComplete="current-password"
				/>
			</Field>
			<Field label="New password">
				<Input
					type="password"
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

function Household({ isOwner }: { isOwner: boolean }) {
	const household = useGetHousehold();
	const invite = useAuthCreateInvite();
	const [link, setLink] = useState<string>();
	const h = household.data;
	return (
		<Card>
			<CardHeader
				title="Household"
				description={
					h ? `${h.household.name} · ${h.household.currency} · ${h.household.timezone}` : undefined
				}
				action={
					isOwner && (
						<Button
							size="sm"
							disabled={invite.isPending}
							onClick={async () => {
								const inv = await invite.mutateAsync({ data: { role: "member" } });
								setLink(window.location.origin + inv.path);
							}}
						>
							<UserPlus />
							Invite
						</Button>
					)
				}
			/>
			<ul className="divide-y divide-line border-t border-line">
				{(h?.members ?? []).map((m) => (
					<li key={m.id} className="flex items-center justify-between gap-3 px-5 py-3">
						<div className="min-w-0">
							<p className="truncate text-sm font-medium">{m.name}</p>
							{m.email && <p className="truncate text-xs text-muted">{m.email}</p>}
						</div>
						<Badge tone={m.role === "owner" ? "accent" : "neutral"}>{m.role}</Badge>
					</li>
				))}
			</ul>
			{link && (
				<div className="grid gap-2 border-t border-line px-5 py-4">
					<p className="text-sm">
						Send this link to the person you are inviting. It works once, for 7 days, and only on a device
						that can reach this server.
					</p>
					<CodeBlock code={link} />
				</div>
			)}
		</Card>
	);
}
