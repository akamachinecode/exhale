import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import {
	Activity,
	Bot,
	CalendarDays,
	ChartNoAxesColumn,
	LayoutGrid,
	Moon,
	PanelLeft,
	PanelLeftClose,
	Repeat,
	Settings,
	Stethoscope,
	Sun,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AssistantPanel } from "@/components/assistant";
import { SignOut } from "@/components/sign-out";
import { useAuthSession, useAuthSetLanguage } from "@/gen/api";
import { useLiveEvents } from "@/lib/events";
import { lang, languages, setLang, syncLanguage, t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// Every destination, in the same order everywhere: the sidebar on wide
// screens and small windows, the dock on phones.
const nav = [
	{ to: "/", label: t("Overview"), icon: LayoutGrid },
	{ to: "/subscriptions", label: t("Subscriptions"), icon: Repeat },
	{ to: "/calendar", label: t("Calendar"), icon: CalendarDays },
	{ to: "/insights", label: t("Insights"), icon: ChartNoAxesColumn },
	{ to: "/checkup", label: t("Check-up"), icon: Stethoscope },
	{ to: "/activity", label: t("Activity"), icon: Activity },
	{ to: "/agents", label: t("AI agents"), icon: Bot },
] as const;
const settings = { to: "/settings", label: t("Settings"), icon: Settings } as const;

function useTheme() {
	const [dark, setDark] = useState(() => {
		try {
			const saved = localStorage.getItem("theme");
			if (saved) return saved === "dark";
		} catch {}
		return true; // dark by default; the toggle remembers the choice
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
			<span className="bg-brand grid size-7 place-items-center rounded-[9px]">
				<svg viewBox="0 0 32 32" className="size-7" aria-hidden>
					<path
						d="M8 18c3-6 13-6 16 0"
						stroke="oklch(0.13 0 0)"
						strokeWidth="2.6"
						fill="none"
						strokeLinecap="round"
					/>
					<path
						d="M11 22c2.2-3 7.8-3 10 0"
						stroke="oklch(0.13 0 0 / 0.6)"
						strokeWidth="2.2"
						fill="none"
						strokeLinecap="round"
					/>
				</svg>
			</span>
			<span className="text-[15px] font-semibold tracking-tight">Exhale</span>
		</span>
	);
}

/**
 * The app's frame. Three widths, one navigation:
 * - 1024px and wider: the sidebar is always there.
 * - 768–1023px (tablets, small windows): the sidebar hides itself. It slides
 *   in when the pointer touches the left edge, when the menu button is
 *   pressed or when the keyboard reaches it, and hides again after.
 * - Phones: a floating dock with every destination, which steps aside while
 *   scrolling down and comes back when scrolling up.
 */
export function Layout() {
	useLiveEvents();
	const [dark, setDark] = useTheme();
	const session = useAuthSession();
	// The language is the person's (or their household's), on every device.
	const language = session.data?.language;
	useEffect(() => syncLanguage(language), [language]);
	const sidebar = useAutoHide();
	const toggleTheme = () => setDark(!dark);
	return (
		<div className="min-h-dvh md:max-lg:[--sticky-top:3.5rem] lg:grid lg:grid-cols-[232px_1fr]">
			<AssistantPanel />
			{/* Touching the left edge with the pointer reveals the hidden sidebar. */}
			<div
				aria-hidden
				className="fixed inset-y-0 left-0 z-40 w-2 max-md:hidden lg:hidden"
				{...sidebar.edge}
			/>
			<div
				aria-hidden
				onClick={sidebar.close}
				className={cn(
					"fixed inset-0 z-40 bg-black/30 transition-opacity duration-200 max-md:hidden lg:hidden motion-reduce:transition-none",
					sidebar.state === "open" ? "opacity-100" : "pointer-events-none opacity-0",
				)}
			/>
			<aside
				id="sidebar"
				ref={sidebar.ref}
				{...sidebar.panel}
				className={cn(
					"fixed inset-y-0 left-0 z-50 flex h-dvh w-[232px] flex-col border-r border-line bg-surface px-3 py-4 max-md:hidden",
					"transition-[translate,box-shadow] duration-200 ease-out motion-reduce:transition-none",
					sidebar.state === "closed" ? "-translate-x-full" : "translate-x-0 shadow-2xl",
					"lg:sticky lg:top-0 lg:bottom-auto lg:z-auto lg:translate-x-0 lg:bg-surface/60 lg:shadow-none lg:transition-none",
				)}
			>
				<div className="flex items-center justify-between pb-6 pl-2">
					<Logo />
					<button
						type="button"
						onClick={sidebar.close}
						aria-label={t("Hide menu")}
						className="rounded-lg p-1.5 text-muted transition-colors hover:bg-raised hover:text-fg lg:hidden"
					>
						<PanelLeftClose className="size-4" />
					</button>
				</div>
				<nav className="grid gap-0.5">
					{nav.map((item) => (
						<SidebarLink key={item.to} {...item} />
					))}
				</nav>
				<div className="mt-auto grid">
					<SidebarLink {...settings} />
				</div>
				<div className="mt-2 flex items-center justify-between gap-1 border-t border-line pt-3 pl-2">
					<div className="min-w-0 text-xs">
						<p className="truncate font-medium text-fg">{session.data?.user?.name ?? "—"}</p>
						<p className="truncate text-muted">{session.data?.user?.email}</p>
					</div>
					<div className="flex shrink-0 items-center">
						<LanguageToggle />
						<ThemeToggle dark={dark} onToggle={toggleTheme} />
						<SignOut compact />
					</div>
				</div>
			</aside>
			<div className="min-w-0 max-md:pb-[calc(5.5rem+env(safe-area-inset-bottom))]">
				<header className="flex h-14 items-center gap-1 border-b border-line bg-surface/60 px-4 md:sticky md:top-0 md:z-30 md:bg-bg/80 md:backdrop-blur lg:hidden">
					<button
						type="button"
						onClick={sidebar.toggle}
						aria-label={sidebar.state === "closed" ? t("Show menu") : t("Hide menu")}
						aria-controls="sidebar"
						aria-expanded={sidebar.state !== "closed"}
						className="-ml-2 rounded-lg p-2 text-muted transition-colors hover:bg-raised hover:text-fg max-md:hidden"
					>
						<PanelLeft className="size-4" />
					</button>
					<Logo />
					<div className="ml-auto flex items-center md:hidden">
						<LanguageToggle />
						<ThemeToggle dark={dark} onToggle={toggleTheme} />
					</div>
				</header>
				<main className="mx-auto w-full max-w-[1180px] px-4 py-6 md:px-8 md:py-8">
					<Outlet />
				</main>
			</div>
			<Dock />
		</div>
	);
}

function SidebarLink({ to, label, icon: Icon }: { to: string; label: string; icon: typeof Settings }) {
	return (
		<Link
			to={to}
			activeOptions={{ exact: to === "/" }}
			className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-muted transition-colors hover:bg-raised hover:text-fg"
			activeProps={{ className: "bg-raised !text-fg font-medium" }}
		>
			<Icon className="size-4" />
			{label}
		</Link>
	);
}

/**
 * The state of the sidebar between 768 and 1023px. "peek" follows the
 * pointer (or the keyboard) and hides when it leaves; "open" was asked for
 * with the menu button and stays until dismissed. Wider screens ignore it.
 */
function useAutoHide() {
	const [state, setState] = useState<"closed" | "peek" | "open">("closed");
	const ref = useRef<HTMLElement>(null);
	const timer = useRef(0);
	const inside = useRef(false);
	const later = (fn: () => void, ms: number) => {
		clearTimeout(timer.current);
		timer.current = window.setTimeout(fn, ms);
	};
	const close = () => {
		clearTimeout(timer.current);
		setState("closed");
		// Don't leave the keyboard on a link that slid away.
		if (ref.current?.contains(document.activeElement)) (document.activeElement as HTMLElement).blur();
	};

	// A new page, or a width where the sidebar is pinned or absent, closes it.
	const path = useRouterState({ select: (s) => s.location.pathname });
	useEffect(() => {
		void path;
		setState("closed");
	}, [path]);
	useEffect(() => {
		const sizes = [matchMedia("(min-width: 1024px)"), matchMedia("(min-width: 768px)")];
		const reset = () => setState("closed");
		for (const m of sizes) m.addEventListener("change", reset);
		return () => {
			for (const m of sizes) m.removeEventListener("change", reset);
			clearTimeout(timer.current);
		};
	}, []);
	useEffect(() => {
		if (state === "closed") return;
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") close();
		};
		document.addEventListener("keydown", onKey);
		return () => document.removeEventListener("keydown", onKey);
	});

	const mouse = (e: React.PointerEvent) => e.pointerType === "mouse";
	return {
		state,
		ref,
		close,
		toggle: () => setState(state === "open" ? "closed" : "open"),
		edge: {
			// A short wait, so a pointer just passing by doesn't open it.
			onPointerEnter: (e: React.PointerEvent) => {
				if (mouse(e) && state === "closed") later(() => setState("peek"), 80);
			},
			onPointerLeave: (e: React.PointerEvent) => {
				if (mouse(e) && state === "closed") clearTimeout(timer.current);
			},
		},
		panel: {
			onPointerEnter: (e: React.PointerEvent) => {
				if (!mouse(e)) return;
				inside.current = true;
				clearTimeout(timer.current);
			},
			onPointerLeave: (e: React.PointerEvent) => {
				if (!mouse(e)) return;
				inside.current = false;
				if (state === "peek") later(close, 300);
			},
			onFocus: () => {
				if (state === "closed") setState("peek");
			},
			onBlur: (e: React.FocusEvent) => {
				const next = e.relatedTarget as Node | null;
				if (state !== "closed" && !inside.current && !e.currentTarget.contains(next)) close();
			},
		},
	};
}

/** The phone's navigation: every destination in a floating pill. */
function Dock() {
	const [hidden, setHidden] = useHideOnScroll();
	const path = useRouterState({ select: (s) => s.location.pathname });
	// After moving to another page, its name shows above its icon for a moment:
	// a phone has no hover to tell what an icon means.
	const [flash, setFlash] = useState(false);
	const shown = useRef(path);
	useEffect(() => {
		if (shown.current === path) return;
		shown.current = path;
		setHidden(false);
		setFlash(true);
		const id = setTimeout(() => setFlash(false), 1400);
		return () => clearTimeout(id);
	}, [path, setHidden]);
	return (
		<nav
			aria-label={t("Navigation")}
			onFocus={() => setHidden(false)}
			className={cn(
				"fixed bottom-[max(0.75rem,env(safe-area-inset-bottom))] left-1/2 z-40 -translate-x-1/2 transition-[translate,opacity] duration-300 ease-out md:hidden motion-reduce:transition-none",
				hidden && "pointer-events-none translate-y-[calc(100%+1.5rem)] opacity-0",
			)}
		>
			<div className="dock-bar flex items-end gap-1 rounded-full p-1.5">
				{[...nav, settings].map(({ to, label, icon: Icon }) => (
					<Link
						key={to}
						to={to}
						activeOptions={{ exact: to === "/" }}
						className="dock-item"
						data-flash={flash && (to === "/" ? path === "/" : path.startsWith(to)) ? "" : undefined}
					>
						<Icon aria-hidden />
						<span className="dock-tip">{label}</span>
					</Link>
				))}
			</div>
		</nav>
	);
}

/** Hidden while scrolling down, back when scrolling up, at the top or at the
 * bottom of the page. Small moves are ignored until they add up. */
function useHideOnScroll() {
	const [hidden, setHidden] = useState(false);
	useEffect(() => {
		let last = window.scrollY;
		let frame = 0;
		const onScroll = () => {
			if (frame) return;
			frame = requestAnimationFrame(() => {
				frame = 0;
				const y = window.scrollY;
				const atEnd = y + window.innerHeight >= document.documentElement.scrollHeight - 24;
				if (y < 48 || atEnd) setHidden(false);
				else if (y - last > 8) setHidden(true);
				else if (last - y > 8) setHidden(false);
				else return;
				last = y;
			});
		};
		window.addEventListener("scroll", onScroll, { passive: true });
		return () => {
			window.removeEventListener("scroll", onScroll);
			cancelAnimationFrame(frame);
		};
	}, []);
	return [hidden, setHidden] as const;
}

/** Switches the language: a button with the other one's name when Exhale
 * speaks two, a list of every language by its own name when it speaks more. */
function LanguageToggle() {
	const others = languages.filter((l) => l.code !== lang);
	const setLanguage = useAuthSetLanguage();
	// Saved as this person's own language, then shown.
	const choose = async (code: string) => {
		try {
			await setLanguage.mutateAsync({ data: { language: code } });
			setLang(code);
		} catch {
			toast.error(t("Could not save"));
		}
	};
	const style =
		"rounded-lg px-2 py-2 text-[11px] font-semibold text-muted uppercase transition-colors hover:bg-raised hover:text-fg";
	if (others.length === 1) {
		const other = others[0];
		return (
			<button
				type="button"
				onClick={() => choose(other.code)}
				aria-label={other.name}
				title={other.name}
				lang={other.code}
				className={style}
			>
				{other.code}
			</button>
		);
	}
	return (
		<select
			value={lang}
			onChange={(e) => choose(e.target.value)}
			aria-label={t("Language")}
			className={cn(style, "cursor-pointer appearance-none bg-transparent text-center")}
		>
			{languages.map((l) => (
				<option key={l.code} value={l.code} lang={l.code}>
					{l.name}
				</option>
			))}
		</select>
	);
}

function ThemeToggle({ dark, onToggle }: { dark: boolean; onToggle: () => void }) {
	return (
		<button
			type="button"
			onClick={onToggle}
			aria-label={dark ? t("Switch to light theme") : t("Switch to dark theme")}
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
