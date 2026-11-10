import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { useMembersResetTwoFactor, useMembersTwoFactor } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";

/**
 * Owners only: turn off two-factor sign-in for a member who lost their
 * phone and their backup codes (ADR 0025). Recorded in Activity.
 */
export function ResetTwoFactor({ id, name }: { id: string; name: string }) {
	const qc = useQueryClient();
	const list = useMembersTwoFactor();
	const reset = useMembersResetTwoFactor();
	const [confirming, setConfirming] = useState(false);
	if (!list.data?.members?.includes(id)) return null;
	if (!confirming) {
		return (
			<Button size="sm" variant="ghost" onClick={() => setConfirming(true)}>
				{t("Turn off 2FA")}
			</Button>
		);
	}
	return (
		<span className="flex items-center gap-1">
			<Button
				size="sm"
				variant="danger"
				disabled={reset.isPending}
				onClick={async () => {
					try {
						await reset.mutateAsync({ data: { id } });
						await qc.invalidateQueries({ queryKey: list.queryKey });
						toast.success(t("Two-factor sign-in is off for {0}", name));
					} catch (err) {
						toast.error(err instanceof ApiError ? err.detail : t("Could not save"));
					}
					setConfirming(false);
				}}
			>
				{t("Turn off 2FA for {0}", name)}
			</Button>
			<Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>
				{t("Cancel")}
			</Button>
		</span>
	);
}
