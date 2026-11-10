import { Bot, Check, ChevronDown, Repeat, Send, SlidersHorizontal, Users, X } from "lucide-react";
import { type ReactNode, useState } from "react";
import { ClaudeConnect, PreferencesForm, ReminderTime, Sharing, TelegramSetup } from "@/components/connect";
import { Button } from "@/components/ui/button";
import {
	useAuthListLinks,
	useAuthListTokens,
	useAuthSession,
	useGetHousehold,
	useListSubscriptions,
} from "@/gen/api";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const HIDE_KEY = "exhale.getStarted.hidden";

export function readGetStartedHidden() {
	try {
		return localStorage.getItem(HIDE_KEY) === "1";
	} catch {
		return false;
	}
}

/**
 * The setup lives on the home screen (no separate pages): one card with the
 * steps, the next one open. Each step does its work in place.
 */
export function GetStarted({
	onAddSubscription,
	onHide,
}: {
	onAddSubscription: () => void;
	onHide: () => void;
}) {
	const session = useAuthSession();
	const links = useAuthListLinks();
	const tokens = useAuthListTokens();
	const subs = useListSubscriptions({});
	const household = useGetHousehold();
	const [open, setOpen] = useState<string>();

	const loaded = links.data && tokens.data && subs.data && household.data;
	if (!loaded) return null;

	const name = session.data?.user?.name?.split(" ")[0];
	const h = household.data.household;
	const count = subs.data.subscriptions?.length ?? 0;
	const steps: Step[] = [
		{
			id: "telegram",
			icon: <Send />,
			title: t("Get reminders in Telegram"),
			summary: t("Fix any renewal from the message itself."),
			done: (links.data.links ?? []).some((l) => l.channel === "telegram"),
			body: <TelegramSetup />,
		},
		{
			id: "subs",
			icon: <Repeat />,
			title: t("Add your subscriptions"),
			summary: count ? t("{0} so far", count) : t("One by one, or let Claude add them"),
			done: count > 0,
			body: (
				<div className="flex flex-wrap items-center gap-3">
					<Button variant="primary" onClick={onAddSubscription}>
						{t("Add a subscription")}
					</Button>
					<span className="text-sm text-muted">{t("or connect Claude and paste your list.")}</span>
				</div>
			),
		},
		{
			id: "claude",
			icon: <Bot />,
			title: t("Connect Claude"),
			summary: t("Manage everything by asking"),
			done: (tokens.data.tokens ?? []).some((t) => t.lastUsedAt),
			body: <ClaudeConnect />,
		},
		{
			id: "prefs",
			icon: <SlidersHorizontal />,
			title: t("Check your preferences"),
			summary: t(
				"Totals in {0} · {1} · reminders at {2}:{3}",
				h.currency,
				h.timezone.replaceAll("_", " "),
				String(h.reminderHour).padStart(2, "0"),
				String(h.reminderMinute).padStart(2, "0"),
			),
			done: true,
			optional: true,
			body: (
				<div className="grid gap-5">
					<PreferencesForm />
					<ReminderTime withTimezone={false} />
				</div>
			),
		},
		{
			id: "share",
			icon: <Users />,
			title: t("Share it with someone"),
			summary: t("A partner, family or flatmates"),
			done: (household.data.members ?? []).length > 1,
			optional: true,
			body: <Sharing />,
		},
	];
	const required = steps.filter((s) => !s.optional);
	const doneCount = required.filter((s) => s.done).length;
	const allDone = doneCount === required.length;
	const current = open ?? steps.find((s) => !s.done)?.id;

	const hide = () => {
		try {
			localStorage.setItem(HIDE_KEY, "1");
		} catch {}
		onHide();
	};

	return (
		<section
			aria-labelledby="get-started"
			className="ring-brand mb-6 overflow-hidden rounded-2xl shadow-[var(--glow)]"
		>
			<header className="flex items-start justify-between gap-4 px-5 pt-5 pb-4">
				<div>
					<h2 id="get-started" className="text-lg font-semibold tracking-tight">
						{allDone ? t("You're all set") : name ? t("Welcome, {0}", name) : t("Welcome")}
					</h2>
					<p className="mt-0.5 text-sm text-muted">
						{allDone ? t("Reminders will find you.") : t("{0} of {1} done", doneCount, required.length)}
					</p>
				</div>
				<div className="flex items-center gap-3">
					<Progress value={doneCount / required.length} />
					<Button variant="ghost" size="icon" aria-label={t("Hide setup")} onClick={hide}>
						<X />
					</Button>
				</div>
			</header>
			<ol className="border-t border-line">
				{steps.map((s, i) => (
					<StepRow
						key={s.id}
						n={i + 1}
						step={s}
						open={current === s.id}
						onToggle={() => setOpen(current === s.id ? "" : s.id)}
					/>
				))}
			</ol>
		</section>
	);
}

type Step = {
	id: string;
	icon: ReactNode;
	title: string;
	summary: string;
	done: boolean;
	optional?: boolean;
	body: ReactNode;
};

function StepRow({
	n,
	step,
	open,
	onToggle,
}: {
	n: number;
	step: Step;
	open: boolean;
	onToggle: () => void;
}) {
	return (
		<li className="border-b border-line last:border-b-0">
			<button
				type="button"
				onClick={onToggle}
				aria-expanded={open}
				className="flex w-full items-center gap-3 px-5 py-3.5 text-left transition-colors hover:bg-raised/60"
			>
				<span
					className={cn(
						"grid size-7 shrink-0 place-items-center rounded-full border text-xs [&_svg]:size-3.5",
						step.done && !step.optional
							? "border-transparent bg-fg text-bg"
							: open
								? "border-fg/40 text-fg"
								: "border-line text-muted",
					)}
					aria-hidden
				>
					{step.done && !step.optional ? <Check /> : step.icon}
				</span>
				<span className="min-w-0 flex-1">
					<span className="block text-sm font-medium">
						<span className="sr-only">
							{t("Step")} {n}:{" "}
						</span>
						{step.title}
						{step.optional && <span className="ml-2 text-xs font-normal text-faint">{t("optional")}</span>}
					</span>
					<span className="block truncate text-xs text-muted">{step.summary}</span>
				</span>
				<ChevronDown
					className={cn("size-4 shrink-0 text-faint transition-transform", open && "rotate-180")}
				/>
			</button>
			{open && <div className="px-5 pt-1 pb-5 sm:pl-15">{step.body}</div>}
		</li>
	);
}

function Progress({ value }: { value: number }) {
	const r = 14;
	const c = 2 * Math.PI * r;
	return (
		<svg viewBox="0 0 36 36" className="size-9 -rotate-90" aria-hidden>
			<defs>
				<linearGradient id="gs-progress" x1="0" y1="0" x2="1" y2="1">
					<stop offset="0" stopColor="oklch(0.78 0.12 300)" />
					<stop offset="0.5" stopColor="oklch(0.8 0.1 230)" />
					<stop offset="1" stopColor="oklch(0.85 0.12 175)" />
				</linearGradient>
			</defs>
			<circle cx="18" cy="18" r={r} fill="none" stroke="var(--line)" strokeWidth="3" />
			<circle
				cx="18"
				cy="18"
				r={r}
				fill="none"
				opacity={value > 0 ? 1 : 0}
				stroke="url(#gs-progress)"
				strokeWidth="3"
				strokeLinecap="round"
				strokeDasharray={`${c * value} ${c}`}
			/>
		</svg>
	);
}
