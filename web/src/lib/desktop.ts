// Exhale's own window (ADR 0030): on a computer with the app installed, the
// web UI runs in a native webview (internal/desktop/window) instead of a
// browser tab. That window has no address bar, no tabs and no download bar,
// so this file hands it what a browser would do by itself:
//
// - links to other sites open in the person's browser;
// - downloads (exports, documents) ask where to save them.
//
// The window listens on the webview's message channel; messages are
// "exhale:" plus JSON, and the window only accepts them from this origin.
import { toast } from "sonner";
import { t } from "@/lib/i18n";

type Message =
	| { type: "ready" }
	| { type: "open"; url: string }
	| { type: "save"; name: string; data: string };

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

function send(m: Message) {
	post?.(`exhale:${JSON.stringify(m)}`);
}

/** Starts the bridge; does nothing in a browser. */
export function initDesktop() {
	if (!post) return;
	document.addEventListener("click", onClick, true);
	send({ type: "ready" });
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
