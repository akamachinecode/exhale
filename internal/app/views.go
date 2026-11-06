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
}

// ChargeView is a charge with its subscription context.
type ChargeView struct {
	domain.Charge
	SubscriptionName string `json:"subscriptionName"`
	PaymentMethod    string `json:"paymentMethod,omitempty"`
	Delayed          bool   `json:"delayed" jsonschema:"true when the charge was moved from its scheduled date"`
	DaysUntil        int    `json:"daysUntil" jsonschema:"days from today to the due date (negative if overdue)"`
}

// SubscriptionDetail is a subscription with its upcoming and past charges.
type SubscriptionDetail struct {
	Subscription SubscriptionView `json:"subscription"`
	Upcoming     []ChargeView     `json:"upcoming"`
	History      []ChargeView     `json:"history"`
}
