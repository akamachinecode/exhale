import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { PageHeader } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { useGetContext } from "@/gen/api";

const origin = typeof window === "undefined" ? "http://localhost:8484" : window.location.origin;

const clients = [
	{
		name: "Claude Code",
		where: "Terminal",
		code: "claude mcp add exhale -- exhale mcp",
	},
	{
		name: "Claude Desktop · Cursor · LM Studio",
		where: "Add to the MCP servers config",
		code: JSON.stringify({ mcpServers: { exhale: { command: "exhale", args: ["mcp"] } } }, null, 2),
	},
	{
		name: "Any MCP client over HTTP",
		where: 'Create a token with: exhale token create --name "My agent"',
		code: JSON.stringify(
			{
				mcpServers: {
					exhale: { type: "http", url: `${origin}/mcp`, headers: { Authorization: "Bearer exhale_pat_…" } },
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
			<div className="grid gap-4 lg:grid-cols-3">
				{clients.map((c) => (
					<Card key={c.name} className="flex flex-col">
						<CardHeader title={c.name} description={c.where} />
						<CodeBlock code={c.code} />
					</Card>
				))}
			</div>
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

function CodeBlock({ code }: { code: string }) {
	const [copied, setCopied] = useState(false);
	return (
		<div className="relative mx-5 mb-5 flex-1 rounded-lg border border-line bg-raised">
			<button
				type="button"
				aria-label="Copy"
				onClick={() => {
					navigator.clipboard?.writeText(code);
					setCopied(true);
					setTimeout(() => setCopied(false), 1500);
				}}
				className="absolute top-2 right-2 rounded-md p-1.5 text-muted hover:bg-surface hover:text-fg"
			>
				{copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
			</button>
			<pre className="overflow-x-auto p-3 pr-10 font-mono text-[12px] leading-relaxed">{code}</pre>
		</div>
	);
}
