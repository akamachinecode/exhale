package actions

import (
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
)

// Event kinds: what a message is telling the member.
const (
	EventReminder = "reminder" // a charge is coming
	EventDecision = "decision" // the last day to cancel is near: keep or cancel?
	EventConfirm  = "confirm"  // the due date passed: was it charged?
	EventFailed   = "failed"   // the charge failed
	EventPaid     = "paid"
	EventSkipped  = "skipped"
	EventTest     = "test" // a test message sent from Settings
)

// Event is the data behind a message, for channels that cannot show a
// screen with buttons: a webhook sends it as JSON, an email builds its
// subject from it, and all of them link to Path in the web. Messages that
// are not about a charge leave the fields they do not have empty.
type Event struct {
	Kind           string
	SubscriptionID string
	Subscription   string
	ChargeID       string
	Amount         string // decimal, e.g. "12.99"
	Currency       string // ISO 4217
	DueDate        string // YYYY-MM-DD
	DaysUntil      int    // negative when overdue
	PaymentMethod  string
	Category       string
	// Path is the web page that shows it, e.g. /subscriptions/sub_…
	Path string
}

// chargeEvent describes a charge message. The kind follows the same rules
// as the title chargeMessage picks.
func chargeEvent(kind string, r app.ChargeResult, today engine.Date) *Event {
	c, sub := r.Charge, r.Subscription
	ev := &Event{
		Kind:           EventReminder,
		SubscriptionID: sub.ID,
		Subscription:   sub.Name,
		ChargeID:       c.ID,
		Amount:         c.Amount.Decimal(),
		Currency:       c.Amount.Currency,
		DueDate:        c.DueDate.String(),
		DaysUntil:      today.DaysUntil(c.DueDate),
		PaymentMethod:  sub.PaymentMethod,
		Category:       sub.CategoryName,
		Path:           "/subscriptions/" + sub.ID,
	}
	switch {
	case c.Status == domain.ChargeFailed:
		ev.Kind = EventFailed
	case c.Status == domain.ChargePaid:
		ev.Kind = EventPaid
	case c.Status == domain.ChargeSkipped:
		ev.Kind = EventSkipped
	case kind == app.KindConfirm || c.DueDate.Before(today):
		ev.Kind = EventConfirm
	case kind == app.KindDecision && !c.CancelBy.IsZero() && c.Decision == "":
		ev.Kind = EventDecision
	}
	return ev
}
