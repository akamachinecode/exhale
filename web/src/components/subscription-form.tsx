import { useForm, useStore } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/field";
import { useCreateSubscription, useGetHousehold, usePreviewSchedule } from "@/gen/api";
import { ApiError } from "@/lib/fetcher";
import { date, todayISO } from "@/lib/utils";

const units = [
	{ value: "month", label: "Month" },
	{ value: "year", label: "Year" },
	{ value: "week", label: "Week" },
	{ value: "day", label: "Day" },
] as const;

export function NewSubscription({
	open,
	onOpenChange,
}: {
	open: boolean;
	onOpenChange: (o: boolean) => void;
}) {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const navigate = useNavigate();
	const [serverError, setServerError] = useState<string>();
	const create = useCreateSubscription();
	const form = useForm({
		defaultValues: {
			name: "",
			price: "",
			currency: household.data?.household.currency ?? "EUR",
			interval: 1,
			unit: "month" as (typeof units)[number]["value"],
			firstCharge: todayISO(),
			categoryId: "",
			paymentMethodId: "",
		},
		onSubmit: async ({ value }) => {
			setServerError(undefined);
			try {
				const res = await create.mutateAsync({ data: { ...value, price: value.price.replace(",", ".") } });
				await qc.invalidateQueries();
				toast.success(`${res.subscription.name} added`);
				onOpenChange(false);
				form.reset();
				navigate({ to: "/subscriptions/$id", params: { id: res.subscription.id } });
			} catch (e) {
				setServerError(e instanceof ApiError ? e.detail : "Could not save");
			}
		},
	});
	const rule = useStore(form.store, (s) => ({
		interval: s.values.interval,
		unit: s.values.unit,
		firstCharge: s.values.firstCharge,
	}));
	const preview = usePreviewSchedule();
	const { mutate } = preview;
	// The web never computes charge dates: the engine previews them (ADR 0013).
	useEffect(() => {
		if (!open || !rule.firstCharge) return;
		const t = setTimeout(() => mutate({ data: { ...rule, count: 4 } }), 250);
		return () => clearTimeout(t);
	}, [open, rule.interval, rule.unit, rule.firstCharge, mutate, rule]);

	return (
		<Dialog
			open={open}
			onOpenChange={onOpenChange}
			title="New subscription"
			description="Exhale computes every charge from the first one."
		>
			<form
				className="grid gap-4"
				onSubmit={(e) => {
					e.preventDefault();
					form.handleSubmit();
				}}
			>
				<form.Field
					name="name"
					validators={{ onChange: ({ value }) => (value.trim() ? undefined : "Give it a name") }}
				>
					{(f) => (
						<Field label="Service" error={f.state.meta.isTouched ? f.state.meta.errors[0] : undefined}>
							<Input
								autoFocus
								placeholder="Netflix, Spotify Family…"
								value={f.state.value}
								onBlur={f.handleBlur}
								onChange={(e) => f.handleChange(e.target.value)}
							/>
						</Field>
					)}
				</form.Field>
				<div className="grid grid-cols-[1fr_110px] gap-3">
					<form.Field
						name="price"
						validators={{
							onChange: ({ value }) => (/^\d+([.,]\d{1,3})?$/.test(value.trim()) ? undefined : "e.g. 12.99"),
						}}
					>
						{(f) => (
							<Field
								label="Price per charge"
								error={f.state.meta.isTouched ? f.state.meta.errors[0] : undefined}
							>
								<Input
									inputMode="decimal"
									placeholder="12.99"
									className="tnum"
									value={f.state.value}
									onBlur={f.handleBlur}
									onChange={(e) => f.handleChange(e.target.value)}
								/>
							</Field>
						)}
					</form.Field>
					<form.Field name="currency">
						{(f) => (
							<Field label="Currency">
								<Input
									maxLength={3}
									value={f.state.value}
									onChange={(e) => f.handleChange(e.target.value.toUpperCase())}
								/>
							</Field>
						)}
					</form.Field>
				</div>
				<div className="grid grid-cols-[80px_1fr_1fr] gap-3">
					<form.Field name="interval">
						{(f) => (
							<Field label="Every">
								<Input
									type="number"
									min={1}
									value={f.state.value}
									onChange={(e) => f.handleChange(Number(e.target.value) || 1)}
								/>
							</Field>
						)}
					</form.Field>
					<form.Field name="unit">
						{(f) => (
							<Field label="Period">
								<Select
									value={f.state.value}
									onChange={(e) => f.handleChange(e.target.value as typeof f.state.value)}
								>
									{units.map((u) => (
										<option key={u.value} value={u.value}>
											{u.label}
											{rule.interval > 1 ? "s" : ""}
										</option>
									))}
								</Select>
							</Field>
						)}
					</form.Field>
					<form.Field name="firstCharge">
						{(f) => (
							<Field label="Next charge">
								<Input type="date" value={f.state.value} onChange={(e) => f.handleChange(e.target.value)} />
							</Field>
						)}
					</form.Field>
				</div>
				<div className="rounded-lg border border-dashed border-line bg-raised/60 px-3 py-2.5 text-xs text-muted">
					<span className="font-medium text-fg">Next charges: </span>
					{preview.data?.dates?.map((d) => date(d)).join(" · ") ?? "…"}
				</div>
				<div className="grid grid-cols-2 gap-3">
					<form.Field name="categoryId">
						{(f) => (
							<Field label="Category">
								<Select value={f.state.value} onChange={(e) => f.handleChange(e.target.value)}>
									<option value="">None</option>
									{household.data?.categories?.map((c) => (
										<option key={c.id} value={c.id}>
											{c.name}
										</option>
									))}
								</Select>
							</Field>
						)}
					</form.Field>
					<form.Field name="paymentMethodId">
						{(f) => (
							<Field label="Payment method">
								<Select value={f.state.value} onChange={(e) => f.handleChange(e.target.value)}>
									<option value="">None</option>
									{household.data?.paymentMethods?.map((p) => (
										<option key={p.id} value={p.id}>
											{p.name}
											{p.last4 ? ` ••${p.last4}` : ""}
										</option>
									))}
								</Select>
							</Field>
						)}
					</form.Field>
				</div>
				{serverError && <p className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{serverError}</p>}
				<div className="mt-1 flex justify-end gap-2">
					<Button onClick={() => onOpenChange(false)}>Cancel</Button>
					<form.Subscribe selector={(s) => [s.canSubmit, s.isSubmitting] as const}>
						{([canSubmit, submitting]) => (
							<Button type="submit" variant="primary" disabled={!canSubmit || submitting}>
								{submitting ? "Saving…" : "Add subscription"}
							</Button>
						)}
					</form.Subscribe>
				</div>
			</form>
		</Dialog>
	);
}
