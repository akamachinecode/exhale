// The documentation site (ADR 0016), published by pages.yml at
// akamachinecode.github.io/exhale/docs/, next to the download page (site/).
// The pages are the Markdown in docs/, copied here by scripts/sync.mjs.
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";
import starlightLlmsTxt from "starlight-llms-txt";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";
import { sync, watchSources } from "./scripts/sync.mjs";

const github = "https://github.com/akamachinecode/exhale";

// Install, guides and the project pages are listed by hand, in reading
// order; ADRs and the API are listed on their own.
const sidebar = [
	{ label: "Start here", items: [{ label: "Introduction", link: "/" }] },
	{
		label: "Install",
		items: [
			{ label: "Overview", link: "/install/" },
			{ label: "macOS", link: "/install/macos/" },
			{ label: "Windows", link: "/install/windows/" },
			{ label: "Linux", link: "/install/linux/" },
			{ label: "Docker", link: "/install/docker/" },
			{ label: "HTTPS and remote access", link: "/install/https/" },
			{ label: "Backups", link: "/install/backups/" },
			{ label: "Single sign-on", link: "/install/sso/" },
		],
	},
	{
		label: "Guides",
		items: [
			{ label: "Reminders that end in an answer", link: "/guides/reminders/" },
			{ label: "Push, email, ntfy and webhook", link: "/guides/notifications/" },
			{ label: "Slack", link: "/guides/slack/" },
			{ label: "A team or small business", link: "/guides/teams/" },
			{ label: "Many subscriptions", link: "/guides/many-subscriptions/" },
			{ label: "The built-in assistant", link: "/guides/assistant/" },
			{ label: "Claude.ai, ChatGPT and Grok", link: "/guides/remote-agents/" },
			{ label: "Translating Exhale", link: "/guides/translating/" },
		],
	},
	{ label: "Reference", items: [...openAPISidebarGroups] },
	{
		label: "Project",
		items: [
			{ label: "Vision", link: "/vision/" },
			{
				label: "Architecture decisions",
				collapsed: true,
				items: [{ label: "How we decide", link: "/adr/" }, { autogenerate: { directory: "adr" } }],
			},
			{ label: "Changelog", link: `${github}/blob/main/CHANGELOG.md` },
		],
	},
];

// Copies docs/*.md into the content folder before Astro reads it, and again
// on every change while `astro dev` runs. A page that is not in the sidebar
// fails the build: nobody would find it.
const syncDocs = {
	name: "exhale-sync-docs",
	hooks: {
		"astro:config:setup": ({ command }) => {
			const listed = new Set(links(sidebar));
			const missing = [...sync().values()].filter((slug) => !slug.startsWith("adr/") && !listed.has(`/${slug}/`));
			if (missing.length) throw new Error(`Add these pages to the sidebar in docs/astro.config.mjs: ${missing.join(", ")}`);
			if (command === "dev") watchSources();
		},
	},
};

function links(items) {
	return items.flatMap((i) => (i.items ? links(i.items) : i.link ? [i.link] : []));
}

export default defineConfig({
	site: "https://akamachinecode.github.io",
	base: "/exhale/docs",
	trailingSlash: "always",
	devToolbar: { enabled: false },
	integrations: [
		syncDocs,
		starlight({
			title: "Exhale",
			description:
				"Exhale is a free, self-hosted subscription manager where the reminder is the interface: fix a renewal from the Telegram or Slack message itself.",
			logo: { src: "./src/assets/logo.svg" },
			favicon: "/favicon.svg",
			social: [{ icon: "github", label: "GitHub", href: github }],
			customCss: ["./src/styles/theme.css"],
			components: {
				ThemeProvider: "./src/components/ThemeProvider.astro",
				SocialIcons: "./src/components/SocialIcons.astro",
			},
			// Each page sets its own editUrl (the source file in docs/).
			editLink: { baseUrl: `${github}/edit/main/docs/` },
			lastUpdated: false,
			// Greys and one soft accent, like the rest of the site.
			expressiveCode: {
				themes: ["min-dark", "min-light"],
				shiki: { langAlias: { caddyfile: "txt" } },
				styleOverrides: {
					borderRadius: "10px",
					borderColor: "var(--sl-color-hairline)",
					codeBackground: "var(--sl-color-gray-6)",
					frames: {
						frameBoxShadowCssValue: "none",
						editorBackground: "var(--sl-color-gray-6)",
						editorTabBarBackground: "var(--sl-color-gray-6)",
						editorActiveTabBackground: "var(--sl-color-gray-6)",
						editorTabBarBorderBottomColor: "var(--sl-color-hairline)",
						terminalBackground: "var(--sl-color-gray-6)",
						terminalTitlebarBackground: "var(--sl-color-gray-6)",
						terminalTitlebarBorderBottomColor: "var(--sl-color-hairline)",
						terminalTitlebarDotsForeground: "var(--sl-color-gray-4)",
					},
				},
			},
			plugins: [
				starlightOpenAPI([
					{
						base: "api",
						schema: "./src/openapi.json", // written by scripts/sync.mjs
						sidebar: { label: "REST API", collapsed: true },
					},
				]),
				starlightLlmsTxt({
					projectName: "Exhale",
					description:
						"Exhale is an open-source, self-hosted subscription manager. Reminders in Telegram and Slack carry buttons to fix the subscription in place, and every operation is exposed to AI agents over MCP.",
					// Install and how-to first; the decisions behind the code last,
					// and only in llms-full.txt.
					promote: ["index*", "install/**", "guides/**"],
					demote: ["vision*", "adr/**"],
					exclude: ["adr/**", "api/**"],
				}),
				// Pages written by starlight-openapi are not in the content collection,
				// and the download page (/exhale/) is not part of this site.
				starlightLinksValidator({ exclude: ["/exhale/", "/exhale/docs/api/", "/exhale/docs/api/**"] }),
			],
			sidebar,
		}),
	],
});
