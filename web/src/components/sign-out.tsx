import { useQueryClient } from "@tanstack/react-query";
import { LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuthLogout } from "@/gen/api";
import { signedOut } from "@/lib/session";

export function SignOut({ compact }: { compact?: boolean }) {
	const qc = useQueryClient();
	const logout = useAuthLogout();
	return (
		<Button
			variant="ghost"
			size={compact ? "icon" : "sm"}
			aria-label="Sign out"
			onClick={async () => {
				await logout.mutateAsync();
				signedOut(qc);
			}}
		>
			<LogOut />
			{!compact && "Sign out"}
		</Button>
	);
}
