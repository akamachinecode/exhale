// The title bar of Exhale's own window (ADR 0031). In a browser none of this
// renders. On Windows and Linux the window has no system frame, so these are
// its minimise, maximise and close buttons; on a Mac the traffic lights are
// the system's and the page only leaves room for them.
import { Copy, Minus, Square, X } from "lucide-react";
import { useSyncExternalStore } from "react";
import { windowAction, windowFrame, windowState } from "@/lib/desktop";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

function useWindowState() {
	return useSyncExternalStore(windowState.subscribe, windowState.get, windowState.get);
}

/** Minimise, maximise (or restore) and close, where the page draws them. */
export function WindowControls({ className }: { className?: string }) {
	const { maximised, fullscreen } = useWindowState();
	if (windowFrame !== "frame") return null;
	const button =
		"grid h-8 w-9 place-items-center rounded-lg text-muted transition-colors hover:bg-frame-hover hover:text-fg focus-visible:outline-2 focus-visible:outline-fg/40";
	const restore = maximised || fullscreen;
	return (
		<div data-no-drag className={cn("flex shrink-0 items-center gap-0.5", className)}>
			<button
				type="button"
				className={button}
				onClick={() => windowAction("minimise")}
				aria-label={t("Minimize")}
			>
				<Minus className="size-4" strokeWidth={1.75} />
			</button>
			<button
				type="button"
				className={button}
				onClick={() => windowAction("maximise")}
				aria-label={restore ? t("Restore") : t("Maximize")}
			>
				{restore ? (
					<Copy className="size-3.5 -scale-x-100" strokeWidth={1.75} />
				) : (
					<Square className="size-3.5" strokeWidth={1.75} />
				)}
			</button>
			<button
				type="button"
				className={cn(button, "hover:bg-bad hover:text-white")}
				onClick={() => windowAction("close")}
				aria-label={t("Close")}
			>
				<X className="size-4" strokeWidth={1.75} />
			</button>
		</div>
	);
}

/** A title bar for screens without the app's frame (loading, sign-in):
 * something to move the window by, and its buttons. */
export function WindowBar() {
	if (!windowFrame) return null;
	return (
		<div data-drag className="fixed inset-x-0 top-0 z-50 flex h-11 items-center justify-end px-3">
			<WindowControls />
		</div>
	);
}
