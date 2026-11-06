import { Link, Outlet } from "@tanstack/react-router";
import { Activity, Bot, CalendarDays, LayoutGrid, Moon, Repeat, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { useGetHousehold } from "@/gen/api";
import { useLiveEvents } from "@/lib/events";
import { cn } from "@/lib/utils";

const nav = [
	{ to: "/", label: "Overview", icon: LayoutGrid },
	{ to: "/subscriptions", label: "Subscriptions", icon: Repeat },
	{ to: "/calendar", label: "Calendar", icon: CalendarDays },
	{ to: "/activity", label: "Activity", icon: Activity },
	{ to: "/agents", label: "AI agents", icon: Bot },
] as const;

function useTheme() {
	const [dark, setDark] = useState(() => {
		try {
			const saved = localStorage.getItem("theme");
			if (saved) return saved === "dark";
		} catch {}
		return window.matchMedia("(prefers-color-scheme: dark)").matches;
	});
	useEffect(() => {
		document.documentElement.classList.toggle("dark", dark);
		try {
			localStorage.setItem("theme", dark ? "dark" : "light");
		} catch {}
	}, [dark]);
	return [dark, setDark] as const;
}

export function Logo() {
	return (
		<span className="flex items-center gap-2.5">
			<svg viewBox="0 0 32 32" className="size-7" aria-hidden>
				<rect width="32" height="32" rx="8" className="fill-accent" />
				<path
					d="M8 18c3-6 13-6 16 0"
					className="stroke-accent-fg"
					strokeWidth="2.6"
					fill="none"
					strokeLinecap="round"
				/>
				<path
					d="M11 22c2.2-3 7.8-3 10 0"
					className="stroke-accent-fg/70"
					strokeWidth="2.2"
					fill="none"
					strokeLinecap="round"
				/>
			</svg>
			<span className="text-[15px] font-semibold tracking-tight">Exhale</span>
		</span>
	);
}

export function Layout() {
	useLiveEvents();
	const [dark, setDark] = useTheme();
	const household = useGetHousehold();
	return (
		<div className="min-h-dvh md:grid md:grid-cols-[232px_1fr]">
			<div className="hidden border-r border-line bg-surface/60 md:block">
				<aside className="sticky top-0 flex h-dvh flex-col px-3 py-4">
					<div className="px-2 pb-6">
						<Logo />
					</div>
					<nav className="grid gap-0.5">
						{nav.map(({ to, label, icon: Icon }) => (
							<Link
								key={to}
								to={to}
								activeOptions={{ exact: to === "/" }}
								className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-muted transition-colors hover:bg-raised hover:text-fg"
								activeProps={{ className: "bg-raised !text-fg font-medium" }}
							>
								<Icon className="size-4" />
								{label}
							</Link>
						))}
					</nav>
					<div className="mt-auto flex items-center justify-between gap-2 border-t border-line px-2 pt-3">
						<div className="min-w-0 text-xs">
							<p className="truncate font-medium text-fg">{household.data?.household.name ?? "Household"}</p>
							<p className="truncate text-muted">
								{household.data?.household.currency} · {household.data?.household.timezone}
							</p>
						</div>
						<ThemeToggle dark={dark} onToggle={() => setDark(!dark)} />
					</div>
				</aside>
			</div>
			<div className="min-w-0 pb-20 md:pb-0">
				<header className="flex items-center justify-between border-b border-line bg-surface/60 px-4 py-3 md:hidden">
					<Logo />
					<ThemeToggle dark={dark} onToggle={() => setDark(!dark)} />
				</header>
				<main className="mx-auto w-full max-w-[1180px] px-4 py-6 md:px-8 md:py-8">
					<Outlet />
				</main>
			</div>
			<nav className="fixed inset-x-0 bottom-0 z-40 grid grid-cols-5 border-t border-line bg-surface/95 px-1 pt-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))] backdrop-blur md:hidden">
				{nav.map(({ to, label, icon: Icon }) => (
					<Link
						key={to}
						to={to}
						activeOptions={{ exact: to === "/" }}
						className="flex flex-col items-center gap-0.5 rounded-lg py-1 text-[11px] text-muted"
						activeProps={{ className: "!text-accent font-medium" }}
					>
						<Icon className="size-5" />
						{label.replace("AI agents", "Agents")}
					</Link>
				))}
			</nav>
		</div>
	);
}

function ThemeToggle({ dark, onToggle }: { dark: boolean; onToggle: () => void }) {
	return (
		<button
			type="button"
			onClick={onToggle}
			aria-label={dark ? "Switch to light theme" : "Switch to dark theme"}
			className={cn("rounded-lg p-2 text-muted transition-colors hover:bg-raised hover:text-fg")}
		>
			{dark ? <Sun className="size-4" /> : <Moon className="size-4" />}
		</button>
	);
}

export function PageHeader({
	title,
	description,
	action,
}: {
	title: string;
	description?: string;
	action?: React.ReactNode;
}) {
	return (
		<div className="mb-6 flex flex-wrap items-end justify-between gap-4">
			<div>
				<h1 className="text-[22px] font-semibold tracking-tight">{title}</h1>
				{description && <p className="mt-1 text-sm text-muted">{description}</p>}
			</div>
			{action}
		</div>
	);
}
