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
		Description: "Adds a recurring subscription and computes its upcoming charges. Use dryRun to preview the charge dates first.",
	}, (*Service).CreateSubscription}
	OpUpdateSubscription = Op[UpdateSubscriptionInput, SubscriptionDetail]{Meta{
		Name: "update_subscription", Title: "Update a subscription", Tag: "Subscriptions", Method: "POST", Path: "/subscriptions/update", Write: true, Idempotent: true,
		Description: "Changes name, price (from the next open charge on), category, payment method, payer, privacy, URL, notes, auto-confirm or reminder settings. Only the fields you pass change.",
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
	OpNeedsAttention = Op[struct{}, AttentionList]{Meta{
		Name: "needs_attention", Title: "What needs attention", Tag: "Charges", Method: "GET", Path: "/attention",
		Description: "Charges whose date passed without confirmation, and payment methods about to expire.",
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
	OpPreviewSchedule = Op[PreviewScheduleInput, SchedulePreview]{Meta{
		Name: "preview_schedule", Title: "Preview charge dates", Tag: "Charges", Method: "POST", Path: "/schedule/preview", Idempotent: true,
		Description: "Computes the charge dates of a billing rule with Exhale's engine (month-end and leap-day aware) without saving anything.",
	}, (*Service).PreviewSchedule}
	OpSummary = Op[struct{}, SpendingSummary]{Meta{
		Name: "spending_summary", Title: "Spending summary", Tag: "Insights", Method: "GET", Path: "/summary",
		Description: "Monthly and yearly recurring spend per currency, spend per category, and the charges of the next 7 days.",
	}, (*Service).Summary}
	OpSavePaymentMethod = Op[SavePaymentMethodInput, PaymentMethodOut]{Meta{
		Name: "save_payment_method", Title: "Create or update a payment method", Tag: "Household", Method: "POST", Path: "/payment-methods", Write: true,
		Description: "Creates a payment method (no id) or updates one (with id): name, kind, last four digits, expiry date and owner.",
	}, savePaymentMethod}
	OpCreateCategory = Op[CreateCategoryInput, CategoryOut]{Meta{
		Name: "create_category", Title: "Create a category", Tag: "Household", Method: "POST", Path: "/categories", Write: true,
		Description: "Adds a category for grouping subscriptions.",
	}, createCategory}
	OpListAudit = Op[AuditLogInput, AuditLog]{Meta{
		Name: "list_changes", Title: "Recent changes", Tag: "Audit", Method: "GET", Path: "/changes",
		Description: "Recent changes with who made them (member, agent or system). Each id can be passed to undo.",
	}, (*Service).ListAudit}
	OpUndo = Op[UndoInput, UndoResult]{Meta{
		Name: "undo", Title: "Undo a change", Tag: "Audit", Method: "POST", Path: "/changes/undo", Write: true,
		Description: "Reverts a change by its auditId, restoring the previous state.",
	}, (*Service).Undo}
)

// Catalog lists every use case exposed to adapters.
func Catalog() []Binding {
	return []Binding{
		OpGetContext.Binding(), OpGetHousehold.Binding(), OpListSubscriptions.Binding(), OpGetSubscription.Binding(),
		OpCreateSubscription.Binding(), OpUpdateSubscription.Binding(), OpSetSubscriptionStatus.Binding(),
		OpDeleteSubscription.Binding(), OpListCharges.Binding(), OpNeedsAttention.Binding(), OpChangeChargeDate.Binding(),
		OpChangeChargeAmount.Binding(), OpSetChargeStatus.Binding(), OpPreviewSchedule.Binding(), OpSummary.Binding(),
		OpSavePaymentMethod.Binding(), OpCreateCategory.Binding(), OpListAudit.Binding(), OpUndo.Binding(),
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

func createCategory(s *Service, ctx context.Context, a Actor, in CreateCategoryInput) (CategoryOut, error) {
	c, err := s.CreateCategory(ctx, a, in)
	return CategoryOut{Category: c}, err
}
