import { Badge } from "@/components/ui/badge";
import type { ChargeView } from "@/gen/api";
import { relativeDays } from "@/lib/utils";

export function ChargeStatus({ charge }: { charge: ChargeView }) {
	switch (charge.status) {
		case "paid":
			return <Badge tone="ok">Paid</Badge>;
		case "failed":
			return <Badge tone="bad">Failed</Badge>;
		case "skipped":
			return <Badge>Skipped</Badge>;
	}
	if (charge.daysUntil < 0) return <Badge tone="warn">Unconfirmed</Badge>;
	if (charge.delayed) return <Badge tone="warn">Moved · {relativeDays(charge.daysUntil)}</Badge>;
	if (charge.daysUntil <= 3) return <Badge tone="accent">{relativeDays(charge.daysUntil)}</Badge>;
	return <Badge>{relativeDays(charge.daysUntil)}</Badge>;
}

export function SubscriptionStatus({ status }: { status: string }) {
	if (status === "paused") return <Badge tone="warn">Paused</Badge>;
	if (status === "cancelled") return <Badge>Cancelled</Badge>;
	return <Badge tone="ok">Active</Badge>;
}
