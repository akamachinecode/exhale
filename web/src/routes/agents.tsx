import { apiOrigin, ClaudeConnect, CodeBlock, TokensManager } from "@/components/connect";
import { PageHeader } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { useGetContext } from "@/gen/api";

const clients = [
	{
		name: "Claude Desktop · Cursor · LM Studio",
		where: "Add to the MCP servers config (needs the exhale binary on this machine)",
		code: JSON.stringify({ mcpServers: { exhale: { command: "exhale", args: ["mcp"] } } }, null, 2),
	},
	{
		name: "Any MCP client over HTTP",
		where: "Create a token below, then use it in the header",
		code: JSON.stringify(
			{
				mcpServers: {
					exhale: {
						type: "http",
						url: `${apiOrigin}/mcp`,
						headers: { Authorization: "Bearer exhale_pat_…" },
					},
				},
			},
			null,
			2,
		),
	},
];

export function AgentsPage() {
	const ctx = useGetContext();
	return (
		<>
			<PageHeader
				title="AI agents"
				description="Connect Claude, ChatGPT, Grok or a local model. Agents read everything and can manage everything — with previews, confirmations and undo."
			/>
			<Card className="mb-4">
				<CardHeader title="Claude Code" description="One command in a terminal on this machine." />
				<div className="px-5 pb-5">
					<ClaudeConnect />
				</div>
			</Card>
			<div className="grid gap-4 lg:grid-cols-2">
				{clients.map((c) => (
					<Card key={c.name} className="flex flex-col">
						<CardHeader title={c.name} description={c.where} />
						<CodeBlock code={c.code} className="mx-5 mb-5 flex-1" />
					</Card>
				))}
			</div>
			<Card className="mt-4">
				<CardHeader
					title="Tokens"
					description="Each token acts as you. Revoke one to disconnect that agent."
				/>
				<div className="border-t border-line px-5 py-4">
					<TokensManager />
				</div>
			</Card>
			<Card className="mt-6">
				<CardHeader
					title="What agents see first"
					description="The live briefing served as the exhale://context resource. It is rebuilt on every read."
					action={<Badge tone="accent">19 tools · 4 prompts</Badge>}
				/>
				<pre className="max-h-[420px] overflow-auto border-t border-line px-5 py-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-muted">
					{ctx.data?.markdown ?? "Loading…"}
				</pre>
			</Card>
		</>
	);
}
