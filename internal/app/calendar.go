package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// CalendarInput selects the dates to show.
type CalendarInput struct {
	From string `json:"from,omitempty" query:"from" jsonschema:"first day, YYYY-MM-DD (default today)"`
	To   string `json:"to,omitempty" query:"to" jsonschema:"last day, YYYY-MM-DD (default one year after from; at most three years)"`
}

// Calendar event kinds.
const (
	EventCharge   = "charge"   // a renewal
	EventTrial    = "trial"    // a free trial turns into the first charge
	EventExpiry   = "expiry"   // it expires unless renewed
	EventCancelBy = "cancelBy" // last day to cancel before a renewal
)

// CalendarEvent is one dated thing worth seeing in a calendar.
type CalendarEvent struct {
	Date           engine.Date `json:"date"`
	Kind           string      `json:"kind" jsonschema:"charge, trial (free trial ends), expiry (expires unless renewed) or cancelBy (last day to cancel)" enum:"charge,trial,expiry,cancelBy"`
	Title          string      `json:"title"`
	SubscriptionID string      `json:"subscriptionId"`
	Subscription   string      `json:"subscription"`
	Amount         money.Money `json:"amount"`
	ChargeID       string      `json:"chargeId,omitempty" jsonschema:"empty for dates projected from the schedule beyond the next few months"`
	Status         string      `json:"status,omitempty" jsonschema:"charge status; projected dates are pending"`
	Projected      bool        `json:"projected" jsonschema:"computed from the schedule; the charge does not exist yet"`
}

// CalendarView lists the events of a period.
type CalendarView struct {
	From   engine.Date     `json:"from"`
	To     engine.Date     `json:"to"`
	Events []CalendarEvent `json:"events"`
}

// Calendar returns every dated event between two days: charges (real ones
// and, further ahead, the ones the schedule will produce), free trials
// ending, things expiring and last days to cancel.
func (s *Service) Calendar(ctx context.Context, actor Actor, in CalendarInput) (CalendarView, error) {
	q := s.st.Q
	today := s.today(ctx, q, actor)
	from, err := optionalDate(in.From, "from")
	if err != nil {
		return CalendarView{}, err
	}
	if from.IsZero() {
		from = today
	}
	to, err := optionalDate(in.To, "to")
	if err != nil {
		return CalendarView{}, err
	}
	if to.IsZero() {
		to = from.AddDays(365)
	}
	if to.Before(from) || from.DaysUntil(to) > 3*366 {
		return CalendarView{}, fmt.Errorf("%w: choose a period of at most three years", domain.ErrValidation)
	}
	out := CalendarView{From: from, To: to, Events: []CalendarEvent{}}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{})
	if err != nil {
		return out, err
	}
	in1 := func(d engine.Date) bool { return !d.Before(from) && !d.After(to) }
	for _, v := range list.Subscriptions {
		rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: v.ID, Limit: 2000})
		if err != nil {
			return out, err
		}
		last := engine.Date{}
		add := func(c ChargeView, projected bool) {
			kind, title := EventCharge, v.Name
			switch c.Kind {
			case ChargeTrial:
				kind, title = EventTrial, v.Name+" free trial ends"
			case ChargeExpiry:
				kind, title = EventExpiry, v.Name+" expires"
			}
			if in1(c.DueDate) {
				out.Events = append(out.Events, CalendarEvent{Date: c.DueDate, Kind: kind, Title: title, SubscriptionID: v.ID, Subscription: v.Name,
					Amount: c.Amount, ChargeID: c.ID, Status: string(c.Status), Projected: projected})
			}
			if !c.CancelBy.IsZero() && c.Status == domain.ChargePending && in1(c.CancelBy) {
				out.Events = append(out.Events, CalendarEvent{Date: c.CancelBy, Kind: EventCancelBy, Title: "Last day to cancel " + v.Name, SubscriptionID: v.ID,
					Subscription: v.Name, Amount: c.Amount, ChargeID: c.ID, Status: string(c.Status), Projected: projected})
			}
		}
		for _, r := range rows {
			c := store.ChargeFromRow(r)
			if c.OriginalDate.After(last) {
				last = c.OriginalDate
			}
			add(s.chargeView(c, v, today), false)
		}
		if v.Status != domain.StatusActive {
			continue
		}
		// Beyond the charges that already exist, project from the schedule.
		start := maxDate(today, from)
		if !last.IsZero() {
			start = maxDate(start, last.AddDays(1))
		}
		for _, d := range v.Schedule.Between(start, to.AddDays(v.NoticeDays)) {
			c := domain.Charge{SubscriptionID: v.ID, OriginalDate: d, DueDate: d, Amount: v.PriceOn(d), Status: domain.ChargePending}
			add(s.chargeView(c, v, today), true)
		}
	}
	slices.SortFunc(out.Events, func(a, b CalendarEvent) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return cmpString(a.Title, b.Title)
	})
	return out, nil
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
