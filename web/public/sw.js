// Exhale's service worker: push notifications only (ADR 0026).
//
// It has no fetch handler and caches nothing, so the app and the API always
// come from the network exactly as without it.
//
// A push carries {title, body, tag, url, actions: [{action, title, token}]}.
// Pressing an action posts its token to the server, which runs the same use
// case as the chat button; no tab and no session are needed. If that fails
// (expired button, offline), the charge opens in the app instead.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

const icon = "/icon-192.png";

self.addEventListener("push", (event) => {
	let d = {};
	try {
		d = event.data ? event.data.json() : {};
	} catch {
		d = { title: "Exhale", body: event.data ? event.data.text() : "" };
	}
	const actions = Array.isArray(d.actions) ? d.actions : [];
	const max = typeof Notification !== "undefined" && Notification.maxActions ? Notification.maxActions : 2;
	event.waitUntil(
		self.registration.showNotification(d.title || "Exhale", {
			body: d.body || "",
			tag: d.tag || "exhale",
			renotify: true,
			icon,
			data: { url: d.url || "/", actions },
			actions: actions.slice(0, max).map((a) => ({ action: a.action, title: a.title })),
		}),
	);
});

self.addEventListener("notificationclick", (event) => {
	const n = event.notification;
	const data = n.data || {};
	const act = event.action ? (data.actions || []).find((a) => a.action === event.action) : undefined;
	n.close();
	event.waitUntil(act ? run(act, n, data.url) : open(data.url));
});

async function run(act, n, url) {
	try {
		const res = await fetch("/api/v1/push/action", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ token: act.token }),
			credentials: "omit",
		});
		if (!res.ok) throw new Error(String(res.status));
		const out = await res.json();
		// Say what happened in place of the reminder; tapping it opens the charge.
		await self.registration.showNotification(out.title || n.title, {
			body: out.message || "",
			tag: n.tag,
			icon,
			silent: true,
			data: { url, actions: [] },
		});
	} catch {
		await open(url);
	}
}

async function open(url) {
	const target = new URL(url || "/", self.location.origin).href;
	const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
	for (const w of windows) {
		if (new URL(w.url).origin !== self.location.origin) continue;
		try {
			await w.focus();
			if (w.url !== target) await w.navigate(target);
			return;
		} catch {
			// Not controlled by this worker yet: open a new window below.
		}
	}
	await self.clients.openWindow(target);
}

// The browser replaced the subscription (keys rotated, or it expired):
// register the new one while the session is still valid. Without a
// session the old device stops working and Settings shows push as off.
self.addEventListener("pushsubscriptionchange", (event) => {
	event.waitUntil(
		(async () => {
			const key = event.oldSubscription?.options?.applicationServerKey;
			const sub =
				event.newSubscription ||
				(key ? await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }) : null);
			if (!sub) return;
			const json = sub.toJSON();
			await fetch("/api/v1/push/devices", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ endpoint: json.endpoint, p256dh: json.keys.p256dh, auth: json.keys.auth }),
			});
		})().catch(() => {}),
	);
});
