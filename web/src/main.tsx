import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRootRoute, createRoute, createRouter, RouterProvider, redirect } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Toaster } from "sonner";
import { getAuthSessionQueryKey } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { lang } from "@/lib/i18n";
import { ActivityPage } from "@/routes/activity";
import { AgentsPage } from "@/routes/agents";
import { JoinPage, Root } from "@/routes/auth";
import { Calendar } from "@/routes/calendar";
import { Checkup } from "@/routes/checkup";
import { Insights } from "@/routes/insights";
import { Overview } from "@/routes/overview";
import { SettingsPage } from "@/routes/settings";
import { SubscriptionDetail } from "@/routes/subscription-detail";
import { Subscriptions } from "@/routes/subscriptions";
import "./styles.css";

const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);

const root = createRootRoute({ component: Root });
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
	createRoute({ getParentRoute: () => root, path: "/insights", component: Insights }),
	createRoute({ getParentRoute: () => root, path: "/checkup", component: Checkup }),
	createRoute({ getParentRoute: () => root, path: "/activity", component: ActivityPage }),
	createRoute({ getParentRoute: () => root, path: "/agents", component: AgentsPage }),
	createRoute({
		getParentRoute: () => root,
		path: "/settings",
		component: SettingsPage,
		validateSearch: (s: Record<string, unknown>): { tab?: string } => ({ tab: str(s.tab) }),
	}),
	createRoute({
		getParentRoute: () => root,
		path: "/welcome",
		beforeLoad: () => {
			throw redirect({ to: "/" });
		},
	}),
	createRoute({ getParentRoute: () => root, path: "/join/$code", component: JoinPage }),
]);

const router = createRouter({ routeTree, defaultPreload: "intent" });
declare module "@tanstack/react-router" {
	interface Register {
		router: typeof router;
	}
}

// A 401 anywhere means the session ended (signed out elsewhere, password
// changed, expired): re-read it so the root shows the sign-in page.
const onError = (e: unknown) => {
	if (e instanceof ApiError && e.status === 401) {
		queryClient.invalidateQueries({ queryKey: getAuthSessionQueryKey() });
	}
};
const queryClient: QueryClient = new QueryClient({
	queryCache: new QueryCache({ onError }),
	mutationCache: new MutationCache({ onError }),
	defaultOptions: {
		queries: {
			staleTime: 15_000,
			retry: (n, e) => !(e instanceof ApiError && e.status < 500) && n < 1,
		},
	},
});

document.documentElement.lang = lang;

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
