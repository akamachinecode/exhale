import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, Pencil, Plus, Trash2, X } from "lucide-react";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select } from "@/components/ui/field";
import {
	type Member,
	type PaymentMethod,
	type SavePaymentMethodInputKind,
	useCreateCategory,
	useDeleteCategory,
	useDeletePaymentMethod,
	useGetHousehold,
	useListSubscriptions,
	useMembersRemove,
	useMembersRename,
	useMembersSetRole,
	useSavePaymentMethod,
	useUpdateCategory,
	useUpdateHousehold,
} from "@/gen/api";
import { ApiError } from "@/lib/fetcher";

const errText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.detail : fallback);

const kinds = [
	{ value: "card", label: "Card" },
	{ value: "bank", label: "Bank account" },
	{ value: "paypal", label: "PayPal" },
	{ value: "other", label: "Other" },
];

/** "2027-08-31" → "2027-08" for a month input, and back to the last day. */
const toMonth = (d?: string) => d?.slice(0, 7) ?? "";
function lastDay(month: string) {
	if (!month) return "";
	const [y, m] = month.split("-").map(Number);
	return `${month}-${String(new Date(Date.UTC(y, m, 0)).getUTCDate()).padStart(2, "0")}`;
}

function daysUntil(iso: string, today: string) {
	return Math.round((Date.parse(iso) - Date.parse(today)) / 86_400_000);
}

/** Payment methods: add, edit, and delete moving their subscriptions. */
export function PaymentMethods() {
	const household = useGetHousehold();
	const subs = useListSubscriptions({ status: "active" });
	const [editing, setEditing] = useState<string>();
	const [deleting, setDeleting] = useState<string>();
	const methods = household.data?.paymentMethods ?? [];
	const today = household.data?.today ?? "";
	const count = (id: string) =>
		(subs.data?.subscriptions ?? []).filter((s) => s.paymentMethodId === id).length;

	return (
		<div className="grid gap-3">
			{methods.length === 0 && editing !== "new" && (
				<p className="text-sm text-muted">Add the cards and accounts that pay your subscriptions.</p>
			)}
			<ul className="divide-y divide-line rounded-lg border border-line empty:hidden">
				{methods.map((pm) =>
					editing === pm.id ? (
						<li key={pm.id} className="p-3">
							<MethodForm method={pm} onDone={() => setEditing(undefined)} />
						</li>
					) : deleting === pm.id ? (
						<li key={pm.id} className="p-3">
							<DeleteMethod
								method={pm}
								others={methods.filter((o) => o.id !== pm.id)}
								count={count(pm.id)}
								onDone={() => setDeleting(undefined)}
							/>
						</li>
					) : (
						<li key={pm.id} className="flex items-center gap-3 px-3 py-2.5 text-sm">
							<span className="min-w-0 flex-1">
								<span className="block truncate font-medium">
									{pm.name}
									{pm.last4 && <span className="tnum text-muted"> ••{pm.last4}</span>}
								</span>
								<span className="block text-xs text-muted">
									{kinds.find((k) => k.value === pm.kind)?.label} · {count(pm.id)} subscription
									{count(pm.id) === 1 ? "" : "s"}
									{pm.expiresOn && ` · expires ${pm.expiresOn.slice(5, 7)}/${pm.expiresOn.slice(2, 4)}`}
								</span>
							</span>
							{pm.expiresOn && today && daysUntil(pm.expiresOn, today) <= 45 && (
								<span className="flex items-center gap-1 text-xs text-warn">
									<AlertTriangle className="size-3.5" />
									{daysUntil(pm.expiresOn, today) < 0 ? "Expired" : "Expires soon"}
								</span>
							)}
							<Button
								size="icon"
								variant="ghost"
								aria-label={`Edit ${pm.name}`}
								onClick={() => setEditing(pm.id)}
							>
								<Pencil />
							</Button>
							<Button
								size="icon"
								variant="ghost"
								aria-label={`Delete ${pm.name}`}
								onClick={() => setDeleting(pm.id)}
							>
								<Trash2 />
							</Button>
						</li>
					),
				)}
			</ul>
			{editing === "new" ? (
				<div className="rounded-lg border border-line p-3">
					<MethodForm onDone={() => setEditing(undefined)} />
				</div>
			) : (
				<div>
					<Button size="sm" onClick={() => setEditing("new")}>
						<Plus /> Add a payment method
					</Button>
				</div>
			)}
		</div>
	);
}

function MethodForm({ method, onDone }: { method?: PaymentMethod; onDone: () => void }) {
	const qc = useQueryClient();
	const save = useSavePaymentMethod();
	const [name, setName] = useState(method?.name ?? "");
	const [kind, setKind] = useState(method?.kind ?? "card");
	const [last4, setLast4] = useState(method?.last4 ?? "");
	const [expires, setExpires] = useState(toMonth(method?.expiresOn));
	const [error, setError] = useState<string>();
	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setError(undefined);
		try {
			await save.mutateAsync({
				data: {
					id: method?.id,
					name: name.trim(),
					kind: kind as SavePaymentMethodInputKind,
					last4: last4.trim(),
					expiresOn: lastDay(expires),
					ownerId: method?.ownerId,
				},
			});
			await qc.invalidateQueries();
			onDone();
		} catch (err) {
			setError(errText(err, "Could not save"));
		}
	};
	return (
		<form onSubmit={onSubmit} className="grid gap-3">
			<div className="grid gap-3 sm:grid-cols-[1.4fr_1fr_90px_130px]">
				<Field label="Name">
					<Input
						autoFocus
						required
						placeholder="Revolut, Visa…"
						value={name}
						onChange={(e) => setName(e.target.value)}
					/>
				</Field>
				<Field label="Kind">
					<Select value={kind} onChange={(e) => setKind(e.target.value)}>
						{kinds.map((k) => (
							<option key={k.value} value={k.value}>
								{k.label}
							</option>
						))}
					</Select>
				</Field>
				<Field label="Last 4">
					<Input
						inputMode="numeric"
						maxLength={4}
						className="tnum"
						value={last4}
						onChange={(e) => setLast4(e.target.value.replace(/\D/g, ""))}
					/>
				</Field>
				<Field label="Expires">
					<Input type="month" value={expires} onChange={(e) => setExpires(e.target.value)} />
				</Field>
			</div>
			{error && <p className="text-xs text-bad">{error}</p>}
			<div className="flex gap-2">
				<Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
					{method ? "Save" : "Add"}
				</Button>
				<Button size="sm" onClick={onDone}>
					Cancel
				</Button>
			</div>
		</form>
	);
}

function DeleteMethod({
	method,
	others,
	count,
	onDone,
}: {
	method: PaymentMethod;
	others: PaymentMethod[];
	count: number;
	onDone: () => void;
}) {
	const qc = useQueryClient();
	const del = useDeletePaymentMethod();
	const [moveTo, setMoveTo] = useState(others[0]?.id ?? "");
	const [error, setError] = useState<string>();
	const run = async () => {
		setError(undefined);
		try {
			const r = await del.mutateAsync({ data: { id: method.id, moveTo: count ? moveTo : "" } });
			await qc.invalidateQueries();
			toast.success(
				r.moved ? `Deleted; ${r.moved} subscription${r.moved === 1 ? "" : "s"} moved` : "Deleted",
			);
			onDone();
		} catch (err) {
			setError(errText(err, "Could not delete"));
		}
	};
	return (
		<div className="grid gap-3 text-sm">
			<p className="font-medium">Delete {method.name}?</p>
			{count > 0 && (
				<Field
					label={`It pays ${count} subscription${count === 1 ? "" : "s"}. Move ${count === 1 ? "it" : "them"} to`}
				>
					<Select value={moveTo} onChange={(e) => setMoveTo(e.target.value)}>
						{others.map((o) => (
							<option key={o.id} value={o.id}>
								{o.name}
								{o.last4 ? ` ••${o.last4}` : ""}
							</option>
						))}
						<option value="">No payment method</option>
					</Select>
				</Field>
			)}
			{error && <p className="text-xs text-bad">{error}</p>}
			<div className="flex gap-2">
				<Button size="sm" variant="danger" disabled={del.isPending} onClick={run}>
					Delete
				</Button>
				<Button size="sm" onClick={onDone}>
					Keep it
				</Button>
			</div>
		</div>
	);
}

/** Categories: add, rename, delete. */
export function Categories() {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const create = useCreateCategory();
	const update = useUpdateCategory();
	const del = useDeleteCategory();
	const [name, setName] = useState("");
	const [editing, setEditing] = useState<{ id: string; name: string }>();
	const cats = household.data?.categories ?? [];
	const run = async (fn: () => Promise<unknown>) => {
		try {
			await fn();
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, "Could not save"));
		}
	};
	return (
		<div className="grid gap-3">
			<ul className="flex flex-wrap gap-2">
				{cats.map((c) =>
					editing?.id === c.id ? (
						<li key={c.id}>
							<form
								className="flex items-center gap-1"
								onSubmit={(e) => {
									e.preventDefault();
									run(() => update.mutateAsync({ data: { id: c.id, name: editing.name } })).then(() =>
										setEditing(undefined),
									);
								}}
							>
								<Input
									autoFocus
									className="h-8 w-40"
									aria-label="Category name"
									value={editing.name}
									onChange={(e) => setEditing({ id: c.id, name: e.target.value })}
								/>
								<Button type="submit" size="icon" variant="ghost" aria-label="Save">
									<Check />
								</Button>
								<Button size="icon" variant="ghost" aria-label="Cancel" onClick={() => setEditing(undefined)}>
									<X />
								</Button>
							</form>
						</li>
					) : (
						<li
							key={c.id}
							className="group flex items-center gap-1 rounded-full border border-line py-0.5 pr-1 pl-3 text-[13px]"
						>
							{c.name}
							<button
								type="button"
								aria-label={`Rename ${c.name}`}
								onClick={() => setEditing({ id: c.id, name: c.name })}
								className="rounded-full p-1 text-faint hover:bg-raised hover:text-fg"
							>
								<Pencil className="size-3" />
							</button>
							<button
								type="button"
								aria-label={`Delete ${c.name}`}
								onClick={() => run(() => del.mutateAsync({ data: { id: c.id } }))}
								className="rounded-full p-1 text-faint hover:bg-raised hover:text-bad"
							>
								<X className="size-3" />
							</button>
						</li>
					),
				)}
			</ul>
			<form
				className="flex gap-2"
				onSubmit={(e) => {
					e.preventDefault();
					if (!name.trim()) return;
					run(() => create.mutateAsync({ data: { name: name.trim() } })).then(() => setName(""));
				}}
			>
				<Input
					className="max-w-56"
					placeholder="New category"
					aria-label="New category"
					value={name}
					onChange={(e) => setName(e.target.value)}
				/>
				<Button type="submit" disabled={create.isPending || !name.trim()}>
					Add
				</Button>
			</form>
			<p className="text-xs text-muted">Deleting a category leaves its subscriptions without one.</p>
		</div>
	);
}

/** The day the household gets paid, to show what is due before it. */
export function Payday() {
	const household = useGetHousehold();
	const qc = useQueryClient();
	const update = useUpdateHousehold();
	const current = household.data?.household.payday ?? 0;
	const save = async (payday: number) => {
		try {
			await update.mutateAsync({ data: { payday } });
			await qc.invalidateQueries();
		} catch (err) {
			toast.error(errText(err, "Could not save"));
		}
	};
	return (
		<Field label="Payday" hint="Exhale shows how much is due before you are paid again.">
			<Select className="max-w-56" value={current} onChange={(e) => save(Number(e.target.value))}>
				<option value={0}>Not set</option>
				{Array.from({ length: 31 }, (_, i) => i + 1).map((d) => (
					<option key={d} value={d}>
						{d === 31 ? "Last day of the month" : `Day ${d}`}
					</option>
				))}
			</Select>
		</Field>
	);
}

/** One person sharing this Exhale: rename, make owner, remove or leave. */
export function MemberRow({
	member,
	me,
	isOwner,
	owners,
}: {
	member: Member;
	me?: string;
	isOwner: boolean;
	owners: number;
}) {
	const qc = useQueryClient();
	const rename = useMembersRename();
	const role = useMembersSetRole();
	const remove = useMembersRemove();
	const [name, setName] = useState<string>();
	const [confirming, setConfirming] = useState(false);
	const self = member.id === me;
	const lastOwner = member.role === "owner" && owners <= 1;
	const run = async (fn: () => Promise<unknown>, done?: string) => {
		try {
			await fn();
			await qc.invalidateQueries();
			if (done) toast.success(done);
		} catch (err) {
			toast.error(errText(err, "Could not save"));
		}
	};
	if (confirming) {
		return (
			<li className="grid gap-2 px-3 py-2.5 text-sm">
				<p>
					<span className="font-medium">{self ? "Leave this Exhale?" : `Remove ${member.name}?`}</span>{" "}
					<span className="text-muted">
						{self
							? "You lose access. What you pay for stays, without a payer."
							: "They lose access. What they pay for passes to you."}
					</span>
				</p>
				<div className="flex gap-2">
					<Button
						size="sm"
						variant="danger"
						disabled={remove.isPending}
						onClick={() =>
							run(
								() => remove.mutateAsync({ data: { id: member.id } }),
								self ? undefined : `${member.name} removed`,
							)
						}
					>
						{self ? "Leave" : "Remove"}
					</Button>
					<Button size="sm" onClick={() => setConfirming(false)}>
						Cancel
					</Button>
				</div>
			</li>
		);
	}
	return (
		<li className="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5">
			<div className="min-w-0 flex-1">
				{name !== undefined ? (
					<form
						className="flex items-center gap-1"
						onSubmit={(e) => {
							e.preventDefault();
							run(() => rename.mutateAsync({ data: { id: member.id, name } })).then(() => setName(undefined));
						}}
					>
						<Input
							autoFocus
							className="h-8 max-w-52"
							aria-label="Name"
							value={name}
							onChange={(e) => setName(e.target.value)}
						/>
						<Button type="submit" size="icon" variant="ghost" aria-label="Save">
							<Check />
						</Button>
						<Button size="icon" variant="ghost" aria-label="Cancel" onClick={() => setName(undefined)}>
							<X />
						</Button>
					</form>
				) : (
					<>
						<p className="truncate text-sm font-medium">
							{member.name}
							{self && <span className="font-normal text-muted"> (you)</span>}
						</p>
						{member.email && <p className="truncate text-xs text-muted">{member.email}</p>}
					</>
				)}
			</div>
			<Badge>{member.role}</Badge>
			{name === undefined && (self || isOwner) && (
				<Button
					size="icon"
					variant="ghost"
					aria-label={`Rename ${member.name}`}
					onClick={() => setName(member.name)}
				>
					<Pencil />
				</Button>
			)}
			{isOwner && !self && (
				<Button
					size="sm"
					variant="ghost"
					disabled={role.isPending}
					onClick={() =>
						run(() =>
							role.mutateAsync({
								data: { id: member.id, role: member.role === "owner" ? "member" : "owner" },
							}),
						)
					}
				>
					{member.role === "owner" ? "Make member" : "Make owner"}
				</Button>
			)}
			{(isOwner || self) && !lastOwner && (
				<Button
					size="icon"
					variant="ghost"
					aria-label={self ? "Leave" : `Remove ${member.name}`}
					onClick={() => setConfirming(true)}
				>
					<Trash2 />
				</Button>
			)}
		</li>
	);
}
