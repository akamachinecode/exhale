import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Field, Input, Select } from "@/components/ui/field";
import { type KeepAwakeInfo, type SetKeepAwakeInput, useGetKeepAwake, useSetKeepAwake } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

/** Owners: keep the computer Exhale runs on from sleeping (ADR 0028). */
export function KeepAwake() {
	const q = useGetKeepAwake();
	const qc = useQueryClient();
	const set = useSetKeepAwake();
	const k = q.data;
	if (!k) return null;
	const zone = k.timezone.replaceAll("_", " ");
	const save = async (data: SetKeepAwakeInput) => {
		try {
			await set.mutateAsync({ data });
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, t("Could not save")));
		}
	};
	const time = (field: "from" | "to", value: string, label: string) => (
		<Input
			type="time"
			aria-label={label}
			className="w-32"
			defaultValue={value}
			key={value}
			disabled={set.isPending}
			onBlur={(e) => {
				if (e.target.value && e.target.value !== value) save({ [field]: e.target.value });
			}}
		/>
	);
	return (
		<div className="grid gap-4">
			<p className="text-sm text-muted">
				{t(
					"While this computer sleeps, reminders wait and the buttons in Telegram and Slack stop answering.",
				)}
			</p>
			<div className="grid gap-2">
				<Field label={t("Keep this computer awake")}>
					<Select
						className="max-w-64"
						value={k.mode}
						disabled={set.isPending}
						onChange={(e) => save({ mode: e.target.value as SetKeepAwakeInput["mode"] })}
					>
						<option value="off">{t("Off")}</option>
						<option value="always">{t("Always")}</option>
						<option value="hours">{t("Only between two times")}</option>
					</Select>
				</Field>
				{k.mode === "hours" && (
					<div className="flex flex-wrap items-center gap-2 text-sm">
						<span aria-hidden>{t("Between")}</span>
						{time("from", k.from, t("Start time"))}
						<span aria-hidden>{t("and")}</span>
						{time("to", k.to, t("End time"))}
						<span className="text-xs text-muted">
							{k.to < k.from ? t("the next day, {0} time", zone) : t("{0} time", zone)}
						</span>
					</div>
				)}
			</div>
			<label className="flex items-start gap-2.5 text-sm">
				<input
					type="checkbox"
					className="mt-0.5 size-4 accent-fg"
					checked={!k.keepDisplayOn}
					disabled={set.isPending}
					onChange={(e) => save({ keepDisplayOn: !e.target.checked })}
				/>
				<span>
					{t("Let the display sleep")}
					<span className="block text-xs text-muted">
						{t("Only the computer stays on; the screen turns off as usual.")}
					</span>
				</span>
			</label>
			<KeepAwakeStatusLine k={k} />
			<p className="text-xs text-muted">{t("A laptop with its lid closed still sleeps.")}</p>
		</div>
	);
}

function KeepAwakeStatusLine({ k }: { k: KeepAwakeInfo }) {
	const s = k.status;
	let tone: "ok" | "muted" | "bad" = "muted";
	let text: string;
	switch (s.state) {
		case "active":
			tone = "ok";
			text =
				k.mode === "hours"
					? t("Active now: this computer stays awake until {0}.", k.to)
					: t("Active now: this computer stays awake.");
			break;
		case "waiting":
			text = t("Not active now: outside the hours. It starts again at {0}.", k.from);
			break;
		case "unavailable":
			tone = "bad";
			text =
				s.reason === "container"
					? t(
							"Not available here: Exhale runs in a container. Keep the computer that runs it awake in its own settings.",
						)
					: s.reason === "unsupported"
						? t("Not available on this operating system.")
						: t("Not available here: {0}", s.error ?? "");
			break;
		case "failed":
			tone = "bad";
			text = t("The system refused: {0}", s.error ?? "");
			break;
		case "unknown":
			text = t("Exhale is not running as a server here, so nothing is applied.");
			break;
		default:
			text = t("Off: this computer sleeps as usual.");
	}
	return (
		<p className="flex items-start gap-2 text-sm" role="status" data-state={s.state}>
			<span
				aria-hidden
				className={cn(
					"mt-1.5 size-2 shrink-0 rounded-full",
					tone === "ok" ? "bg-ok" : tone === "bad" ? "bg-bad" : "bg-faint",
				)}
			/>
			<span className={cn(tone === "bad" && "text-bad")}>{text}</span>
		</p>
	);
}
