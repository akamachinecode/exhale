package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// HorizonDays is how far ahead charges are materialized.
const HorizonDays = 120

// --- create ------------------------------------------------------------------

// CreateSubscriptionInput describes a new subscription.
type CreateSubscriptionInput struct {
	Name            string `json:"name" jsonschema:"service name, e.g. Netflix Premium" minLength:"1" maxLength:"120"`
	Price           string `json:"price" jsonschema:"price per charge as a decimal string, e.g. 12.99"`
	Currency        string `json:"currency" jsonschema:"ISO 4217 code, e.g. EUR"`
	Interval        int    `json:"interval" jsonschema:"number of units between charges, usually 1" minimum:"1"`
	Unit            string `json:"unit" jsonschema:"day, week, month or year" enum:"day,week,month,year"`
	FirstCharge     string `json:"firstCharge" jsonschema:"date of the first (or next) charge, YYYY-MM-DD"`
	CategoryID      string `json:"categoryId,omitempty"`
	PaymentMethodID string `json:"paymentMethodId,omitempty"`
	PayerID         string `json:"payerId,omitempty" jsonschema:"member who pays; defaults to the caller"`
	Private         bool   `json:"private,omitempty" jsonschema:"only visible to its creator and payer"`
	URL             string `json:"url,omitempty"`
	Notes           string `json:"notes,omitempty"`
	AutoConfirm     bool   `json:"autoConfirm,omitempty" jsonschema:"mark each charge paid automatically on its due date"`
	ReminderDays    []int  `json:"reminderDays,omitempty" jsonschema:"days before each charge to remind, default [3, 0]"`
	EndDate         string `json:"endDate,omitempty" jsonschema:"last possible charge date, YYYY-MM-DD"`
	DryRun          bool   `json:"dryRun,omitempty" jsonschema:"preview the result without saving"`
}

// CreateSubscription creates a subscription and its upcoming charges.
func (s *Service) CreateSubscription(ctx context.Context, actor Actor, in CreateSubscriptionInput) (SubscriptionDetail, error) {
	price, err := money.Parse(in.Price, in.Currency)
	if err != nil {
		return SubscriptionDetail{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	first, err := engine.ParseDate(in.FirstCharge)
	if err != nil {
		return SubscriptionDetail{}, fmt.Errorf("%w: first charge: %v", domain.ErrValidation, err)
	}
	if in.Interval == 0 {
		in.Interval = 1
	}
	sched, err := engine.NewSchedule(engine.Rule{Interval: in.Interval, Unit: engine.Unit(in.Unit), Anchor: first})
	if err != nil {
		return SubscriptionDetail{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if in.EndDate != "" {
		if sched.Until, err = engine.ParseDate(in.EndDate); err != nil {
			return SubscriptionDetail{}, fmt.Errorf("%w: end date: %v", domain.ErrValidation, err)
		}
	}
	now := s.now()
	sub := domain.Subscription{
		ID: domain.NewID(domain.PrefixSubscription), HouseholdID: actor.HouseholdID,
		Name: strings.TrimSpace(in.Name), Price: price, Schedule: sched,
		CategoryID: in.CategoryID, PaymentMethodID: in.PaymentMethodID, PayerID: in.PayerID,
		Visibility: domain.VisibilityShared, Status: domain.StatusActive, URL: in.URL, Notes: in.Notes,
		AutoConfirm: in.AutoConfirm, Reminders: domain.DefaultReminderPolicy(), CreatedBy: actor.MemberID,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if sub.PayerID == "" {
		sub.PayerID = actor.MemberID
	}
	if in.Private {
		sub.Visibility = domain.VisibilityPrivate
	}
	if in.ReminderDays != nil {
		sub.Reminders.DaysBefore = in.ReminderDays
	}
	if err := sub.Validate(); err != nil {
		return SubscriptionDetail{}, err
	}
	var out SubscriptionDetail
	err = s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		sched, rem, err := store.SubscriptionJSON(sub)
		if err != nil {
			return err
		}
		if err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{
			ID: sub.ID, HouseholdID: sub.HouseholdID, Name: sub.Name, PriceMinor: sub.Price.Minor,
			Currency: sub.Price.Currency, Schedule: sched, CategoryID: store.Ptr(sub.CategoryID),
			PaymentMethodID: store.Ptr(sub.PaymentMethodID), PayerID: store.Ptr(sub.PayerID),
			Visibility: string(sub.Visibility), Status: string(sub.Status), Url: sub.URL, Notes: sub.Notes,
			AutoConfirm: boolInt(sub.AutoConfirm), Reminders: rem, CreatedBy: sub.CreatedBy,
			CreatedAt: store.FormatTime(now), UpdatedAt: store.FormatTime(now),
		}); err != nil {
			return err
		}
		if err := s.materialize(ctx, q, sub, s.today(ctx, q, actor)); err != nil {
			return err
		}
		if _, err := c.record(q, "subscription.create", "subscription", sub.ID, nil, sub); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionCreated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
		out, err = s.detail(ctx, q, actor, sub.ID)
		return err
	})
	return out, err
}

// --- update ------------------------------------------------------------------

// UpdateSubscriptionInput changes subscription-level fields. Only non-nil
// fields change. Price changes made here apply from the next open charge on
// ("this and following"); use ChangeChargeAmount for a single charge.
type UpdateSubscriptionInput struct {
	ID              string  `json:"id" jsonschema:"subscription id (sub_…)"`
	Name            *string `json:"name,omitempty"`
	Price           *string `json:"price,omitempty" jsonschema:"new price from the next open charge on, decimal string"`
	CategoryID      *string `json:"categoryId,omitempty"`
	PaymentMethodID *string `json:"paymentMethodId,omitempty" jsonschema:"applies to this and following charges"`
	PayerID         *string `json:"payerId,omitempty"`
	Private         *bool   `json:"private,omitempty"`
	URL             *string `json:"url,omitempty"`
	Notes           *string `json:"notes,omitempty"`
	AutoConfirm     *bool   `json:"autoConfirm,omitempty"`
	ReminderDays    []int   `json:"reminderDays,omitempty"`
	ReminderHour    *int    `json:"reminderHour,omitempty" jsonschema:"local hour of day for reminders (0-23)"`
	DryRun          bool    `json:"dryRun,omitempty"`
}

// UpdateSubscription edits a subscription.
func (s *Service) UpdateSubscription(ctx context.Context, actor Actor, in UpdateSubscriptionInput) (SubscriptionDetail, error) {
	var out SubscriptionDetail
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		before, err := s.subscription(ctx, q, actor, in.ID)
		if err != nil {
			return err
		}
		sub := before
		set(&sub.Name, in.Name)
		set(&sub.CategoryID, in.CategoryID)
		set(&sub.PaymentMethodID, in.PaymentMethodID)
		set(&sub.PayerID, in.PayerID)
		set(&sub.URL, in.URL)
		set(&sub.Notes, in.Notes)
		set(&sub.AutoConfirm, in.AutoConfirm)
		if in.Private != nil {
			sub.Visibility = domain.VisibilityShared
			if *in.Private {
				sub.Visibility = domain.VisibilityPrivate
			}
		}
		if in.ReminderDays != nil {
			sub.Reminders.DaysBefore = in.ReminderDays
		}
		set(&sub.Reminders.Hour, in.ReminderHour)
		repriced := false
		if in.Price != nil {
			p, err := money.Parse(*in.Price, sub.Price.Currency)
			if err != nil {
				return fmt.Errorf("%w: %v", domain.ErrValidation, err)
			}
			repriced = p != sub.Price
			sub.Price = p
		}
		sub.Name = strings.TrimSpace(sub.Name)
		if err := sub.Validate(); err != nil {
			return err
		}
		if err := s.saveSubscription(ctx, q, sub); err != nil {
			return err
		}
		if repriced {
			today := s.today(ctx, q, actor)
			if err := q.DeleteFuturePendingCharges(ctx, db.DeleteFuturePendingChargesParams{SubscriptionID: sub.ID, OriginalDate: today.String()}); err != nil {
				return err
			}
			if err := s.materialize(ctx, q, sub, today); err != nil {
				return err
			}
		}
		if _, err := c.record(q, "subscription.update", "subscription", sub.ID, before, sub); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
		out, err = s.detail(ctx, q, actor, sub.ID)
		return err
	})
	return out, err
}

// --- lifecycle ---------------------------------------------------------------

// SetSubscriptionStatusInput pauses, resumes or cancels a subscription.
type SetSubscriptionStatusInput struct {
	ID     string `json:"id"`
	Action string `json:"action" jsonschema:"pause, resume, cancel_at_period_end or cancel_now" enum:"pause,resume,cancel_at_period_end,cancel_now"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// SetSubscriptionStatus changes the lifecycle of a subscription. Cancelling
// at period end keeps the service until the day before the next charge.
func (s *Service) SetSubscriptionStatus(ctx context.Context, actor Actor, in SetSubscriptionStatusInput) (SubscriptionDetail, error) {
	var out SubscriptionDetail
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		before, err := s.subscription(ctx, q, actor, in.ID)
		if err != nil {
			return err
		}
		sub := before
		today := s.today(ctx, q, actor)
		dropFrom := today
		switch in.Action {
		case "pause":
			sub.Status = domain.StatusPaused
		case "resume":
			sub.Status = domain.StatusActive
		case "cancel_now":
			sub.Status = domain.StatusCancelled
			sub.Schedule.Until = maxDate(today.AddDays(-1), sub.Schedule.First())
		case "cancel_at_period_end":
			sub.Status = domain.StatusCancelled
			next, ok := sub.Schedule.NextOnOrAfter(today.AddDays(1))
			if ok {
				sub.Schedule.Until = maxDate(next.AddDays(-1), sub.Schedule.First())
				dropFrom = next
			}
		default:
			return fmt.Errorf("%w: unknown action %q", domain.ErrValidation, in.Action)
		}
		if err := s.saveSubscription(ctx, q, sub); err != nil {
			return err
		}
		if err := q.DeleteFuturePendingCharges(ctx, db.DeleteFuturePendingChargesParams{SubscriptionID: sub.ID, OriginalDate: dropFrom.String()}); err != nil {
			return err
		}
		if sub.Status == domain.StatusActive {
			if err := s.materialize(ctx, q, sub, today); err != nil {
				return err
			}
		}
		if _, err := c.record(q, "subscription."+in.Action, "subscription", sub.ID, before, sub); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
		out, err = s.detail(ctx, q, actor, sub.ID)
		return err
	})
	return out, err
}

// DeleteSubscriptionInput permanently deletes a subscription and its charges.
type DeleteSubscriptionInput struct {
	ID     string `json:"id"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DeleteResult confirms a deletion.
type DeleteResult struct {
	Deleted string `json:"deleted"`
	AuditID string `json:"auditId" jsonschema:"pass to undo to restore"`
}

// DeleteSubscription removes a subscription (it can be restored with Undo).
func (s *Service) DeleteSubscription(ctx context.Context, actor Actor, in DeleteSubscriptionInput) (DeleteResult, error) {
	var out DeleteResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		sub, err := s.subscription(ctx, q, actor, in.ID)
		if err != nil {
			return err
		}
		if err := q.DeleteSubscription(ctx, db.DeleteSubscriptionParams{ID: sub.ID, HouseholdID: actor.HouseholdID}); err != nil {
			return err
		}
		id, err := c.record(q, "subscription.delete", "subscription", sub.ID, sub, nil)
		if err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionDeleted, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
		out = DeleteResult{Deleted: sub.ID, AuditID: id}
		return nil
	})
	return out, err
}

// --- helpers -----------------------------------------------------------------

func (s *Service) saveSubscription(ctx context.Context, q *db.Queries, sub domain.Subscription) error {
	sched, rem, err := store.SubscriptionJSON(sub)
	if err != nil {
		return err
	}
	n, err := q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{
		Name: sub.Name, PriceMinor: sub.Price.Minor, Currency: sub.Price.Currency, Schedule: sched,
		CategoryID: store.Ptr(sub.CategoryID), PaymentMethodID: store.Ptr(sub.PaymentMethodID),
		PayerID: store.Ptr(sub.PayerID), Visibility: string(sub.Visibility), Status: string(sub.Status),
		Url: sub.URL, Notes: sub.Notes, AutoConfirm: boolInt(sub.AutoConfirm), Reminders: rem,
		UpdatedAt: store.FormatTime(s.now()), ID: sub.ID, HouseholdID: sub.HouseholdID, Version: int64(sub.Version),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("subscription %s changed meanwhile: %w", sub.ID, domain.ErrConflict)
	}
	return nil
}

// materialize creates pending charges for every scheduled date from `from`
// (but never before the first charge) to the horizon. Existing charges are
// left untouched (idempotent).
func (s *Service) materialize(ctx context.Context, q *db.Queries, sub domain.Subscription, from engine.Date) error {
	if sub.Status != domain.StatusActive {
		return nil
	}
	to := from.AddDays(HorizonDays)
	for _, d := range sub.Schedule.Between(from, to) {
		if err := q.InsertChargeIfMissing(ctx, db.InsertChargeIfMissingParams{
			ID: domain.NewID(domain.PrefixCharge), SubscriptionID: sub.ID, OriginalDate: d.String(),
			DueDate: d.String(), AmountMinor: sub.Price.Minor, Currency: sub.Price.Currency,
			UpdatedAt: store.FormatTime(s.now()),
		}); err != nil {
			return err
		}
	}
	return nil
}

// today returns the current date in the actor's household time zone.
func (s *Service) today(ctx context.Context, q *db.Queries, actor Actor) engine.Date {
	h, err := s.household(ctx, q, actor.HouseholdID)
	if err != nil {
		return engine.DateOf(s.now().UTC())
	}
	return engine.DateOf(s.now().In(h.Location()))
}

func (s *Service) view(ctx context.Context, q *db.Queries, sub domain.Subscription, today engine.Date) SubscriptionView {
	v := SubscriptionView{Subscription: sub}
	rule := sub.Schedule.Current(today)
	unit := string(rule.Unit)
	if rule.Interval != 1 {
		unit += "s"
	}
	v.Cadence = fmt.Sprintf("every %d %s", rule.Interval, unit)
	if sub.Status == domain.StatusActive {
		if c, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: sub.ID, Limit: 400}); err == nil {
			for _, r := range slices.Backward(c) {
				ch := store.ChargeFromRow(r)
				if ch.Open() && !ch.DueDate.Before(today) {
					v.NextCharge = ch.DueDate
					break
				}
			}
		}
		if v.NextCharge.IsZero() {
			v.NextCharge, _ = sub.Schedule.NextOnOrAfter(today)
		}
	}
	if sub.Status == domain.StatusCancelled {
		v.CancelledUntil = sub.Schedule.Until
	}
	v.MonthlyCost, _ = sub.Price.Scale(monthlyFactor(rule))
	if sub.CategoryID != "" {
		for _, cat := range mustList(q.ListCategories(ctx, sub.HouseholdID)) {
			if cat.ID == sub.CategoryID {
				v.CategoryName = cat.Name
			}
		}
	}
	if sub.PaymentMethodID != "" {
		if pm, err := q.GetPaymentMethod(ctx, db.GetPaymentMethodParams{ID: sub.PaymentMethodID, HouseholdID: sub.HouseholdID}); err == nil {
			v.PaymentMethod = store.PaymentMethodFromRow(pm).Label()
		}
	}
	if sub.PayerID != "" {
		if m, err := q.GetMember(ctx, sub.PayerID); err == nil {
			v.PayerName = m.Name
		}
	}
	return v
}

func (s *Service) chargeView(ch domain.Charge, sub SubscriptionView, today engine.Date) ChargeView {
	return ChargeView{Charge: ch, SubscriptionName: sub.Name, PaymentMethod: sub.PaymentMethod, Delayed: ch.Delayed(), DaysUntil: today.DaysUntil(ch.DueDate)}
}

func (s *Service) detail(ctx context.Context, q *db.Queries, actor Actor, id string) (SubscriptionDetail, error) {
	sub, err := s.subscription(ctx, q, actor, id)
	if err != nil {
		return SubscriptionDetail{}, err
	}
	today := s.today(ctx, q, actor)
	v := s.view(ctx, q, sub, today)
	out := SubscriptionDetail{Subscription: v, Upcoming: []ChargeView{}, History: []ChargeView{}}
	rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: id, Limit: 400})
	if err != nil {
		return out, err
	}
	for _, r := range slices.Backward(rows) { // oldest first
		ch := store.ChargeFromRow(r)
		cv := s.chargeView(ch, v, today)
		if ch.Open() && !ch.DueDate.Before(today) {
			if len(out.Upcoming) < 12 {
				out.Upcoming = append(out.Upcoming, cv)
			}
		} else {
			out.History = append(out.History, cv)
		}
	}
	slices.Reverse(out.History) // newest first
	if len(out.History) > 24 {
		out.History = out.History[:24]
	}
	return out, nil
}

// monthlyFactor converts one charge of rule into an average per month.
func monthlyFactor(r engine.Rule) string {
	perMonth := map[engine.Unit]float64{engine.Day: 30.436875, engine.Week: 4.348125, engine.Month: 1, engine.Year: 1.0 / 12}[r.Unit]
	return fmt.Sprintf("%.8f", perMonth/float64(r.Interval))
}

func set[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func maxDate(a, b engine.Date) engine.Date {
	if a.After(b) {
		return a
	}
	return b
}

func mustList[T any](v []T, _ error) []T { return v }
