import type { QueryClient } from "@tanstack/react-query";
import { type AuthSession, getAuthSessionQueryKey } from "@/gen/api";

/**
 * Switches the cached session (sign in, join, sign out) and drops every other
 * cached query, so nothing from the previous person is shown. The session
 * query itself is kept so the root re-renders from it.
 */
export function applySession(qc: QueryClient, session: AuthSession) {
	const key = getAuthSessionQueryKey();
	qc.removeQueries({ predicate: (q) => q.queryKey[0] !== key[0] });
	qc.setQueryData(key, session);
}

export function signedOut(qc: QueryClient) {
	const prev = qc.getQueryData<AuthSession>(getAuthSessionQueryKey());
	applySession(qc, {
		setupRequired: false,
		setupNeedsCode: false,
		hasHousehold: true,
		passwordLoginDisabled: false,
		...prev,
		user: undefined,
		assistant: false, // only someone signed in sees Ask Exhale
	});
}
