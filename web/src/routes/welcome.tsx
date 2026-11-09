import { Link } from "@tanstack/react-router";
import { Bot, Check, Repeat, Send } from "lucide-react";
import type { ReactNode } from "react";
import { ClaudeConnect, TelegramConnect } from "@/components/connect";
import { PageHeader } from "@/components/layout";
import { Card } from "@/components/ui/card";
import { useAuthListLinks, useAuthListTokens, useAuthSession, useListSubscriptions } from "@/gen/api";
import { cn } from "@/lib/utils";

/** After setup or joining: connect Telegram and Claude, then add subscriptions. */
export function WelcomePage() {
	const session = useAuthSession();
	const links = useAuthListLinks();
	const tokens = useAuthListTokens();
	const subs = useListSubscriptions({});
	const name = session.data?.user?.name?.split(" ")[0] ?? "";
	const linked = (links.data?.links ?? []).some((l) => l.channel === "telegram");
	const claude = (tokens.data?.tokens ?? []).length > 0;
	const count = subs.data?.subscriptions?.length ?? 0;
	return (
		<div className="mx-auto max-w-[720px]">
			<PageHeader
				title={name ? `You're in, ${name}` : "You're in"}
				description="Three steps and Exhale runs on its own: reminders arrive where you are, and you fix anything from them."
			/>
			<div className="grid gap-4">
				<Step n={1} done={linked} icon={<Send />} title="Get reminders in Telegram">
					<TelegramConnect />
				</Step>
				<Step n={2} done={claude} icon={<Bot />} title="Connect Claude">
					<ClaudeConnect />
				</Step>
				<Step n={3} done={count > 0} icon={<Repeat />} title="Add your subscriptions">
					<p className="text-sm text-muted">
						{count > 0
							? `${count} subscription${count === 1 ? "" : "s"} so far.`
							: "Paste your list into Claude, or add them one by one."}{" "}
						<Link to="/subscriptions" className="font-medium text-accent hover:underline">
							Open subscriptions
						</Link>
					</p>
				</Step>
			</div>
			<p className="mt-6 text-center text-sm text-muted">
				Everything here is also in{" "}
				<Link to="/settings" className="text-accent hover:underline">
					Settings
				</Link>
				.{" "}
				<Link to="/" className="text-accent hover:underline">
					Go to the overview
				</Link>
			</p>
		</div>
	);
}

function Step({
	n,
	done,
	icon,
	title,
	children,
}: {
	n: number;
	done: boolean;
	icon: ReactNode;
	title: string;
	children: ReactNode;
}) {
	return (
		<Card className="p-5">
			<div className="mb-4 flex items-center gap-3">
				<span
					className={cn(
						"grid size-8 shrink-0 place-items-center rounded-full text-sm font-semibold [&_svg]:size-4",
						done ? "bg-ok-soft text-ok" : "bg-accent-soft text-accent",
					)}
					aria-hidden
				>
					{done ? <Check /> : icon}
				</span>
				<h2 className="text-[15px] font-semibold">
					<span className="sr-only">Step {n}: </span>
					{title}
				</h2>
				{done && <span className="ml-auto text-xs font-medium text-ok">Done</span>}
			</div>
			{children}
		</Card>
	);
}
