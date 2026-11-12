// Exhale's own window (ADR 0030): on a computer with the app installed, the
// web UI runs in a native webview (internal/desktop/window) instead of a
// browser tab. That window has no address bar, no tabs and no download bar,
// so this file hands it what a browser would do by itself:
//
// - links to other sites open in the person's browser;
// - downloads (exports, documents) ask where to save them;
// - the title bar: on Windows and Linux the window has no system frame, so
//   the page draws minimise, maximise and close, and moves and resizes the
//   window (ADR 0031). On a Mac the traffic lights stay, over the sidebar.
//
// The window listens on the webview's message channel; messages are
// "exhale:" plus JSON, and the window only accepts them from this origin.
import { toast } from "sonner";
import { t } from "@/lib/i18n";

type Message =
	| { type: "ready" }
	| { type: "open"; url: string }
	| { type: "save"; name: string; data: string }
	| { type: "window"; action: "minimise" | "maximise" | "close" | "titlebar" }
	| { type: "update" };

type Host = {
	chrome?: { webview?: { postMessage(m: string): void } }; // WebView2, Windows
	webkit?: { messageHandlers?: { external?: { postMessage(m: string): void } } }; // WebKit, macOS and Linux
};

function channel(): ((m: string) => void) | null {
	if (typeof window === "undefined") return null;
	const w = window as unknown as Host;
	const webview2 = w.chrome?.webview;
	if (webview2) return (m) => webview2.postMessage(m);
	const webkit = w.webkit?.messageHandlers?.external;
	if (webkit) return (m) => webkit.postMessage(m);
	return null;
}

const post = channel();

/** True inside Exhale's own window, false in a browser. */
export const inDesktopWindow = post !== null;

/** Which title bar the window has: "mac" keeps the traffic lights, "frame"
 * (Windows, Linux) is drawn by the page, null is a browser. */
export const windowFrame: "mac" | "frame" | null = !post
	? null
	: /Macintosh|Mac OS X/.test(navigator.userAgent)
		? "mac"
		: "frame";

function send(m: Message) {
	post?.(`exhale:${JSON.stringify(m)}`);
}

/** The window's system, or null in a browser. */
export const windowOS: "mac" | "windows" | "linux" | null = !post
	? null
	: /Windows/.test(navigator.userAgent)
		? "windows"
		: windowFrame === "mac"
			? "mac"
			: "linux";

/** An update the window runs (Linux packages, ADR 0033): it installs the
 * newest package with pkexec, which asks for the password. */
export type WindowUpdate = {
	phase: "downloading" | "installing" | "restarting" | "failed" | "cancelled";
	error?: string;
};

/** Asks the window to update the Linux package; progress comes as
 * "exhale:update" events, see onWindowUpdate. */
export function updateFromWindow() {
	send({ type: "update" });
}

/** Listens to the window's update progress; returns the unsubscribe. */
export function onWindowUpdate(fn: (u: WindowUpdate) => void): () => void {
	const h = (e: Event) => fn((e as CustomEvent<WindowUpdate>).detail);
	window.addEventListener("exhale:update", h);
	return () => window.removeEventListener("exhale:update", h);
}

/** The page's own minimise, maximise and close buttons. */
export function windowAction(action: "minimise" | "maximise" | "close") {
	send({ type: "window", action });
}

/** Starts the bridge; does nothing in a browser. */
export function initDesktop() {
	if (!post || !windowFrame) return;
	document.documentElement.dataset.window = windowFrame;
	// Linux gives a window without a frame no edge at all: styles.css draws one.
	document.documentElement.dataset.os = windowOS ?? "";
	document.addEventListener("click", onClick, true);
	initTitleBar(post);
	// Lets the window run scripts in the page (the maximised state below).
	post("wails:runtime:ready");
	send({ type: "ready" });
}

// --- Title bar ---------------------------------------------------------------
//
// Wails moves and resizes a window when the page asks with "wails:drag" and
// "wails:resize:<edge>" while the mouse button is down. Its own runtime does
// this for pages it serves; Exhale's pages come from the local server, so
// this is the same logic, kept small.
//
// - An element with data-drag moves the window, except on its buttons,
//   links and fields, and except inside a data-no-drag one closer to the
//   pointer. Double-clicking it does what the system's title bar does: the
//   window decides (on a Mac, from "Double-click a window's title bar to").
//   The window only starts moving once the pointer has moved a few pixels,
//   so a double click with a shaky hand is not taken for a drag.
// - Without a system frame (Windows, Linux), the 5px band along each edge
//   resizes the window.

type WindowState = { maximised: boolean; fullscreen: boolean };
let state: WindowState = { maximised: false, fullscreen: false };
const listeners = new Set<() => void>();

/** The window's state, for useSyncExternalStore. */
export const windowState = {
	get: () => state,
	subscribe(fn: () => void) {
		listeners.add(fn);
		return () => listeners.delete(fn);
	},
};

const edge = 5; // px of the resize band
const dragSlop = 3; // px the pointer moves before the window does
const corner = 10; // extra px that count as a corner
const cursors: Record<string, string> = {
	"n-resize": "ns-resize",
	"s-resize": "ns-resize",
	"e-resize": "ew-resize",
	"w-resize": "ew-resize",
	"nw-resize": "nwse-resize",
	"se-resize": "nwse-resize",
	"ne-resize": "nesw-resize",
	"sw-resize": "nesw-resize",
};

function initTitleBar(post: (m: string) => void) {
	let armed: "drag" | `resize:${string}` | null = null;
	let from = { x: 0, y: 0 }; // where the button went down
	let moving = false; // the window is being moved or resized
	let swallow = false; // so the page doesn't take the end of it for a click
	let resize = "";

	window.addEventListener("exhale:window", (e) => {
		state = (e as CustomEvent<WindowState>).detail;
		document.documentElement.toggleAttribute("data-maximised", state.maximised || state.fullscreen);
		document.documentElement.toggleAttribute("data-fullscreen", state.fullscreen);
		for (const fn of listeners) fn();
	});

	const draggable = (e: MouseEvent) => {
		// The nearest marked area decides, so a card inside the frame doesn't move it.
		const t = e.target as Element | null;
		const zone = t?.closest?.("[data-drag], [data-no-drag]");
		return (
			!!zone?.hasAttribute("data-drag") &&
			!t?.closest("button, a, input, select, textarea, label, [role=button]")
		);
	};

	window.addEventListener(
		"mousedown",
		(e) => {
			if (e.button !== 0) return;
			swallow = false;
			if (resize) {
				armed = `resize:${resize}`;
				e.preventDefault();
			} else if (e.detail === 1 && draggable(e)) {
				armed = "drag";
				from = { x: e.screenX, y: e.screenY };
			} else {
				armed = null;
			}
		},
		true,
	);
	window.addEventListener(
		"mousemove",
		(e) => {
			if (armed && e.buttons & 1) {
				if (armed === "drag" && Math.hypot(e.screenX - from.x, e.screenY - from.y) < dragSlop) return;
				post(armed === "drag" ? "wails:drag" : `wails:${armed}`);
				armed = null;
				moving = swallow = true;
				return;
			}
			if (moving) {
				if (e.buttons & 1) return;
				moving = false;
			}
			setResize(windowFrame === "frame" && !state.maximised && !state.fullscreen ? edgeAt(e) : "");
		},
		true,
	);
	window.addEventListener(
		"mouseup",
		() => {
			armed = null;
		},
		true,
	);
	window.addEventListener(
		"click",
		(e) => {
			if (!swallow) return;
			swallow = false;
			e.stopImmediatePropagation();
			e.preventDefault();
		},
		true,
	);
	window.addEventListener("dblclick", (e) => {
		if (!draggable(e)) return;
		e.preventDefault();
		send({ type: "window", action: "titlebar" });
	});

	let before = "";
	function setResize(next: string) {
		if (next === resize) return;
		if (!resize) before = document.body.style.cursor;
		document.body.style.cursor = next ? cursors[next] : before;
		resize = next;
	}
}

function edgeAt(e: MouseEvent): string {
	const w = document.documentElement.clientWidth;
	const h = document.documentElement.clientHeight;
	const { clientX: x, clientY: y } = e;
	if (x > w || y > h) return ""; // on a scrollbar
	const left = x < edge + corner;
	const right = w - x < edge + corner;
	const top = y < edge + corner;
	const bottom = h - y < edge + corner;
	if (bottom && right) return "se-resize";
	if (bottom && left) return "sw-resize";
	if (top && left) return "nw-resize";
	if (top && right) return "ne-resize";
	if (x < edge) return "w-resize";
	if (y < edge) return "n-resize";
	if (h - y < edge) return "s-resize";
	if (w - x < edge) return "e-resize";
	return "";
}

function onClick(e: MouseEvent) {
	if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
	const a = (e.target as Element | null)?.closest?.("a[href]");
	if (!(a instanceof HTMLAnchorElement)) return;
	let url: URL;
	try {
		url = new URL(a.href, location.href);
	} catch {
		return;
	}
	const sameOrigin = url.origin === location.origin;
	const file =
		sameOrigin && (a.hasAttribute("download") || (a.target === "_blank" && url.pathname.startsWith("/api/")));
	if (file) {
		e.preventDefault();
		void save(url, a.getAttribute("download") ?? "");
	} else if (!sameOrigin) {
		// The window has no address bar to come back from another site.
		e.preventDefault();
		send({ type: "open", url: url.href });
	} else if (a.target === "_blank") {
		// No tabs either: a page of Exhale opens here.
		e.preventDefault();
		location.assign(url.href);
	}
}

async function save(url: URL, suggested: string) {
	try {
		const res = await fetch(url, { credentials: "same-origin" });
		if (!res.ok) throw new Error(String(res.status));
		const blob = await res.blob();
		send({ type: "save", name: suggested || fileName(res, url), data: await base64(blob) });
	} catch {
		toast.error(t("Something went wrong"));
	}
}

/** The name the server suggests (Content-Disposition), else the URL's. */
function fileName(res: Response, url: URL) {
	const cd = res.headers.get("Content-Disposition") ?? "";
	const star = /filename\*=UTF-8''([^;]+)/i.exec(cd);
	if (star) return decodeURIComponent(star[1]);
	const plain = /filename="?([^";]+)"?/i.exec(cd);
	if (plain) return plain[1];
	return url.pathname.split("/").pop() || "exhale-download";
}

function base64(blob: Blob): Promise<string> {
	return new Promise((resolve, reject) => {
		const r = new FileReader();
		r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ""));
		r.onerror = () => reject(r.error);
		r.readAsDataURL(blob);
	});
}
