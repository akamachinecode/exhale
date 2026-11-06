import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

/**
 * Live updates (ADR 0013): the server streams bus events over SSE. Each event
 * refreshes the cached data; after a reconnect everything is refetched,
 * because events may have been missed while disconnected.
 */
export function useLiveEvents() {
	const qc = useQueryClient();
	useEffect(() => {
		let first = true;
		const es = new EventSource("/api/v1/events");
		const refresh = (client: QueryClient) => client.invalidateQueries();
		es.addEventListener("ready", () => {
			if (!first) refresh(qc);
			first = false;
		});
		for (const type of [
			"subscription.created",
			"subscription.updated",
			"subscription.deleted",
			"charge.updated",
			"payment_method.saved",
			"category.saved",
		]) {
			es.addEventListener(type, () => refresh(qc));
		}
		return () => es.close();
	}, [qc]);
}
