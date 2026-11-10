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
	Interval        int    `json:"interval,omitempty" jsonschema:"number of units between charges, default 1" minimum:"1"`
	Unit            string `json:"unit,omitempty" jsonschema:"day, week, month (default) or year" enum:"day,week,month,year"`
	FirstCharge     string `json:"firstCharge,omitempty" jsonschema:"date of the first (or next) charge, YYYY-MM-DD. Required unless trialEndsOn is given"`
	CategoryID      string `json:"categoryId,omitempty"`
	PaymentMethodID string `json:"paymentMethodId,omitempty"`
	PayerID         string `json:"payerId,omitempty" jsonschema:"member who pays; defaults to the caller"`
	Private         bool   `json:"private,omitempty" jsonschema:"only visible to its creator and payer"`
	URL             string `json:"url,omitempty"`
	Notes           string `json:"notes,omitempty"`
	AutoConfirm     bool   `json:"autoConfirm,omitempty" jsonschema:"mark each charge paid automatically on its due date"`
	ReminderDays    []int  `json:"reminderDays,omitempty" jsonschema:"days before each charge to remind, default [3, 0]"`
	EndDate         string `json:"endDate,omitempty" jsonschema:"last possible charge date, YYYY-MM-DD"`
	Status          string `json:"status,omitempty" jsonschema:"active (default) or paused; cancelled records a subscription that already ended: pass firstCharge = when it started and endDate = its last day" enum:"active,paused,cancelled"`
	TermsInput
	DryRun bool `json:"dryRun,omitempty" jsonschema:"preview the result without saving"`
}

// TermsInput are the contract facts that can be set when creating a
// subscription.
type TermsInput struct {
	StartedOn   string         `json:"startedOn,omitempty" jsonschema:"when the member first subscribed, YYYY-MM-DD. History only: it creates no charges"`
	TrialEndsOn string         `json:"trialEndsOn,omitempty" jsonschema:"free trial: the day it turns into the first paid charge, YYYY-MM-DD. price is what it costs from then on"`
	NoticeDays  int            `json:"noticeDays,omitempty" jsonschema:"days of notice the provider requires to cancel before a renewal. Exhale reminds before the last day to cancel" minimum:"0" maximum:"366"`
	Renewal     string         `json:"renewal,omitempty" jsonschema:"auto (default): the provider charges again by itself. manual: it expires unless someone renews it (domains, licences, prepaid plans)" enum:"auto,manual"`
	CancelURL   string         `json:"cancelUrl,omitempty" jsonschema:"page where it is cancelled or managed, https://…"`
	Account     string         `json:"account,omitempty" jsonschema:"login or email the subscription lives under"`
	Shares      []domain.Share `json:"shares,omitempty" jsonschema:"how members split the cost, by relative weight; empty: the payer carries it all"`
}

func (t TermsInput) details() (domain.Details, error) {
	d := domain.Details{NoticeDays: t.NoticeDays, Renewal: domain.Renewal(t.Renewal), CancelURL: strings.TrimSpace(t.CancelURL), Account: strings.TrimSpace(t.Account), Shares: t.Shares}
	var err error
	if d.StartedOn, err = optionalDate(t.StartedOn, "started on"); err != nil {
		return d, err
	}
	d.TrialEndsOn, err = optionalDate(t.TrialEndsOn, "trial end")
	return d, err
}

func optionalDate(s, what string) (engine.Date, error) {
	if s == "" {
		return engine.Date{}, nil
	}
	d, err := engine.ParseDate(s)
	if err != nil {
		return d, fmt.Errorf("%w: %s: %v", domain.ErrValidation, what, err)
	}
	return d, nil
}

// CreateSubscription creates a subscription and its upcoming charges.
func (s *Service) CreateSubscription(ctx context.Context, actor Actor, in CreateSubscriptionInput) (SubscriptionDetail, error) {
	sub, err := s.newSubscription(ctx, s.st.Q, actor, in)
	if err != nil {
		return SubscriptionDetail{}, err
	}
	var out SubscriptionDetail
	err = s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if err := s.persistNew(ctx, q, c, actor, &sub); err != nil {
			return err
		}
		out, err = s.detail(ctx, q, actor, sub.ID)
		return err
	})
	return out, err
}

// newSubscription validates the input and builds the subscription.
func (s *Service) newSubscription(ctx context.Context, q *db.Queries, actor Actor, in CreateSubscriptionInput) (domain.Subscription, error) {
	none := domain.Subscription{}
	price, err := money.Parse(in.Price, in.Currency)
	if err != nil {
		return none, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	details, err := in.details()
	if err != nil {
		return none, err
	}
	if in.FirstCharge == "" && !details.TrialEndsOn.IsZero() {
		in.FirstCharge = details.TrialEndsOn.String() // a trial's first charge is the day it ends
	}
	first, err := engine.ParseDate(in.FirstCharge)
	if err != nil {
		return none, fmt.Errorf("%w: first charge: %v", domain.ErrValidation, err)
	}
	if !details.TrialEndsOn.IsZero() && first.Before(details.TrialEndsOn) {
		return none, fmt.Errorf("%w: the first charge (%s) cannot be before the trial ends (%s)", domain.ErrValidation, first, details.TrialEndsOn)
	}
	if in.Interval == 0 {
		in.Interval = 1
	}
	if in.Unit == "" {
		in.Unit = string(engine.Month)
	}
	sched, err := engine.NewSchedule(engine.Rule{Interval: in.Interval, Unit: engine.Unit(in.Unit), Anchor: first})
	if err != nil {
		return none, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if in.EndDate != "" {
		if sched.Until, err = engine.ParseDate(in.EndDate); err != nil {
			return none, fmt.Errorf("%w: end date: %v", domain.ErrValidation, err)
		}
	}
	now := s.now()
	sub := domain.Subscription{
		ID: domain.NewID(domain.PrefixSubscription), HouseholdID: actor.HouseholdID,
		Name: strings.TrimSpace(in.Name), Price: price, Schedule: sched,
		CategoryID: in.CategoryID, PaymentMethodID: in.PaymentMethodID, PayerID: in.PayerID,
		Visibility: domain.VisibilityShared, Status: domain.StatusActive, URL: in.URL, Notes: in.Notes,
		AutoConfirm: in.AutoConfirm, Reminders: domain.DefaultReminderPolicy(), Details: details, CreatedBy: actor.MemberID,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if in.Status != "" {
		sub.Status = domain.SubscriptionStatus(in.Status)
	}
	if h, err := s.household(ctx, q, actor.HouseholdID); err == nil {
		sub.Reminders.Hour, sub.Reminders.Minute = h.ReminderHour, h.ReminderMinute
		if h.ConfirmTimes > 0 && h.ConfirmEveryDays > 0 {
			sub.Reminders.ConfirmTimes, sub.Reminders.ConfirmEveryDays = h.ConfirmTimes, h.ConfirmEveryDays
		}
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
	return sub, sub.Validate()
}

// persistNew saves a new subscription with its upcoming charges inside a
// write transaction.
func (s *Service) persistNew(ctx context.Context, q *db.Queries, c *change, actor Actor, sub *domain.Subscription) error {
	today := s.today(ctx, q, actor)
	if sub.Status == domain.StatusCancelled && sub.Schedule.Until.IsZero() {
		sub.Schedule.Until = maxDate(today.AddDays(-1), sub.Schedule.First())
	}
	if err := s.checkRefs(ctx, q, *sub); err != nil {
		return err
	}
	if err := s.insertSubscription(ctx, q, *sub); err != nil {
		return err
	}
	if err := s.materialize(ctx, q, *sub, today); err != nil {
		return err
	}
	if _, err := c.record(q, "subscription.create", "subscription", sub.ID, nil, *sub); err != nil {
		return err
	}
	c.emit(bus.Event{Type: bus.SubscriptionCreated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
	return nil
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
	Interval        *int    `json:"interval,omitempty" jsonschema:"new number of units between charges, from the next charge on" minimum:"1"`
	Unit            *string `json:"unit,omitempty" jsonschema:"new billing unit, from the next charge on" enum:"day,week,month,year"`
	NextCharge      *string `json:"nextCharge,omitempty" jsonschema:"date of the next charge, YYYY-MM-DD: changes the billing day from now on (to move a single charge use change_charge_date)"`
	EndDate         *string `json:"endDate,omitempty" jsonschema:"last possible charge date, YYYY-MM-DD, for fixed-term contracts; empty string removes it"`
	PriceFrom       *string `json:"priceFrom,omitempty" jsonschema:"with price: the date the new price starts, YYYY-MM-DD, for a price change announced in advance. Default: from the next open charge"`
	StartedOn       *string `json:"startedOn,omitempty" jsonschema:"when the member first subscribed, YYYY-MM-DD; empty string clears it"`
	TrialEndsOn     *string `json:"trialEndsOn,omitempty" jsonschema:"day the free trial turns into the first paid charge, YYYY-MM-DD; empty string clears it"`
	NoticeDays      *int    `json:"noticeDays,omitempty" jsonschema:"days of notice required to cancel before a renewal; 0 removes it"`
	Renewal         *string `json:"renewal,omitempty" jsonschema:"auto or manual (expires unless renewed)" enum:"auto,manual"`
	CancelURL       *string `json:"cancelUrl,omitempty" jsonschema:"page where it is cancelled or managed"`
	Account         *string `json:"account,omitempty" jsonschema:"login or email the subscription lives under"`
	// Shares replace the cost split; an empty list gives the cost back to the payer.
	Shares []domain.Share `json:"shares,omitempty" jsonschema:"how members split the cost, by relative weight"`
	Using  *bool          `json:"using,omitempty" jsonschema:"the member's answer to 'still using this?'"`
	DryRun bool           `json:"dryRun,omitempty"`
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
		set(&sub.NoticeDays, in.NoticeDays)
		set(&sub.CancelURL, in.CancelURL)
		set(&sub.Account, in.Account)
		if in.Renewal != nil {
			sub.Renewal = domain.Renewal(*in.Renewal)
		}
		if in.Shares != nil {
			sub.Shares = in.Shares
		}
		if in.Using != nil {
			sub.Usage = &domain.Usage{Using: *in.Using, At: s.now()}
		}
		if in.StartedOn != nil {
			if sub.StartedOn, err = optionalDate(*in.StartedOn, "started on"); err != nil {
				return err
			}
		}
		if in.TrialEndsOn != nil {
			if sub.TrialEndsOn, err = optionalDate(*in.TrialEndsOn, "trial end"); err != nil {
				return err
			}
		}
		today := s.today(ctx, q, actor)
		repriceFrom := engine.Date{}
		if in.Interval != nil || in.Unit != nil || (in.NextCharge != nil && *in.NextCharge != "") {
			// A new billing cycle or day applies from the next charge on.
			at, ok := sub.Schedule.NextOnOrAfter(today)
			if !ok {
				at = today
			}
			rule := sub.Schedule.Current(at)
			set(&rule.Interval, in.Interval)
			if in.Unit != nil {
				rule.Unit = engine.Unit(*in.Unit)
			}
			anchor := at
			if in.NextCharge != nil && *in.NextCharge != "" {
				if anchor, err = engine.ParseDate(*in.NextCharge); err != nil {
					return fmt.Errorf("%w: next charge: %v", domain.ErrValidation, err)
				}
			}
			if sub.Schedule, err = sub.Schedule.ChangeFrom(at, anchor, rule.Interval, rule.Unit); err != nil {
				return fmt.Errorf("%w: %v", domain.ErrValidation, err)
			}
			repriceFrom = at
			if anchor.Before(at) {
				repriceFrom = anchor
			}
		}
		if in.EndDate != nil {
			if sub.Schedule.Until, err = optionalDate(*in.EndDate, "end date"); err != nil {
				return err
			}
			if repriceFrom.IsZero() || today.Before(repriceFrom) {
				repriceFrom = today
			}
		}
		if in.Price != nil {
			p, err := money.Parse(*in.Price, sub.Price.Currency)
			if err != nil {
				return fmt.Errorf("%w: %v", domain.ErrValidation, err)
			}
			from := today
			if in.PriceFrom != nil && *in.PriceFrom != "" {
				if from, err = engine.ParseDate(*in.PriceFrom); err != nil {
					return fmt.Errorf("%w: price from: %v", domain.ErrValidation, err)
				}
			}
			earliest := func(d engine.Date) {
				if repriceFrom.IsZero() || d.Before(repriceFrom) {
					repriceFrom = d
				}
			}
			switch {
			case from.After(today): // announced in advance: keep today's price until then
				sub.NextPrice = &domain.PriceChange{From: from, Price: p}
				earliest(from)
			case p != sub.Price || sub.NextPrice != nil:
				sub.Price, sub.NextPrice = p, nil
				earliest(today)
			}
		}
		sub.Name = strings.TrimSpace(sub.Name)
		sub.CancelURL, sub.Account = strings.TrimSpace(sub.CancelURL), strings.TrimSpace(sub.Account)
		if err := sub.Validate(); err != nil {
			return err
		}
		if err := s.checkRefs(ctx, q, sub); err != nil {
			return err
		}
		if err := s.saveSubscription(ctx, q, sub); err != nil {
			return err
		}
		if !repriceFrom.IsZero() {
			if err := q.DeleteFuturePendingCharges(ctx, db.DeleteFuturePendingChargesParams{SubscriptionID: sub.ID, OriginalDate: repriceFrom.String()}); err != nil {
				return err
			}
			if err := s.materialize(ctx, q, sub, maxDate(repriceFrom, today)); err != nil {
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

// deletedSubscription is the audit snapshot of a deleted subscription.
type deletedSubscription struct {
	domain.Subscription
	Charges []domain.Charge `json:"charges,omitempty"`
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
		// The snapshot keeps every charge, so undo brings the history back.
		gone := deletedSubscription{Subscription: sub}
		rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: sub.ID, Limit: 5000})
		if err != nil {
			return err
		}
		for _, r := range rows {
			gone.Charges = append(gone.Charges, store.ChargeFromRow(r))
		}
		if err := q.DeleteSubscription(ctx, db.DeleteSubscriptionParams{ID: sub.ID, HouseholdID: actor.HouseholdID}); err != nil {
			return err
		}
		id, err := c.record(q, "subscription.delete", "subscription", sub.ID, gone, nil)
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

func (s *Service) insertSubscription(ctx context.Context, q *db.Queries, sub domain.Subscription) error {
	sched, rem, det, err := store.SubscriptionJSON(sub)
	if err != nil {
		return err
	}
	return q.CreateSubscription(ctx, db.CreateSubscriptionParams{
		ID: sub.ID, HouseholdID: sub.HouseholdID, Name: sub.Name, PriceMinor: sub.Price.Minor,
		Currency: sub.Price.Currency, Schedule: sched, CategoryID: store.Ptr(sub.CategoryID),
		PaymentMethodID: store.Ptr(sub.PaymentMethodID), PayerID: store.Ptr(sub.PayerID),
		Visibility: string(sub.Visibility), Status: string(sub.Status), Url: sub.URL, Notes: sub.Notes,
		AutoConfirm: boolInt(sub.AutoConfirm), Reminders: rem, Details: det, CreatedBy: sub.CreatedBy,
		CreatedAt: store.FormatTime(sub.CreatedAt), UpdatedAt: store.FormatTime(s.now()),
	})
}

// checkRefs makes sure the category, payment method, payer and share members
// exist in the household, so a wrong id fails with a clear message instead
// of a database constraint.
func (s *Service) checkRefs(ctx context.Context, q *db.Queries, sub domain.Subscription) error {
	missing := func(what, id string) error {
		return fmt.Errorf("%w: %s %s does not exist. Read ids from get_household; ids returned by a dry run are not saved", domain.ErrValidation, what, id)
	}
	if sub.CategoryID != "" {
		found := false
		for _, cat := range mustList(q.ListCategories(ctx, sub.HouseholdID)) {
			found = found || cat.ID == sub.CategoryID
		}
		if !found {
			return missing("category", sub.CategoryID)
		}
	}
	if sub.PaymentMethodID != "" {
		if _, err := q.GetPaymentMethod(ctx, db.GetPaymentMethodParams{ID: sub.PaymentMethodID, HouseholdID: sub.HouseholdID}); err != nil {
			return missing("payment method", sub.PaymentMethodID)
		}
	}
	members := map[string]bool{}
	for _, m := range mustList(q.ListMembers(ctx, sub.HouseholdID)) {
		members[m.ID] = true
	}
	if sub.PayerID != "" && !members[sub.PayerID] {
		return missing("member", sub.PayerID)
	}
	for _, sh := range sub.Shares {
		if !members[sh.MemberID] {
			return missing("member", sh.MemberID)
		}
	}
	return nil
}

func (s *Service) saveSubscription(ctx context.Context, q *db.Queries, sub domain.Subscription) error {
	sched, rem, det, err := store.SubscriptionJSON(sub)
	if err != nil {
		return err
	}
	n, err := q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{
		Name: sub.Name, PriceMinor: sub.Price.Minor, Currency: sub.Price.Currency, Schedule: sched,
		CategoryID: store.Ptr(sub.CategoryID), PaymentMethodID: store.Ptr(sub.PaymentMethodID),
		PayerID: store.Ptr(sub.PayerID), Visibility: string(sub.Visibility), Status: string(sub.Status),
		Url: sub.URL, Notes: sub.Notes, AutoConfirm: boolInt(sub.AutoConfirm), Reminders: rem, Details: det,
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
	// A notice period moves the date that matters earlier, so look further.
	to := from.AddDays(HorizonDays + sub.NoticeDays)
	for _, d := range sub.Schedule.Between(from, to) {
		if err := q.InsertChargeIfMissing(ctx, db.InsertChargeIfMissingParams{
			ID: domain.NewID(domain.PrefixCharge), SubscriptionID: sub.ID, OriginalDate: d.String(),
			DueDate: d.String(), AmountMinor: sub.PriceOn(d).Minor, Currency: sub.Price.Currency,
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
	v.Cadence = cadence(rule)
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
		if v.CancelledUntil == sub.Schedule.First() {
			// Cancelled before its first charge: the schedule cannot end
			// before it starts, but the service ends the day before.
			v.CancelledUntil = v.CancelledUntil.AddDays(-1)
		}
	}
	v.MonthlyCost, _ = sub.Price.Scale(monthlyFactor(rule))
	v.YearlyCost, _ = v.MonthlyCost.Scale("12")
	if sub.Status == domain.StatusActive {
		v.InTrial = sub.InTrial(today)
		v.Expires = sub.ManualRenewal()
		v.AskUsage = usageStale(sub, s.now())
		if sub.NoticeDays > 0 && !v.NextCharge.IsZero() {
			// The next deadline that can still be met: if the one for the next
			// charge has passed, it is the one for the charge after.
			due := v.NextCharge
			for range 4 {
				if v.CancelBy = sub.CancelDeadline(due); !v.CancelBy.Before(today) {
					break
				}
				next, ok := sub.Schedule.NextOnOrAfter(due.AddDays(1))
				if !ok {
					v.CancelBy = engine.Date{}
					break
				}
				due = next
			}
			if v.CancelBy.Before(today) {
				v.CancelBy = engine.Date{}
			}
		}
	}
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
	v := ChargeView{Charge: ch, SubscriptionName: sub.Name, PaymentMethod: sub.PaymentMethod, Delayed: ch.Delayed(), DaysUntil: today.DaysUntil(ch.DueDate), Kind: ChargeRenewal}
	switch {
	case sub.ManualRenewal():
		v.Kind = ChargeExpiry
	case !sub.TrialEndsOn.IsZero() && ch.OriginalDate == sub.Schedule.First():
		v.Kind = ChargeTrial
	}
	if sub.NoticeDays > 0 && ch.Open() {
		v.CancelBy = sub.CancelDeadline(ch.DueDate)
	}
	return v
}

func (s *Service) detail(ctx context.Context, q *db.Queries, actor Actor, id string) (SubscriptionDetail, error) {
	sub, err := s.subscription(ctx, q, actor, id)
	if err != nil {
		return SubscriptionDetail{}, err
	}
	today := s.today(ctx, q, actor)
	v := s.view(ctx, q, sub, today)
	out := SubscriptionDetail{Subscription: v, Upcoming: []ChargeView{}, History: []ChargeView{}}
	rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: id, Limit: 2000})
	if err != nil {
		return out, err
	}
	all := make([]domain.Charge, 0, len(rows))
	for _, r := range slices.Backward(rows) {
		all = append(all, store.ChargeFromRow(r))
	}
	out.PriceHistory, out.Paid = priceHistory(sub, all, today), paidTotals(sub, all)
	for _, ch := range all { // oldest first
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

// cadence describes a rule in plain words: "monthly", "every 4 weeks".
func cadence(r engine.Rule) string {
	if r.Interval == 1 {
		return map[engine.Unit]string{engine.Day: "daily", engine.Week: "weekly", engine.Month: "monthly", engine.Year: "yearly"}[r.Unit]
	}
	return fmt.Sprintf("every %d %ss", r.Interval, r.Unit)
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
