// Web Push in this browser (ADR 0026): the service worker in public/sw.js
// shows the notifications; this file asks for permission and manages the
// browser's subscription.
import { t } from "@/lib/i18n";

/** Why push cannot be turned on here, or "ok". */
export type PushSupport = "ok" | "insecure" | "ios-install" | "unsupported";

const isIOS = () =>
	/iPhone|iPad|iPod/.test(navigator.userAgent) ||
	(navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1); // iPadOS says it is a Mac

const standalone = () =>
	window.matchMedia?.("(display-mode: standalone)").matches ||
	(navigator as Navigator & { standalone?: boolean }).standalone === true;

export function pushSupport(): PushSupport {
	if (typeof window === "undefined") return "unsupported";
	// Browsers only allow push on https:// and on localhost.
	if (!window.isSecureContext) return "insecure";
	// Safari on iPhone and iPad only offers push to apps added to the Home Screen.
	if (isIOS() && !standalone()) return "ios-install";
	if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
		return "unsupported";
	}
	return "ok";
}

/** Registers the service worker; it caches nothing and only handles push. */
export function registerServiceWorker() {
	if (typeof navigator === "undefined" || !("serviceWorker" in navigator) || !window.isSecureContext) return;
	navigator.serviceWorker.register("/sw.js").catch(() => {});
}

async function registration() {
	await navigator.serviceWorker.register("/sw.js");
	return navigator.serviceWorker.ready;
}

/** The push subscription of this browser, if any. */
export async function currentSubscription(): Promise<PushSubscription | null> {
	if (pushSupport() !== "ok") return null;
	const reg = await navigator.serviceWorker.getRegistration("/");
	return (await reg?.pushManager.getSubscription()) ?? null;
}

function keyBytes(base64url: string) {
	const s = atob(base64url.replace(/-/g, "+").replace(/_/g, "/"));
	return Uint8Array.from(s, (c) => c.charCodeAt(0));
}

function sameKey(sub: PushSubscription, key: Uint8Array) {
	const k = sub.options.applicationServerKey;
	if (!k) return false;
	const a = new Uint8Array(k);
	return a.length === key.length && a.every((b, i) => b === key[i]);
}

export class PushDenied extends Error {}

/** Asks for permission and subscribes with the server's key. */
export async function subscribe(publicKey: string) {
	if ((await Notification.requestPermission()) !== "granted") throw new PushDenied();
	const reg = await registration();
	const key = keyBytes(publicKey);
	const old = await reg.pushManager.getSubscription();
	if (old && sameKey(old, key)) return old;
	await old?.unsubscribe(); // made with another server's key
	return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
}

/** What the server needs from a subscription. */
export function subscriptionKeys(sub: PushSubscription) {
	const json = sub.toJSON();
	return { endpoint: sub.endpoint, p256dh: json.keys?.p256dh ?? "", auth: json.keys?.auth ?? "" };
}

const browsers: [RegExp, string][] = [
	[/Edg\//, "Edge"],
	[/OPR\//, "Opera"],
	[/Firefox\//, "Firefox"],
	[/Chrome\//, "Chrome"],
	[/Safari\//, "Safari"],
];
const systems: [RegExp, string][] = [
	[/Android/, "Android"],
	[/iPhone/, "iPhone"],
	[/iPad/, "iPad"],
	[/Mac OS X/, "macOS"],
	[/Windows/, "Windows"],
	[/CrOS/, "ChromeOS"],
	[/Linux/, "Linux"],
];

/** A name for this browser, e.g. "Chrome · Android". */
export function deviceName() {
	const ua = navigator.userAgent;
	const browser = browsers.find(([re]) => re.test(ua))?.[1] ?? t("Browser");
	const os = isIOS() && !/iPhone/.test(ua) ? "iPad" : systems.find(([re]) => re.test(ua))?.[1];
	return os ? `${browser} · ${os}` : browser;
}
