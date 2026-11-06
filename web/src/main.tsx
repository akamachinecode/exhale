import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Toaster } from "sonner";
import { Layout } from "@/components/layout";
import { ActivityPage } from "@/routes/activity";
import { AgentsPage } from "@/routes/agents";
import { Calendar } from "@/routes/calendar";
import { Overview } from "@/routes/overview";
import { SubscriptionDetail } from "@/routes/subscription-detail";
import { Subscriptions } from "@/routes/subscriptions";
import "./styles.css";

const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);

const root = createRootRoute({ component: Layout });
const routeTree = root.addChildren([
	createRoute({ getParentRoute: () => root, path: "/", component: Overview }),
	createRoute({
		getParentRoute: () => root,
		path: "/subscriptions/",
		component: Subscriptions,
		validateSearch: (s: Record<string, unknown>): { status?: string; q?: string } => ({
			status: str(s.status),
			q: str(s.q),
		}),
	}),
	createRoute({ getParentRoute: () => root, path: "/subscriptions/$id", component: SubscriptionDetail }),
	createRoute({
		getParentRoute: () => root,
		path: "/calendar",
		component: Calendar,
		validateSearch: (s: Record<string, unknown>): { month?: string } => ({
			month: str(s.month)?.match(/^\d{4}-\d{2}$/)?.[0],
		}),
	}),
	createRoute({ getParentRoute: () => root, path: "/activity", component: ActivityPage }),
	createRoute({ getParentRoute: () => root, path: "/agents", component: AgentsPage }),
]);

const router = createRouter({ routeTree, defaultPreload: "intent" });
declare module "@tanstack/react-router" {
	interface Register {
		router: typeof router;
	}
}

const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 15_000, retry: 1 } } });

const el = document.getElementById("root");
if (el) {
	createRoot(el).render(
		<StrictMode>
			<QueryClientProvider client={queryClient}>
				<RouterProvider router={router} />
				<Toaster position="bottom-right" richColors closeButton />
			</QueryClientProvider>
		</StrictMode>,
	);
}
