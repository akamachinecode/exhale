import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { toast } from "sonner";
import { TagInput } from "@/components/tags";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/field";
import {
	type CreateSubscriptionInput,
	type SubscriptionView,
	type UpdateSubscriptionInput,
	useCreateSubscription,
	useGetHousehold,
	usePreviewSchedule,
	useUpdateSubscription,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { t } from "@/lib/i18n";
import {
	billingDayText,
	cn,
	type DayRule,
	date,
	dayFacts,
	decimal,
	todayISO,
	unitLabel,
	type WeekendShift,
} from "@/lib/utils";

const units = [{ value: "month" }, { value: "year" }, { value: "week" }, { value: "day" }] as const;
type Unit = (typeof units)[number]["value"];

const reminderOptions = [0, 1, 3, 7, 14, 30];

export function reminderLabel(d: number) {
	return d === 0 ? t("Same day") : d === 1 ? t("1 day before") : t("{0} days before", d);
}

type Values = {
	name: string;
	price: string;
	currency: string;
	interval: number;
	unit: Unit;
	nextCharge: string;
	dayRule: DayRule;
	weekendShift: WeekendShift;
	categoryId: string;
	paymentMethodId: string;
	trial: boolean;
	noticeDays: string;
	manual: boolean;
	startedOn: string;
	endDate: string;
	cancelUrl: string;
	account: string;
	url: string;
	notes: string;
	payerId: string;
	reminderDays: number[];
	shares: Record<string, number>;
	priceFrom: string;
	private: boolean;
	autoConfirm: boolean;
	tags: string[];
	altPrice: string;
	altUnit: "month" | "year";
};

function initial(s: SubscriptionView | undefined, currency: string): Values {
	const rule = s?.schedule.versions?.at(-1)?.rule;
	return {
		tags: s?.tags ?? [],
		altPrice: s?.altPrice ? decimal(s.altPrice.price) : "",
		altUnit: s?.altPrice?.unit === "month" ? "month" : "year",
		name: s?.name ?? "",
		price: s ? decimal(s.price) : "",
		currency: s?.price.currency ?? currency,
		interval: rule?.interval ?? 1,
		unit: (rule?.unit as Unit) ?? "month",
		// The date before a weekend shift: the billing day itself.
		nextCharge: s?.nextScheduled ?? s?.nextCharge ?? (s ? "" : todayISO()),
		dayRule: (rule?.dayRule as DayRule) || "day",
		weekendShift: (rule?.weekendShift as WeekendShift) || "none",
		categoryId: s?.categoryId ?? "",
		paymentMethodId: s?.paymentMethodId ?? "",
		trial: Boolean(s?.trialEndsOn),
		noticeDays: s?.noticeDays ? String(s.noticeDays) : "",
		manual: s?.renewal === "manual",
		startedOn: s?.startedOn ?? "",
		endDate: s?.schedule.until ?? "",
		cancelUrl: s?.cancelUrl ?? "",
		account: s?.account ?? "",
		url: s?.url ?? "",
		notes: s?.notes ?? "",
		payerId: s?.payerId ?? "",
		reminderDays: s?.reminders.daysBefore ?? [3, 0],
		shares: Object.fromEntries((s?.shares ?? []).map((x) => [x.memberId, x.weight])),
		priceFrom: "",
		private: s?.visibility === "private",
		autoConfirm: s?.autoConfirm ?? false,
	};
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** A copy starts from another subscription: same terms, a new name, no charges and no history. */
function copied(s: SubscriptionView, currency: string): Values {
	return {
		...initial(s, currency),
		name: t("{0} (copy)", s.name),
		nextCharge: s.nextCharge ?? todayISO(),
		trial: false,
		startedOn: "",
		endDate: "",
	};
}

/**
 * Creates a subscription, or edits one when `subscription` is given. With
 * `copyOf` it creates a new one prefilled from another (Duplicate).
 */
export function SubscriptionDialog({
	open,
	onOpenChange,
	subscription,
	copyOf,
}: {
	open: boolean;
	onOpenChange: (o: boolean) => void;
	subscription?: SubscriptionView;
	copyOf?: SubscriptionView;
}) {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const navigate = useNavigate();
	const create = useCreateSubscription();
	const update = useUpdateSubscription();
	const editing = Boolean(subscription);
	const base = initial(subscription, household.data?.household.currency ?? "EUR");
	const [v, setV] = useState<Values>(base);
	const tagSuggestions = (household.data?.tags ?? []).map((x) => x.name);
	const [more, setMore] = useState(false);
	const [error, setError] = useState<string>();
	const set = <K extends keyof Values>(k: K, value: Values[K]) => setV((old) => ({ ...old, [k]: value }));
	const mainCurrency = household.data?.household.currency;

	// Reset whenever the dialog opens (for another subscription or a new one).
	// biome-ignore lint/correctness/useExhaustiveDependencies: only when it opens
	useEffect(() => {
		if (open) {
			setV(copyOf ? copied(copyOf, mainCurrency ?? "EUR") : initial(subscription, mainCurrency ?? "EUR"));
			setError(undefined);
			setMore(Boolean(copyOf));
		}
	}, [open, subscription?.id, copyOf?.id, mainCurrency]);

	// Which billing days the chosen date offers ("day 18", "the third
	// Wednesday", "the last Wednesday"); a choice the date no longer offers
	// falls back to the day number.
	const monthly = v.unit === "month" || v.unit === "year";
	const facts = v.nextCharge ? dayFacts(v.nextCharge) : undefined;
	const dayRules: DayRule[] = ["day"];
	if (monthly && facts?.week) dayRules.push("weekday");
	if (monthly && facts?.last) dayRules.push("last-weekday");
	const dayRule: DayRule = dayRules.includes(v.dayRule) ? v.dayRule : "day";
	// A shift moves a charge by up to two days: closer charges could collide.
	const shiftable = v.unit !== "day" || v.interval >= 3;
	const weekendShift: WeekendShift = shiftable ? v.weekendShift : "none";

	const preview = usePreviewSchedule();
	const { mutate } = preview;
	// The web never computes charge dates: the engine previews them (ADR 0013).
	useEffect(() => {
		if (!open || !v.nextCharge) return;
		const t = setTimeout(
			() =>
				mutate({
					data: {
						interval: v.interval,
						unit: v.unit,
						firstCharge: v.nextCharge,
						dayRule,
						weekendShift,
						count: 4,
					},
				}),
			250,
		);
		return () => clearTimeout(t);
	}, [open, v.interval, v.unit, v.nextCharge, dayRule, weekendShift, mutate]);

	const members = household.data?.members ?? [];
	const shares = Object.entries(v.shares)
		.filter(([, w]) => w > 0)
		.map(([memberId, weight]) => ({ memberId, weight }));
	const priceChanged = editing && v.price.replace(",", ".") !== base.price;

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		const price = v.price.trim().replace(",", ".");
		if (!v.name.trim()) return setError(t("Give it a name."));
		if (!/^\d+(\.\d{1,3})?$/.test(price)) return setError(t("The price should look like 12.99."));
		if (!v.nextCharge && !editing) return setError(t("Choose the date of the next charge."));
		const altPrice = v.altPrice.trim().replace(",", ".");
		if (altPrice && !/^\d+(\.\d{1,3})?$/.test(altPrice))
			return setError(t("The other price should look like 99.00."));
		try {
			if (!subscription) {
				const data: CreateSubscriptionInput = {
					name: v.name.trim(),
					price,
					currency: v.currency,
					interval: v.interval,
					unit: v.unit,
					firstCharge: v.nextCharge,
					dayRule: dayRule === "day" ? undefined : dayRule,
					weekendShift: weekendShift === "none" ? undefined : weekendShift,
					categoryId: v.categoryId || undefined,
					paymentMethodId: v.paymentMethodId || undefined,
					payerId: v.payerId || undefined,
					trialEndsOn: v.trial ? v.nextCharge : undefined,
					noticeDays: Number(v.noticeDays) || undefined,
					renewal: v.manual ? "manual" : undefined,
					startedOn: v.startedOn || undefined,
					endDate: v.endDate || undefined,
					cancelUrl: v.cancelUrl || undefined,
					account: v.account || undefined,
					url: v.url || undefined,
					notes: v.notes || undefined,
					reminderDays: v.reminderDays,
					shares: shares.length ? shares : undefined,
					private: v.private || undefined,
					autoConfirm: v.autoConfirm || undefined,
					tags: v.tags.length ? v.tags : undefined,
					altPrice: altPrice || undefined,
					altUnit: altPrice ? v.altUnit : undefined,
				};
				const res = await create.mutateAsync({ data });
				await qc.invalidateQueries();
				toast.success(t("{0} added", res.subscription.name));
				onOpenChange(false);
				navigate({ to: "/subscriptions/$id", params: { id: res.subscription.id } });
				return;
			}
			// Only what changed is sent, so nothing else moves.
			const data: UpdateSubscriptionInput = { id: subscription.id };
			if (v.name.trim() !== base.name) data.name = v.name.trim();
			if (priceChanged) {
				data.price = price;
				if (v.priceFrom) data.priceFrom = v.priceFrom;
			}
			if (v.interval !== base.interval) data.interval = v.interval;
			if (v.unit !== base.unit) data.unit = v.unit;
			if (v.nextCharge && v.nextCharge !== base.nextCharge) data.nextCharge = v.nextCharge;
			if (dayRule !== base.dayRule) data.dayRule = dayRule;
			if (weekendShift !== base.weekendShift) data.weekendShift = weekendShift;
			if (v.categoryId !== base.categoryId) data.categoryId = v.categoryId;
			if (v.paymentMethodId !== base.paymentMethodId) data.paymentMethodId = v.paymentMethodId;
			if (v.payerId !== base.payerId) data.payerId = v.payerId;
			if (v.trial !== base.trial) data.trialEndsOn = v.trial ? v.nextCharge : "";
			if (v.noticeDays !== base.noticeDays) data.noticeDays = Number(v.noticeDays) || 0;
			if (v.manual !== base.manual) data.renewal = v.manual ? "manual" : "auto";
			if (v.startedOn !== base.startedOn) data.startedOn = v.startedOn;
			if (v.endDate !== base.endDate) data.endDate = v.endDate;
			if (v.cancelUrl !== base.cancelUrl) data.cancelUrl = v.cancelUrl;
			if (v.account !== base.account) data.account = v.account;
			if (v.url !== base.url) data.url = v.url;
			if (v.notes !== base.notes) data.notes = v.notes;
			if (!same(v.reminderDays, base.reminderDays)) data.reminderDays = v.reminderDays;
			if (!same(v.shares, base.shares)) data.shares = shares;
			if (v.private !== base.private) data.private = v.private;
			if (v.autoConfirm !== base.autoConfirm) data.autoConfirm = v.autoConfirm;
			if (!same(v.tags, base.tags)) data.tags = v.tags;
			if (altPrice !== base.altPrice || (altPrice && v.altUnit !== base.altUnit)) {
				data.altPrice = altPrice;
				if (altPrice) data.altUnit = v.altUnit;
			}
			await update.mutateAsync({ data });
			await qc.invalidateQueries();
			toast.success(t("Saved"));
			onOpenChange(false);
		} catch (err) {
			setError(err instanceof ApiError ? err.detail : t("Could not save"));
		}
	};

	const dateLabel = v.manual ? t("Expires on") : v.trial ? t("Trial ends") : t("Next charge");
	return (
		<Dialog
			open={open}
			onOpenChange={onOpenChange}
			title={
				editing
					? t("Edit {0}", subscription?.name)
					: copyOf
						? t("Duplicate {0}", copyOf.name)
						: t("New subscription")
			}
			description={
				editing
					? undefined
					: copyOf
						? t("A new subscription with the same terms. It starts with no charges and no history.")
						: t("Exhale computes every charge from the first one.")
			}
		>
			<form className="grid gap-4" onSubmit={onSubmit}>
				<Field label={t("Service")}>
					<Input
						autoFocus={!editing}
						placeholder="Netflix, Spotify Family…"
						value={v.name}
						onChange={(e) => set("name", e.target.value)}
					/>
				</Field>
				<div className="grid grid-cols-[1fr_110px] gap-3">
					<Field label={v.trial ? t("Price after the trial") : t("Price per charge")}>
						<Input
							inputMode="decimal"
							placeholder="12.99"
							className="tnum"
							value={v.price}
							onChange={(e) => set("price", e.target.value)}
						/>
					</Field>
					<Field label={t("Currency")}>
						<Input
							maxLength={3}
							disabled={editing}
							value={v.currency}
							onChange={(e) => set("currency", e.target.value.toUpperCase())}
						/>
					</Field>
				</div>
				{priceChanged && (
					<Field label={t("New price starts on")} hint={t("Leave empty to apply it from the next charge.")}>
						<Input type="date" value={v.priceFrom} onChange={(e) => set("priceFrom", e.target.value)} />
					</Field>
				)}
				<div className="grid grid-cols-[80px_1fr_1fr] gap-3">
					<Field label={t("Every")}>
						<Input
							type="number"
							min={1}
							value={v.interval}
							onChange={(e) => set("interval", Number(e.target.value) || 1)}
						/>
					</Field>
					<Field label={t("Period")}>
						<Select value={v.unit} onChange={(e) => set("unit", e.target.value as Unit)}>
							{units.map((u) => (
								<option key={u.value} value={u.value}>
									{unitLabel(u.value, v.interval > 1)}
								</option>
							))}
						</Select>
					</Field>
					<Field label={dateLabel}>
						<Input type="date" value={v.nextCharge} onChange={(e) => set("nextCharge", e.target.value)} />
					</Field>
				</div>
				<div className="grid gap-3 sm:grid-cols-2">
					{monthly && v.nextCharge && (
						<Field label={t("Charged on")}>
							<Select value={dayRule} onChange={(e) => set("dayRule", e.target.value as DayRule)}>
								{dayRules.map((r) => (
									<option key={r} value={r}>
										{billingDayText(r, v.nextCharge)}
									</option>
								))}
							</Select>
						</Field>
					)}
					{shiftable && (
						<Field label={t("On weekends")}>
							<Select
								value={weekendShift}
								onChange={(e) => set("weekendShift", e.target.value as WeekendShift)}
							>
								<option value="none">{t("Keep the date")}</option>
								<option value="next">{t("Next business day")}</option>
								<option value="previous">{t("Previous business day")}</option>
							</Select>
						</Field>
					)}
				</div>
				{v.nextCharge && (
					<div className="rounded-lg border border-dashed border-line bg-raised/60 px-3 py-2.5 text-xs text-muted">
						<span className="font-medium text-fg">
							{v.manual ? t("Would expire: ") : t("Next charges: ")}
						</span>
						{preview.data?.dates?.map((d) => date(d)).join(" · ") ?? "…"}
					</div>
				)}
				<div className="grid grid-cols-2 gap-3">
					<Field label={t("Category")}>
						<Select value={v.categoryId} onChange={(e) => set("categoryId", e.target.value)}>
							<option value="">{t("None")}</option>
							{household.data?.categories?.map((c) => (
								<option key={c.id} value={c.id}>
									{c.name}
								</option>
							))}
						</Select>
					</Field>
					<Field label={t("Payment method")}>
						<Select value={v.paymentMethodId} onChange={(e) => set("paymentMethodId", e.target.value)}>
							<option value="">{t("None")}</option>
							{household.data?.paymentMethods?.map((p) => (
								<option key={p.id} value={p.id}>
									{p.name}
									{p.last4 ? ` ••${p.last4}` : ""}
								</option>
							))}
						</Select>
					</Field>
				</div>
				<Field
					label={t("Tags")}
					hint={t("Free labels to find and group subscriptions. Totals stay by category.")}
				>
					<TagInput value={v.tags} onChange={(x) => set("tags", x)} suggestions={tagSuggestions} />
				</Field>
				<div className="flex flex-wrap gap-2">
					<Chip on={v.trial} onClick={() => set("trial", !v.trial)}>
						{t("Free trial")}
					</Chip>
					<Chip on={v.manual} onClick={() => set("manual", !v.manual)}>
						{t("Expires unless renewed")}
					</Chip>
					<Chip on={v.noticeDays !== ""} onClick={() => set("noticeDays", v.noticeDays === "" ? "30" : "")}>
						{t("Needs notice to cancel")}
					</Chip>
				</div>
				{v.noticeDays !== "" && (
					<Field
						label={t("Days of notice")}
						hint={t("Exhale reminds you a week before the last day to cancel, and on that day.")}
					>
						<Input
							type="number"
							min={1}
							max={366}
							className="w-28"
							value={v.noticeDays}
							onChange={(e) => set("noticeDays", e.target.value)}
						/>
					</Field>
				)}
				<button
					type="button"
					onClick={() => setMore(!more)}
					aria-expanded={more}
					className="flex items-center gap-1.5 text-[13px] font-medium text-muted hover:text-fg"
				>
					<ChevronDown className={cn("size-4 transition-transform", more && "rotate-180")} />
					{t("More details")}
				</button>
				{more && (
					<div className="grid gap-4 border-t border-line pt-4">
						<Field label={t("Remind me")}>
							<div className="flex flex-wrap gap-2">
								{reminderOptions.map((d) => (
									<Chip
										key={d}
										on={v.reminderDays.includes(d)}
										onClick={() =>
											set(
												"reminderDays",
												(v.reminderDays.includes(d)
													? v.reminderDays.filter((x) => x !== d)
													: [...v.reminderDays, d]
												).sort((a, b) => b - a),
											)
										}
									>
										{reminderLabel(d)}
									</Chip>
								))}
							</div>
						</Field>
						<div className="grid grid-cols-2 gap-3">
							<Field label={t("Subscribed since")}>
								<Input type="date" value={v.startedOn} onChange={(e) => set("startedOn", e.target.value)} />
							</Field>
							<Field label={t("Ends on")} hint={t("For fixed-term contracts.")}>
								<Input type="date" value={v.endDate} onChange={(e) => set("endDate", e.target.value)} />
							</Field>
						</div>
						<Field
							label={t("Also offered at")}
							hint={t("Another price the provider offers, e.g. yearly. Exhale shows what switching saves.")}
						>
							<div className="grid grid-cols-[1fr_140px] gap-3">
								<Input
									inputMode="decimal"
									placeholder="99.00"
									className="tnum"
									aria-label={t("Other price")}
									value={v.altPrice}
									onChange={(e) => set("altPrice", e.target.value)}
								/>
								<Select
									aria-label={t("Other price period")}
									value={v.altUnit}
									onChange={(e) => set("altUnit", e.target.value as "month" | "year")}
								>
									<option value="year">{t("per year")}</option>
									<option value="month">{t("per month")}</option>
								</Select>
							</div>
						</Field>
						<div className="grid grid-cols-2 gap-3">
							<Field label={t("Cancel or manage page")}>
								<Input
									type="url"
									placeholder="https://…"
									value={v.cancelUrl}
									onChange={(e) => set("cancelUrl", e.target.value)}
								/>
							</Field>
							<Field label={t("Account")}>
								<Input
									placeholder={t("Login or email")}
									value={v.account}
									onChange={(e) => set("account", e.target.value)}
								/>
							</Field>
						</div>
						{members.length > 1 && (
							<>
								<Field label={t("Paid by")}>
									<Select value={v.payerId} onChange={(e) => set("payerId", e.target.value)}>
										{!editing && <option value="">{t("Me")}</option>}
										{members.map((m) => (
											<option key={m.id} value={m.id}>
												{m.name}
											</option>
										))}
									</Select>
								</Field>
								<Field
									label={t("Split the cost")}
									hint={t("Choose who shares it; equal parts unless you change them.")}
								>
									<div className="grid gap-2">
										{members.map((m) => (
											<span key={m.id} className="flex items-center gap-3 text-sm">
												<input
													type="checkbox"
													className="size-4 accent-[var(--accent)]"
													checked={(v.shares[m.id] ?? 0) > 0}
													onChange={(e) => set("shares", { ...v.shares, [m.id]: e.target.checked ? 1 : 0 })}
													aria-label={t("{0} shares it", m.name)}
												/>
												<span className="flex-1">{m.name}</span>
												{(v.shares[m.id] ?? 0) > 0 && (
													<Input
														type="number"
														min={1}
														className="h-8 w-20"
														aria-label={t("Parts for {0}", m.name)}
														value={v.shares[m.id]}
														onChange={(e) =>
															set("shares", { ...v.shares, [m.id]: Math.max(1, Number(e.target.value) || 1) })
														}
													/>
												)}
											</span>
										))}
									</div>
								</Field>
							</>
						)}
						<Field label={t("Notes")}>
							<textarea
								rows={2}
								className="w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm text-fg placeholder:text-faint focus:border-accent focus:outline-2 focus:outline-accent/25"
								value={v.notes}
								onChange={(e) => set("notes", e.target.value)}
							/>
						</Field>
						<div className="grid gap-2 text-sm">
							<Check on={v.autoConfirm} onChange={(x) => set("autoConfirm", x)}>
								{t("Mark each charge paid on its day, without asking")}
							</Check>
							{members.length > 1 && (
								<Check on={v.private} onChange={(x) => set("private", x)}>
									{t("Private: only its creator and payer see it")}
								</Check>
							)}
						</div>
					</div>
				)}
				{error && <p className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
				<div className="mt-1 flex justify-end gap-2">
					<Button onClick={() => onOpenChange(false)}>{t("Cancel")}</Button>
					<Button type="submit" variant="primary" disabled={create.isPending || update.isPending}>
						{create.isPending || update.isPending
							? t("Saving…")
							: editing
								? t("Save")
								: t("Add subscription")}
					</Button>
				</div>
			</form>
		</Dialog>
	);
}

/** Kept for callers that only create. */
export function NewSubscription(props: { open: boolean; onOpenChange: (o: boolean) => void }) {
	return <SubscriptionDialog {...props} />;
}

export function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: ReactNode }) {
	return (
		<button
			type="button"
			aria-pressed={on}
			onClick={onClick}
			className={cn(
				"rounded-full border px-3 py-1 text-xs font-medium transition-colors",
				on ? "border-fg bg-fg text-bg" : "border-line text-muted hover:border-fg/40 hover:text-fg",
			)}
		>
			{children}
		</button>
	);
}

function Check({
	on,
	onChange,
	children,
}: {
	on: boolean;
	onChange: (v: boolean) => void;
	children: ReactNode;
}) {
	return (
		<label className="flex items-center gap-2.5 text-muted">
			<input
				type="checkbox"
				className="size-4 accent-[var(--accent)]"
				checked={on}
				onChange={(e) => onChange(e.target.checked)}
			/>
			{children}
		</label>
	);
}
