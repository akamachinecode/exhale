package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Outbox kinds.
const (
	KindReminder = "reminder" // "Netflix renews in 3 days"
	KindConfirm  = "confirm"  // "Was Netflix charged?"
	KindDecision = "decision" // "Last day to cancel the gym is in 7 days: keep or cancel?"
)

// DecisionDaysBefore is when to ask "keep or cancel?" before the last day to
// cancel a subscription with a notice period: a week ahead and on the day.
var DecisionDaysBefore = []int{7, 0}

// System is the actor used by the scheduler.
func System(householdID string) Actor {
	return Actor{Kind: ActorSystem, HouseholdID: householdID, Label: "Exhale", Scope: ScopeTrusted}
}

// Plan materializes upcoming charges for every active subscription, enqueues
// their reminders and confirmation questions in the outbox, and auto-confirms
// charges of trusted subscriptions. It is idempotent: every outbox entry has
// a dedupe key, so running it again never duplicates a reminder (ADR 0003).
func (s *Service) Plan(ctx context.Context) error {
	subs, err := s.st.Q.ListActiveSubscriptions(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	households := map[string]bool{}
	defer func() {
		for _, id := range sortedKeys(households) {
			s.planNotices(ctx, id, now)
		}
	}()
	for _, r := range subs {
		sub, err := store.SubscriptionFromRow(r)
		if err != nil {
			s.log.Error("plan: bad subscription", "id", r.ID, "err", err)
			continue
		}
		h, err := s.household(ctx, s.st.Q, sub.HouseholdID)
		if err != nil {
			return err
		}
		households[h.ID] = true
		loc := h.Location()
		today := engine.DateOf(now.In(loc))
		if sub.NextPrice != nil && !today.Before(sub.NextPrice.From) {
			// A price change announced in advance has arrived.
			sub.Price, sub.NextPrice = sub.NextPrice.Price, nil
			if err := s.st.Tx(ctx, func(q *db.Queries) error { return s.saveSubscription(ctx, q, sub) }); err != nil {
				s.log.Error("plan: applying the scheduled price failed", "id", sub.ID, "err", err)
				continue
			}
			sub.Version++
		}
		if err := s.st.Tx(ctx, func(q *db.Queries) error { return s.materialize(ctx, q, sub, today) }); err != nil {
			return err
		}
		charges, err := s.st.Q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: sub.ID, Limit: 60})
		if err != nil {
			return err
		}
		for _, cr := range charges {
			ch := store.ChargeFromRow(cr)
			if !ch.Open() || today.DaysUntil(ch.DueDate) > 60+sub.NoticeDays {
				continue
			}
			if sub.AutoConfirm && !sub.ManualRenewal() && ch.Status == domain.ChargePending && !ch.DueDate.After(today) {
				if _, err := s.SetChargeStatus(ctx, System(sub.HouseholdID), SetChargeStatusInput{ChargeID: ch.ID, Status: string(domain.ChargePaid), Note: "auto-confirmed"}); err != nil {
					s.log.Error("auto-confirm failed", "charge", ch.ID, "err", err)
				}
				continue
			}
			if err := s.enqueue(ctx, sub, ch, loc, today, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) enqueue(ctx context.Context, sub domain.Subscription, ch domain.Charge, loc *time.Location, today engine.Date, now time.Time) error {
	recipient := sub.PayerID
	if recipient == "" {
		recipient = sub.CreatedBy
	}
	if recipient == "" {
		return nil
	}
	p := sub.Reminders
	add := func(kind, key string, at time.Time) error {
		if at.Before(now) {
			at = now // catch up on reminders missed while the server was down
		}
		return s.st.Q.EnqueueOutbox(ctx, db.EnqueueOutboxParams{
			ID: domain.NewID(domain.PrefixOutbox), Kind: kind, DedupeKey: key, ChargeID: store.Ptr(ch.ID),
			MemberID: store.Ptr(recipient), DueAt: store.FormatTime(at), CreatedAt: store.FormatTime(now),
		})
	}
	if ch.Status == domain.ChargePending && !ch.DueDate.Before(today) {
		for _, days := range p.DaysBefore {
			at := ch.DueDate.AddDays(-days).At(p.Hour, p.Minute, loc)
			if at.Before(now) && days != 0 && today.DaysUntil(ch.DueDate) < days-1 {
				continue // a later reminder for this charge is still ahead; skip stale ones
			}
			if err := add(KindReminder, fmt.Sprintf("%s:%s:%s:%d", KindReminder, ch.ID, ch.DueDate, days), at); err != nil {
				return err
			}
		}
	}
	if sub.NoticeDays > 0 && ch.Status == domain.ChargePending && ch.Decision == "" {
		cancelBy := sub.CancelDeadline(ch.DueDate)
		for _, days := range DecisionDaysBefore {
			if cancelBy.Before(today) {
				break // too late to cancel this one
			}
			at := cancelBy.AddDays(-days).At(p.Hour, p.Minute, loc)
			if at.Before(now) && days != 0 {
				continue // the "last day" reminder is still ahead
			}
			if err := add(KindDecision, fmt.Sprintf("%s:%s:%s:%d", KindDecision, ch.ID, cancelBy, days), at); err != nil {
				return err
			}
		}
	}
	if p.ConfirmAfterDays >= 0 && ch.Status == domain.ChargePending {
		// "Was it charged?" repeats until someone answers (ReminderPolicy).
		asks := p.ConfirmAsks(ch.DueDate)
		for i, d := range asks {
			key := confirmKey(ch, i)
			if i > 0 {
				if d.Before(today) {
					continue // a repeat missed while the server was down is not worth sending late
				}
				if s.askedOn(ctx, ch.ID, KindConfirm, d, loc, key) {
					continue // a snoozed question already comes back that day
				}
			}
			if err := add(KindConfirm, key, d.At(p.Hour, p.Minute, loc)); err != nil {
				return err
			}
		}
		for i := max(len(asks), 1); i < domain.MaxConfirmTimes; i++ {
			// Fewer repeats than before: drop the ones no longer wanted.
			if err := s.st.Q.CancelOutboxKey(ctx, confirmKey(ch, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// confirmKey is the dedupe key of the n-th "was it charged?" for a charge
// (n = 0 is the first ask and keeps the key it always had).
func confirmKey(ch domain.Charge, n int) string {
	if n == 0 {
		return fmt.Sprintf("%s:%s:%s", KindConfirm, ch.ID, ch.DueDate)
	}
	return fmt.Sprintf("%s:%s:%s:r%d", KindConfirm, ch.ID, ch.DueDate, n)
}

// OutboxItem is something the scheduler must deliver now.
type OutboxItem struct {
	ID       string
	Kind     string
	ChargeID string
	MemberID string
	DueAt    time.Time
	Attempts int
	// Ref is what an item that is not about a charge is about: a payment
	// method (KindCardExpiry) or a duplicate group (KindDuplicate).
	Ref string
	// Personal is set for an item one member asked for (a snooze): it goes
	// to them only, never to a shared team channel.
	Personal bool
}

// DueOutbox returns up to limit items due at or before now.
func (s *Service) DueOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	rows, err := s.st.Q.ListOutboxDue(ctx, db.ListOutboxDueParams{DueAt: store.FormatTime(s.now()), Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, OutboxItem{ID: r.ID, Kind: r.Kind, ChargeID: store.Str(r.ChargeID), MemberID: store.Str(r.MemberID), DueAt: store.ParseTime(r.DueAt), Attempts: int(r.Attempts),
			Ref: r.Ref, Personal: strings.HasPrefix(r.DedupeKey, snoozePrefix)})
	}
	return out, nil
}

// NextOutboxDue returns when the next pending item is due, if any.
func (s *Service) NextOutboxDue(ctx context.Context) (time.Time, bool) {
	v, err := s.st.Q.NextOutboxDue(ctx)
	if err != nil {
		return time.Time{}, false
	}
	return store.ParseTime(v), true
}

// MaxAttempts is how many times delivery is retried.
const MaxAttempts = 8

// MarkDelivered records a successful delivery.
func (s *Service) MarkDelivered(ctx context.Context, id string) error {
	return s.st.Q.MarkOutboxSent(ctx, db.MarkOutboxSentParams{SentAt: store.Ptr(store.FormatTime(s.now())), ID: id})
}

// MarkFailed records a failed attempt and schedules a retry with backoff.
func (s *Service) MarkFailed(ctx context.Context, item OutboxItem, cause error) error {
	delay := time.Duration(1<<min(item.Attempts, 7)) * time.Minute // 1, 2, 4 … 128 min
	return s.st.Q.MarkOutboxAttemptFailed(ctx, db.MarkOutboxAttemptFailedParams{
		LastError: cause.Error(), DueAt: store.FormatTime(s.now().Add(delay)), Attempts: MaxAttempts, ID: item.ID,
	})
}

// ActorForMember returns the actor of a member (used by channel adapters).
func (s *Service) ActorForMember(ctx context.Context, memberID string) (Actor, error) {
	m, err := s.st.Q.GetMember(ctx, memberID)
	if err != nil {
		return Actor{}, store.NotFound(err, "member %s: not found", memberID)
	}
	return MemberActor(store.MemberFromRow(m), ScopeWrite), nil
}
