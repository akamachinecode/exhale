package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// ErrNoDestination is returned by a delivery when the member has nowhere to
// receive it: no chat or channel linked, or none of them working.
var ErrNoDestination = errors.New("no linked chat")

// MarkUndeliverable records that an item could not be sent to anyone and
// will not be retried: there is nowhere to send it.
func (s *Service) MarkUndeliverable(ctx context.Context, item OutboxItem, cause error) error {
	return s.st.Q.MarkOutboxUndeliverable(ctx, db.MarkOutboxUndeliverableParams{LastError: cause.Error(), ID: item.ID})
}

// ReminderLogInput selects how much of the log to read.
type ReminderLogInput struct {
	Limit int `json:"limit,omitempty" query:"limit" jsonschema:"how many entries, newest first (default 100)" maximum:"500"`
}

// ReminderEntry is one reminder Exhale planned, sent or failed to send.
type ReminderEntry struct {
	ID             string      `json:"id"`
	Kind           string      `json:"kind" jsonschema:"reminder (before a charge), decision (before the last day to cancel) or confirm (was it charged?)"`
	Status         string      `json:"status" jsonschema:"pending, sent, failed or cancelled" enum:"pending,sent,failed,cancelled"`
	DueAt          time.Time   `json:"dueAt" jsonschema:"when it is or was meant to go out"`
	SentAt         *time.Time  `json:"sentAt,omitempty"`
	Attempts       int         `json:"attempts"`
	Error          string      `json:"error,omitempty" jsonschema:"last delivery error"`
	SubscriptionID string      `json:"subscriptionId"`
	Subscription   string      `json:"subscription"`
	ChargeDate     engine.Date `json:"chargeDate"`
	To             string      `json:"to,omitempty" jsonschema:"member it is for"`
	Explanation    string      `json:"explanation" jsonschema:"what happened, in plain words"`
}

// ReminderLog is the answer to "why didn't I get that reminder?".
type ReminderLog struct {
	Entries []ReminderEntry `json:"entries"`
}

// Reminders returns the reminders of the household, newest first, each with
// what happened to it: sent, waiting, failed and why, or no longer needed.
func (s *Service) Reminders(ctx context.Context, actor Actor, in ReminderLogInput) (ReminderLog, error) {
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := s.st.Q
	rows, err := q.ListOutboxForHousehold(ctx, db.ListOutboxForHouseholdParams{HouseholdID: actor.HouseholdID, Limit: int64(limit)})
	if err != nil {
		return ReminderLog{}, err
	}
	names := map[string]string{}
	for _, m := range mustList(q.ListMembers(ctx, actor.HouseholdID)) {
		names[m.ID] = m.Name
	}
	visible := map[string]bool{}
	out := ReminderLog{Entries: []ReminderEntry{}}
	for _, r := range rows {
		ok, seen := visible[r.SubscriptionID]
		if !seen {
			_, err := s.subscription(ctx, q, actor, r.SubscriptionID)
			ok = err == nil
			visible[r.SubscriptionID] = ok
		}
		if !ok {
			continue
		}
		e := ReminderEntry{ID: r.ID, Kind: r.Kind, Status: r.Status, DueAt: store.ParseTime(r.DueAt), Attempts: int(r.Attempts), Error: r.LastError,
			SubscriptionID: r.SubscriptionID, Subscription: r.SubscriptionName, To: names[store.Str(r.MemberID)]}
		e.ChargeDate, _ = engine.ParseDate(r.ChargeDue)
		if r.SentAt != nil {
			t := store.ParseTime(*r.SentAt)
			e.SentAt = &t
		}
		who := e.To
		if who == "" {
			who = "the member"
		}
		switch {
		case r.Status == "sent":
			e.Explanation = "Sent."
		case r.Status == "cancelled":
			e.Explanation = "Not needed any more: the charge was resolved, moved or cancelled before it was due."
		case r.Status == "failed" && r.LastError == ErrNoDestination.Error():
			e.Explanation = fmt.Sprintf("Not sent: %s had no chat or channel linked at that time.", who)
		case r.Status == "failed":
			e.Explanation = fmt.Sprintf("Gave up after %d attempts. Last error: %s", r.Attempts, r.LastError)
		case r.Attempts > 0:
			e.Explanation = fmt.Sprintf("Failed %d time(s), will retry. Last error: %s", r.Attempts, r.LastError)
		default:
			e.Explanation = "Scheduled."
		}
		out.Entries = append(out.Entries, e)
	}
	return out, nil
}
