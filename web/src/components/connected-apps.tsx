import { useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { CodeBlock } from "@/components/connect";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { useAuthDisconnectApp, useAuthListApps } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { locale, t } from "@/lib/i18n";

const guide = "https://github.com/akamachinecode/exhale/blob/main/docs/guides/remote-agents.md";

function day(iso: string) {
	return new Date(iso).toLocaleDateString(locale, { day: "numeric", month: "short", year: "numeric" });
}

/**
 * Remote MCP (ADR 0024): the address to give Claude.ai, ChatGPT or Grok,
 * and the applications connected through OAuth, each revocable.
 */
export function RemoteAgents() {
	const apps = useAuthListApps();
	const disconnect = useAuthDisconnectApp();
	const qc = useQueryClient();
	const list = apps.data?.apps ?? [];
	const url = apps.data?.mcpUrl;
	return (
		<Card className="mt-4">
			<CardHeader
				title={t("Claude.ai, ChatGPT and Grok")}
				description={t(
					"Cloud assistants reach Exhale over the internet. Add a custom connector with this address, then approve it on the page that opens.",
				)}
				action={
					<a
						href={guide}
						target="_blank"
						rel="noreferrer"
						className="inline-flex shrink-0 items-center gap-1 text-[13px] text-muted hover:text-fg"
					>
						{t("Guide")} <ExternalLink className="size-3.5" />
					</a>
				}
			/>
			<div className="px-5 pb-4">
				{url ? (
					<CodeBlock code={url} />
				) : (
					apps.isSuccess && (
						<p className="rounded-lg bg-raised px-3 py-2 text-[13px] text-muted">
							{t(
								"This needs a public HTTPS address. Set EXHALE_PUBLIC_URL to it and restart Exhale; the guide shows how.",
							)}
						</p>
					)
				)}
			</div>
			<div className="border-t border-line px-5 py-4">
				<p className="mb-3 text-[13px] font-medium">{t("Connected apps")}</p>
				{list.length === 0 ? (
					<p className="text-[13px] text-muted">{t("No applications connected yet.")}</p>
				) : (
					<ul className="divide-y divide-line rounded-lg border border-line">
						{list.map((a) => (
							<li key={a.id} className="flex items-center justify-between gap-3 px-3 py-2.5">
								<div className="min-w-0">
									<p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm font-medium">
										<span className="truncate">{a.name}</span>
										{a.verified ? (
											<Badge tone="ok">{a.clientHost}</Badge>
										) : (
											<Badge tone="warn">{t("Unverified")}</Badge>
										)}
										<Badge>{a.scope === "write" ? t("read and change") : t("read only")}</Badge>
									</p>
									<p className="text-xs text-muted">
										{t("Connected {0}", day(a.createdAt))} ·{" "}
										{a.lastUsedAt ? t("last used {0}", day(a.lastUsedAt)) : t("never used")} ·{" "}
										{a.redirectHost}
									</p>
								</div>
								<Button
									variant="ghost"
									size="icon"
									aria-label={t("Disconnect {0}", a.name)}
									onClick={async () => {
										try {
											await disconnect.mutateAsync({ data: { id: a.id } });
											qc.invalidateQueries();
											toast.success(t("{0} disconnected", a.name));
										} catch (e) {
											toast.error(e instanceof ApiError ? e.detail : t("Could not disconnect"));
										}
									}}
								>
									<Trash2 />
								</Button>
							</li>
						))}
					</ul>
				)}
			</div>
		</Card>
	);
}
