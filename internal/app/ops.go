package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/akamachinecode/exhale/internal/domain"
)

// Meta describes a use case for every adapter that exposes it: the REST API
// (huma), the MCP server and the in-app assistant (ADR 0018).
type Meta struct {
	Name        string // tool name, snake_case
	Title       string
	Description string
	Write       bool // changes data; needs write scope
	Destructive bool // deletes or cancels; agents need confirmation
	Idempotent  bool // repeating the call has no further effect
	Method      string
	Path        string // REST path under /api/v1
	Tag         string
}

// Op is a typed use case.
type Op[In, Out any] struct {
	Meta
	Run func(s *Service, ctx context.Context, a Actor, in In) (Out, error)
}

// Binding is the untyped view of an Op, for adapters that work with JSON.
type Binding struct {
	Meta
	In, Out reflect.Type
	Invoke  func(s *Service, ctx context.Context, a Actor, raw json.RawMessage) (any, error)
}

// Binding returns the untyped view of o.
func (o Op[In, Out]) Binding() Binding {
	return Binding{
		Meta: o.Meta, In: reflect.TypeFor[In](), Out: reflect.TypeFor[Out](),
		Invoke: func(s *Service, ctx context.Context, a Actor, raw json.RawMessage) (any, error) {
			var in In
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, fmt.Errorf("invalid arguments for %s: %w", o.Name, err)
				}
			}
			return o.Run(s, ctx, a, in)
		},
	}
}

// Use cases exposed to every adapter. Adding one here makes it available to
// AI agents; the parity test fails if an adapter misses one.
var (
	OpGetContext = Op[struct{}, AgentContext]{Meta{
		Name: "get_context", Title: "Read the household briefing", Tag: "Context", Method: "GET", Path: "/context",
		Description: "Start here. Returns a live Markdown briefing: today's date and time zone, members, every subscription with ids, the next 30 days of charges, what needs attention, and the rules for changing anything safely.",
	}, (*Service).BuildContext}
	OpGetHousehold = Op[struct{}, HouseholdInfo]{Meta{
		Name: "get_household", Title: "Get household reference data", Tag: "Household", Method: "GET", Path: "/household",
		Description: "Household settings, members, categories and payment methods with their ids.",
	}, (*Service).GetHousehold}
	OpUpdateHousehold = Op[UpdateHouseholdInput, HouseholdInfo]{Meta{
		Name: "update_household", Title: "Change household preferences", Tag: "Household", Method: "POST", Path: "/household", Write: true, Idempotent: true,
		Description: "Changes the display currency (totals convert into it), time zone, reminder language, name, or default reminder time (HH:MM). Owners only.",
	}, (*Service).UpdateHousehold}
	OpListSubscriptions = Op[ListSubscriptionsInput, SubscriptionList]{Meta{
		Name: "list_subscriptions", Title: "List subscriptions", Tag: "Subscriptions", Method: "GET", Path: "/subscriptions",
		Description: "Lists subscriptions with price, cadence, next charge, monthly cost, category and payment method. Filter by status or name.",
	}, (*Service).ListSubscriptions}
	OpGetSubscription = Op[GetSubscriptionInput, SubscriptionDetail]{Meta{
		Name: "get_subscription", Title: "Get a subscription", Tag: "Subscriptions", Method: "GET", Path: "/subscriptions/{id}",
		Description: "One subscription with its upcoming charges (with charge ids) and its charge history.",
	}, (*Service).GetSubscription}
	OpCreateSubscription = Op[CreateSubscriptionInput, SubscriptionDetail]{Meta{
		Name: "create_subscription", Title: "Create a subscription", Tag: "Subscriptions", Method: "POST", Path: "/subscriptions", Write: true,
		Description: "Adds a recurring subscription and computes its upcoming charges. Also covers a free trial (trialEndsOn + the price it becomes), a notice period (noticeDays), things that expire unless renewed (renewal=manual) and subscriptions that already ended (status=cancelled). Use dryRun to preview the charge dates first.",
	}, (*Service).CreateSubscription}
	OpUpdateSubscription = Op[UpdateSubscriptionInput, SubscriptionDetail]{Meta{
		Name: "update_subscription", Title: "Update a subscription", Tag: "Subscriptions", Method: "POST", Path: "/subscriptions/update", Write: true, Idempotent: true,
		Description: "Changes name, price (from the next open charge on, or from priceFrom for a change announced in advance), billing cycle or day (interval, unit, nextCharge: from the next charge on), end date, category, payment method, payer, cost shares, privacy, URL, notes, auto-confirm, reminders, or the contract terms (start date, trial end, notice period, renewal, cancel link, account). Only the fields you pass change.",
	}, (*Service).UpdateSubscription}
	OpSetSubscriptionStatus = Op[SetSubscriptionStatusInput, SubscriptionDetail]{Meta{
		Name: "set_subscription_status", Title: "Pause, resume or cancel", Tag: "Subscriptions", Method: "POST", Path: "/subscriptions/status", Write: true, Destructive: true, Idempotent: true,
		Description: "Pauses, resumes or cancels a subscription. cancel_at_period_end keeps the service until the day before the next charge; cancel_now stops future charges immediately.",
	}, (*Service).SetSubscriptionStatus}
	OpDeleteSubscription = Op[DeleteSubscriptionInput, DeleteResult]{Meta{
		Name: "delete_subscription", Title: "Delete a subscription", Tag: "Subscriptions", Method: "POST", Path: "/subscriptions/delete", Write: true, Destructive: true,
		Description: "Permanently deletes a subscription and its charges. Prefer set_subscription_status to cancel. Can be reverted with undo.",
	}, (*Service).DeleteSubscription}
	OpListCharges = Op[ListChargesInput, ChargeList]{Meta{
		Name: "list_charges", Title: "List charges by date", Tag: "Charges", Method: "GET", Path: "/charges",
		Description: "Charges due between two dates (default: the next 30 days), with ids, amounts, status and whether they were moved.",
	}, (*Service).ListCharges}
	OpCalendar = Op[CalendarInput, CalendarView]{Meta{
		Name: "calendar", Title: "Calendar of dates", Tag: "Charges", Method: "GET", Path: "/calendar",
		Description: "Every dated event in a period (default: the next year): charges, including the ones the schedule will produce further ahead, free trials ending, things expiring and last days to cancel.",
	}, (*Service).Calendar}
	OpNeedsAttention = Op[struct{}, AttentionList]{Meta{
		Name: "needs_attention", Title: "What needs attention", Tag: "Charges", Method: "GET", Path: "/attention",
		Description: "Charges whose date passed without confirmation, payment methods about to expire, last days to cancel and free trials about to charge.",
	}, (*Service).NeedsAttention}
	OpChangeChargeDate = Op[ChangeChargeDateInput, ChargeResult]{Meta{
		Name: "change_charge_date", Title: "Move a charge", Tag: "Charges", Method: "POST", Path: "/charges/move", Write: true, Idempotent: true,
		Description: "Moves a charge to another date. scope=this (default) for a one-off delay; scope=following to change the billing day from this charge on.",
	}, (*Service).ChangeChargeDate}
	OpChangeChargeAmount = Op[ChangeChargeAmountInput, ChargeResult]{Meta{
		Name: "change_charge_amount", Title: "Change a charge amount", Tag: "Charges", Method: "POST", Path: "/charges/amount", Write: true, Idempotent: true,
		Description: "Changes the amount of a charge. scope=following (default) also changes the subscription price for later charges; scope=this only this charge.",
	}, (*Service).ChangeChargeAmount}
	OpSetChargeStatus = Op[SetChargeStatusInput, ChargeResult]{Meta{
		Name: "set_charge_status", Title: "Mark a charge paid, failed or skipped", Tag: "Charges", Method: "POST", Path: "/charges/status", Write: true, Idempotent: true,
		Description: "Resolves a charge: paid (optionally with the amount the bank actually took, in any currency), failed, skipped, or pending to reopen it.",
	}, (*Service).SetChargeStatus}
	OpDecideRenewal = Op[DecideRenewalInput, ChargeResult]{Meta{
		Name: "decide_renewal", Title: "Keep a renewal", Tag: "Charges", Method: "POST", Path: "/charges/decide", Write: true, Idempotent: true,
		Description: "For subscriptions with a notice period: records that the member wants to keep this renewal, so Exhale stops asking before the last day to cancel. To cancel instead, use set_subscription_status.",
	}, (*Service).DecideRenewal}
	OpPreviewSchedule = Op[PreviewScheduleInput, SchedulePreview]{Meta{
		Name: "preview_schedule", Title: "Preview charge dates", Tag: "Charges", Method: "POST", Path: "/schedule/preview", Idempotent: true,
		Description: "Computes the charge dates of a billing rule with Exhale's engine (month-end and leap-day aware) without saving anything.",
	}, (*Service).PreviewSchedule}
	OpSummary = Op[struct{}, SpendingSummary]{Meta{
		Name: "spending_summary", Title: "Spending summary", Tag: "Insights", Method: "GET", Path: "/summary",
		Description: "Monthly and yearly recurring spend per currency, spend per category and payment method, how much to set aside each month for yearly bills, what is due before the next payday, and the charges of the next 7 days.",
	}, (*Service).Summary}
	OpSpendHistory = Op[SpendingHistoryInput, SpendingHistory]{Meta{
		Name: "spending_history", Title: "Real spend per month", Tag: "Insights", Method: "GET", Path: "/spending/history",
		Description: "What was really paid each month (from the charge ledger, not from current prices), what is expected in the coming months, and lifetime paid per subscription.",
	}, (*Service).SpendHistory}
	OpBalances = Op[BalancesInput, Balances]{Meta{
		Name: "month_balances", Title: "Who owes whom", Tag: "Insights", Method: "GET", Path: "/balances",
		Description: "For subscriptions with cost shares: what each member paid and should carry in a month, and the fewest payments that settle it.",
	}, (*Service).MonthBalances}
	OpAudit = Op[struct{}, AuditReport]{Meta{
		Name: "audit", Title: "Audit the subscriptions", Tag: "Insights", Method: "GET", Path: "/audit",
		Description: "Facts worth acting on, computed not guessed: duplicates, price increases in the last year, free trials about to charge, last days to cancel, things about to expire, big charges coming, cards about to expire with the subscriptions that depend on them, unconfirmed charges and subscriptions marked as not used.",
	}, (*Service).Audit}
	OpSavePaymentMethod = Op[SavePaymentMethodInput, PaymentMethodOut]{Meta{
		Name: "save_payment_method", Title: "Create or update a payment method", Tag: "Household", Method: "POST", Path: "/payment-methods", Write: true,
		Description: "Creates a payment method (no id) or updates one (with id): name, kind, last four digits, expiry date and owner.",
	}, savePaymentMethod}
	OpReassignPaymentMethod = Op[ReassignPaymentMethodInput, ReassignResult]{Meta{
		Name: "reassign_payment_method", Title: "Move subscriptions to another payment method", Tag: "Household", Method: "POST", Path: "/payment-methods/reassign", Write: true, Idempotent: true,
		Description: "Moves every subscription paid with one method to another in one step, e.g. when a card is about to expire.",
	}, (*Service).ReassignPaymentMethod}
	OpDeletePaymentMethod = Op[DeletePaymentMethodInput, ReassignResult]{Meta{
		Name: "delete_payment_method", Title: "Delete a payment method", Tag: "Household", Method: "POST", Path: "/payment-methods/delete", Write: true, Destructive: true,
		Description: "Deletes a payment method. Pass moveTo so its subscriptions move to another method instead of being left without one.",
	}, (*Service).DeletePaymentMethod}
	OpUpdateCategory = Op[UpdateCategoryInput, CategoryOut]{Meta{
		Name: "update_category", Title: "Rename a category", Tag: "Household", Method: "POST", Path: "/categories/update", Write: true, Idempotent: true,
		Description: "Renames or recolours a category.",
	}, updateCategory}
	OpDeleteCategory = Op[DeleteCategoryInput, DeleteResult]{Meta{
		Name: "delete_category", Title: "Delete a category", Tag: "Household", Method: "POST", Path: "/categories/delete", Write: true, Destructive: true,
		Description: "Deletes a category; its subscriptions become uncategorized.",
	}, (*Service).DeleteCategory}
	OpCreateCategory = Op[CreateCategoryInput, CategoryOut]{Meta{
		Name: "create_category", Title: "Create a category", Tag: "Household", Method: "POST", Path: "/categories", Write: true,
		Description: "Adds a category for grouping subscriptions.",
	}, createCategory}
	OpExport = Op[struct{}, ExportData]{Meta{
		Name: "export_data", Title: "Export everything", Tag: "Data", Method: "GET", Path: "/export",
		Description: "Everything the household keeps in Exhale in one JSON document: settings, members, categories, payment methods and every subscription with all its charges.",
	}, (*Service).Export}
	OpImport = Op[ImportInput, ImportResult]{Meta{
		Name: "import_subscriptions", Title: "Import subscriptions", Tag: "Data", Method: "POST", Path: "/import", Write: true,
		Description: "Creates many subscriptions in one step, e.g. from a bank statement, a spreadsheet or another app. Categories, payment methods and payers are given by name (missing categories and payment methods are created). Items that duplicate an existing subscription or fail validation are skipped and reported. Always call it first with dryRun and show the user the list.",
	}, (*Service).Import}
	OpReminders = Op[ReminderLogInput, ReminderLog]{Meta{
		Name: "list_reminders", Title: "Reminder log", Tag: "Audit", Method: "GET", Path: "/reminders",
		Description: "Every reminder Exhale planned, newest first, with what happened to it: sent, scheduled, failed and why, or no longer needed. Answers \"why didn't I get that reminder?\".",
	}, (*Service).Reminders}
	OpListAudit = Op[AuditLogInput, AuditLog]{Meta{
		Name: "list_changes", Title: "Recent changes", Tag: "Audit", Method: "GET", Path: "/changes",
		Description: "Recent changes with who made them (member, agent or system). Each id can be passed to undo.",
	}, (*Service).ListAudit}
	OpUndo = Op[UndoInput, UndoResult]{Meta{
		Name: "undo", Title: "Undo a change", Tag: "Audit", Method: "POST", Path: "/changes/undo", Write: true,
		Description: "Reverts a change by its auditId, restoring the previous state.",
	}, (*Service).Undo}
)

// Tools for many subscriptions: bulk changes and tags (bulk.go).
var (
	OpBulkUpdate = Op[BulkUpdateInput, BulkResult]{Meta{
		Name: "bulk_update_subscriptions", Title: "Change several subscriptions", Tag: "Subscriptions", Method: "POST", Path: "/subscriptions/bulk", Write: true, Destructive: true,
		Description: "Changes several subscriptions in one step, all or none: set the category or payment method, add or remove tags (action update), or pause, resume, cancel or delete them. Call it first with dryRun to show what changes for each. The auditIds it returns can all be passed to undo to revert the whole change.",
	}, (*Service).BulkUpdate}
	OpRenameTag = Op[RenameTagInput, BulkResult]{Meta{
		Name: "rename_tag", Title: "Rename a tag", Tag: "Subscriptions", Method: "POST", Path: "/tags/rename", Write: true, Idempotent: true,
		Description: "Renames a tag on every subscription that has it; if one already has the new name, they merge. The tags in use are in get_household.",
	}, (*Service).RenameTag}
	OpDeleteTag = Op[DeleteTagInput, BulkResult]{Meta{
		Name: "delete_tag", Title: "Delete a tag", Tag: "Subscriptions", Method: "POST", Path: "/tags/delete", Write: true, Destructive: true,
		Description: "Removes a tag from every subscription. The subscriptions stay. Can be reverted with undo (auditIds).",
	}, (*Service).DeleteTag}
)

// Catalog lists every use case exposed to adapters.
func Catalog() []Binding {
	return []Binding{
		OpGetContext.Binding(), OpGetHousehold.Binding(), OpUpdateHousehold.Binding(), OpListSubscriptions.Binding(), OpGetSubscription.Binding(),
		OpCreateSubscription.Binding(), OpUpdateSubscription.Binding(), OpSetSubscriptionStatus.Binding(),
		OpDeleteSubscription.Binding(), OpListCharges.Binding(), OpCalendar.Binding(), OpNeedsAttention.Binding(), OpChangeChargeDate.Binding(),
		OpChangeChargeAmount.Binding(), OpSetChargeStatus.Binding(), OpDecideRenewal.Binding(), OpPreviewSchedule.Binding(), OpSummary.Binding(),
		OpSpendHistory.Binding(), OpBalances.Binding(), OpAudit.Binding(),
		OpSavePaymentMethod.Binding(), OpReassignPaymentMethod.Binding(), OpDeletePaymentMethod.Binding(),
		OpCreateCategory.Binding(), OpUpdateCategory.Binding(), OpDeleteCategory.Binding(), OpExport.Binding(), OpImport.Binding(), OpReminders.Binding(), OpListAudit.Binding(), OpUndo.Binding(),
		OpBulkUpdate.Binding(), OpRenameTag.Binding(), OpDeleteTag.Binding(),
	}
}

// PaymentMethodOut wraps a saved payment method.
type PaymentMethodOut struct {
	PaymentMethod domain.PaymentMethod `json:"paymentMethod"`
}

// CategoryOut wraps a created category.
type CategoryOut struct {
	Category domain.Category `json:"category"`
}

func savePaymentMethod(s *Service, ctx context.Context, a Actor, in SavePaymentMethodInput) (PaymentMethodOut, error) {
	pm, err := s.SavePaymentMethod(ctx, a, in)
	return PaymentMethodOut{PaymentMethod: pm}, err
}

func updateCategory(s *Service, ctx context.Context, a Actor, in UpdateCategoryInput) (CategoryOut, error) {
	c, err := s.UpdateCategory(ctx, a, in)
	return CategoryOut{Category: c}, err
}

func createCategory(s *Service, ctx context.Context, a Actor, in CreateCategoryInput) (CategoryOut, error) {
	c, err := s.CreateCategory(ctx, a, in)
	return CategoryOut{Category: c}, err
}
