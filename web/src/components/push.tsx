import { useQueryClient } from "@tanstack/react-query";
import { BellRing, Loader2, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	type LinkInfo,
	pushFindDevice,
	useAuthListLinks,
	useAuthTestLink,
	useAuthUnlink,
	usePushAddDevice,
	usePushRemoveDevice,
	usePushStatus,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import {
	currentSubscription,
	deviceName,
	PushDenied,
	pushSupport,
	subscribe,
	subscriptionKeys,
} from "@/lib/push";

// Web Push (ADR 0026): each browser or installed app where a person turns
// it on is one destination. Notifications carry a "paid" button that works
// without opening the app.

function errText(e: unknown, fallback: string) {
	return e instanceof ApiError ? e.detail : fallback;
}

/** The link id of this browser's subscription, "" when push is off here. */
function useThisDevice(devices: LinkInfo[]) {
	const [id, setId] = useState<string>();
	const known = devices.map((d) => d.id).join(",");
	useEffect(() => {
		let live = true;
		void known; // re-check when the list changes (a device removed elsewhere)
		(async () => {
			const sub = await currentSubscription().catch(() => null);
			const found = sub ? await pushFindDevice({ endpoint: sub.endpoint }).catch(() => undefined) : undefined;
			if (live) setId(found?.id ?? "");
		})();
		return () => {
			live = false;
		};
	}, [known]);
	return id;
}

export function PushNotifications() {
	const qc = useQueryClient();
	const support = pushSupport();
	const status = usePushStatus({ query: { enabled: support === "ok" } });
	const links = useAuthListLinks();
	const add = usePushAddDevice();
	const remove = usePushRemoveDevice();
	const devices = (links.data?.links ?? []).filter((l) => l.channel === "webpush");
	const thisId = useThisDevice(devices);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const blocked = support === "ok" && Notification.permission === "denied";

	const turnOn = async () => {
		const key = status.data?.publicKey;
		if (!key) return;
		setError(undefined);
		setBusy(true);
		try {
			const sub = await subscribe(key);
			await add.mutateAsync({ data: { ...subscriptionKeys(sub), device: deviceName() } });
			await qc.invalidateQueries();
			toast.success(t("Notifications are on for this device"));
		} catch (err) {
			if (err instanceof PushDenied) setError(t("Notifications were not allowed."));
			else setError(errText(err, t("Could not turn on notifications here")));
		} finally {
			setBusy(false);
		}
	};
	const turnOff = async () => {
		setBusy(true);
		try {
			const sub = await currentSubscription();
			if (sub) {
				await remove.mutateAsync({ data: { endpoint: sub.endpoint } });
				await sub.unsubscribe();
			}
			await qc.invalidateQueries();
			toast.success(t("Notifications are off for this device"));
		} catch (err) {
			setError(errText(err, t("Could not turn off notifications here")));
		} finally {
			setBusy(false);
		}
	};

	if (links.isPending) return <Loader2 className="size-4 animate-spin text-muted" />;
	return (
		<div className="grid gap-4">
			<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
				<p className="text-sm">
					<span className="font-medium">{t("This device")}</span>
					<span className="text-muted">
						{" · "}
						{thisId ? t("on") : t("off")}
					</span>
				</p>
				{support === "ok" && !blocked && thisId !== undefined && (
					<Button
						variant={thisId ? "ghost" : "primary"}
						size="sm"
						disabled={busy || (!thisId && !status.data)}
						onClick={thisId ? turnOff : turnOn}
					>
						{busy ? <Loader2 className="animate-spin" /> : !thisId && <BellRing />}
						{thisId ? t("Turn off") : t("Turn on for this device")}
					</Button>
				)}
			</div>
			<Why support={support} blocked={blocked} />
			{error && <p className="text-xs text-bad">{error}</p>}
			{devices.length > 0 && (
				<div className="grid gap-2">
					{devices.map((d) => (
						<Device key={d.id} link={d} current={d.id === thisId} />
					))}
				</div>
			)}
			<p className="text-xs text-muted">
				{t(
					"Push needs Exhale on https:// (localhost works for testing). On iPhone and iPad, add Exhale to the Home Screen and turn it on from there.",
				)}
			</p>
		</div>
	);
}

/** Why the button is missing, when it is. */
function Why({ support, blocked }: { support: ReturnType<typeof pushSupport>; blocked: boolean }) {
	const text = {
		ok: blocked
			? t("Notifications are blocked for this site. Allow them in the browser's site settings.")
			: "",
		insecure: t("This page is not on https://, so the browser does not offer notifications."),
		"ios-install": t("Open Exhale from the Home Screen: Share, then Add to Home Screen."),
		unsupported: t("This browser does not support push notifications."),
	}[support];
	return text ? <p className="text-sm text-muted">{text}</p> : null;
}

function Device({ link, current }: { link: LinkInfo; current: boolean }) {
	const qc = useQueryClient();
	const test = useAuthTestLink();
	const unlink = useAuthUnlink();
	const [error, setError] = useState<string>();
	const name = link.destination || t("Unnamed device");
	return (
		<div className="grid gap-1.5">
			<div className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2">
				<span className="flex min-w-0 items-center gap-2 text-sm">
					<span className="truncate">{name}</span>
					{current && <Badge tone="ok">{t("This device")}</Badge>}
				</span>
				<span className="flex shrink-0 items-center gap-1">
					<Button
						variant="ghost"
						size="sm"
						disabled={test.isPending}
						onClick={async () => {
							setError(undefined);
							try {
								await test.mutateAsync({ data: { id: link.id } });
								toast.success(t("Test sent to {0}", name));
							} catch (err) {
								setError(errText(err, t("The test could not be sent")));
								await qc.invalidateQueries(); // a device the push service forgot is gone now
							}
						}}
					>
						{test.isPending && <Loader2 className="animate-spin" />}
						{t("Send test")}
					</Button>
					<Button
						variant="ghost"
						size="icon"
						aria-label={t("Remove {0}", name)}
						disabled={unlink.isPending}
						onClick={async () => {
							await unlink.mutateAsync({ data: { id: link.id } });
							if (current) await (await currentSubscription())?.unsubscribe();
							await qc.invalidateQueries();
							toast.success(t("{0} removed", name));
						}}
					>
						<Trash2 />
					</Button>
				</span>
			</div>
			{error && <p className="text-xs text-bad">{error}</p>}
		</div>
	);
}
