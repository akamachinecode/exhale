package app

import (
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
)

// SubscriptionView is a subscription plus everything a reader (human or AI
// agent) needs to understand it without extra lookups.
type SubscriptionView struct {
	domain.Subscription
	Cadence        string      `json:"cadence" jsonschema:"human description of the billing cycle, e.g. every 1 month"`
	NextCharge     engine.Date `json:"nextCharge,omitzero" jsonschema:"next charge date (YYYY-MM-DD), empty if none"`
	MonthlyCost    money.Money `json:"monthlyCost" jsonschema:"price normalized to one month, in the subscription currency"`
	CategoryName   string      `json:"categoryName,omitempty"`
	PaymentMethod  string      `json:"paymentMethod,omitempty" jsonschema:"payment method label, e.g. Revolut ••0937"`
	PayerName      string      `json:"payerName,omitempty"`
	CancelledUntil engine.Date `json:"serviceUntil,omitzero" jsonschema:"for cancelled subscriptions: last day of service"`
	YearlyCost     money.Money `json:"yearlyCost" jsonschema:"price normalized to one year, in the subscription currency"`
	InTrial        bool        `json:"inTrial" jsonschema:"the free trial is still running; the next charge is the first paid one"`
	CancelBy       engine.Date `json:"cancelBy,omitzero" jsonschema:"last day to cancel before the next charge (next charge minus the notice period); empty when there is no notice period"`
	Expires        bool        `json:"expires" jsonschema:"manual renewal: the next charge date is when it expires unless renewed"`
	AskUsage       bool        `json:"askUsage" jsonschema:"it is time to ask the member whether they still use it (never asked, or asked over six months ago)"`
	// Billing rules (weekday of the month, weekend shift).
	BillingDay    string      `json:"billingDay,omitempty" jsonschema:"when in its cycle it is charged, in words, e.g. on the third Wednesday, moved to the next business day when it falls on a weekend; empty for a plain day of the month"`
	NextScheduled engine.Date `json:"nextScheduled,omitzero" jsonschema:"the date the next charge is scheduled on before a weekend shift moves it; equals nextCharge when it was not moved"`
}

// ChargeView is a charge with its subscription context.
type ChargeView struct {
	domain.Charge
	SubscriptionName string `json:"subscriptionName"`
	PaymentMethod    string `json:"paymentMethod,omitempty"`
	Delayed          bool   `json:"delayed" jsonschema:"true when the charge was moved from its scheduled date"`
	DaysUntil        int    `json:"daysUntil" jsonschema:"days from today to the due date (negative if overdue)"`
	// Kind tells what this date means to the member.
	Kind         string      `json:"kind" jsonschema:"renewal (a normal charge), trial (the first charge after a free trial) or expiry (manual renewal: it expires on this date unless renewed)" enum:"renewal,trial,expiry"`
	CancelBy     engine.Date `json:"cancelBy,omitzero" jsonschema:"last day to cancel before this charge, when the subscription has a notice period"`
	CancelPassed bool        `json:"cancelPassed" jsonschema:"the last day to cancel before this charge has passed: it will renew"`
}

// Charge kinds.
const (
	ChargeRenewal = "renewal"
	ChargeTrial   = "trial"
	ChargeExpiry  = "expiry"
)

// SubscriptionDetail is a subscription with its upcoming and past charges.
type SubscriptionDetail struct {
	Subscription SubscriptionView `json:"subscription"`
	Upcoming     []ChargeView     `json:"upcoming"`
	History      []ChargeView     `json:"history"`
	PriceHistory []PricePoint     `json:"priceHistory" jsonschema:"every price it has had, oldest first, from the charge ledger; a scheduled change comes last"`
	Paid         PaidTotals       `json:"paid" jsonschema:"what it has really cost so far"`
}
