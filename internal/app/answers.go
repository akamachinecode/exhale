package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Reminders that end in an answer (feature scan, ADR 0021): a reminder can
// be snoozed, "was it charged?" repeats until someone answers, and Exhale
// says so on its own when it finds the same service paid twice or a card
// about to expire with subscriptions on it. Everything goes through the
// outbox, so it is deduplicated, survives restarts and catches up.

// Outbox kinds of messages that are not about a charge.
const (
	KindDuplicate  = "duplicate" // "You and Ana both pay for Spotify"
	KindCardExpiry = "card"      // "Visa ••4421 expires in 20 days: 7 subscriptions use it"
)

// CardNoticeDays is how long before a card expires Exhale says so.
var CardNoticeDays = 30

// snoozePrefix starts the dedupe key of a snoozed message.
const snoozePrefix = "snooze:"

// ErrObsolete is returned by a delivery when the message has nothing left
// to say (the duplicate was resolved, the card renewed). The item is
// recorded as no longer needed instead of sent.
var ErrObsolete = errors.New("no longer needed")

// MarkObsolete records that an item was not sent because it no longer
// applies.
func (s *Service) MarkObsolete(ctx context.Context, item OutboxItem) error {
	return s.st.Q.MarkOutboxObsolete(ctx, db.MarkOutboxObsoleteParams{LastError: ErrObsolete.Error(), ID: item.ID})
}

// --- snooze ------------------------------------------------------------------------

// SnoozeInput asks for a reminder again later.
type SnoozeInput struct {
	ChargeID string `json:"chargeId"`
	Kind     string `json:"kind" jsonschema:"the message to bring back: reminder (a charge is coming), decision (keep or cancel before the last day to cancel) or confirm (was it charged?)" enum:"reminder,decision,confirm"`
	Days     int    `json:"days" jsonschema:"bring it back in this many days, at the reminder time (1: tomorrow)" minimum:"1" maximum:"30"`
	DryRun   bool   `json:"dryRun,omitempty"`
}

// SnoozeResult says when the message comes back.
type SnoozeResult struct {
	ChargeID string      `json:"chargeId"`
	Kind     string      `json:"kind"`
	On       engine.Date `json:"on" jsonschema:"the day it comes back"`
	At       time.Time   `json:"at" jsonschema:"when it comes back"`
	Clamped  bool        `json:"clamped" jsonschema:"it comes back earlier than asked: later would be too late (the charge date, or the last day to cancel)"`
	Planned  bool        `json:"alreadyPlanned" jsonschema:"a message about this charge was already planned that day, so no second one is added"`
	DryRun   bool        `json:"dryRun,omitempty"`
}

// SnoozeLimit is the last day a message about a charge is still useful: a
// reminder until the charge date, a "keep or cancel?" until the last day to
// cancel. ok is false when there is no limit ("was it charged?").
func SnoozeLimit(kind string, c ChargeView) (engine.Date, bool) {
	switch kind {
	case KindReminder:
		return c.DueDate, true
	case KindDecision:
		return c.CancelBy, true
	}
	return engine.Date{}, false
}

// SnoozeDay is the day a message snoozed for days comes back: never later
// than its limit. ok is false when it cannot come back after today.
func SnoozeDay(kind string, c ChargeView, today engine.Date, days int) (on engine.Date, clamped, ok bool) {
	on = today.AddDays(days)
	if limit, has := SnoozeLimit(kind, c); has && on.After(limit) {
		on, clamped = limit, true
	}
	return on, clamped, on.After(today)
}

// Snooze brings a reminder, a "keep or cancel?" or a "was it charged?" back
// in a few days at the reminder time, never past the point where it is
// useless: a decision snoozed past the last day to cancel comes back on
// that day.
func (s *Service) Snooze(ctx context.Context, actor Actor, in SnoozeInput) (SnoozeResult, error) {
	out := SnoozeResult{ChargeID: in.ChargeID, Kind: in.Kind, DryRun: in.DryRun}
	if in.Days < 1 || in.Days > 30 {
		return out, fmt.Errorf("%w: snooze for 1 to 30 days", domain.ErrValidation)
	}
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		ch, sub, err := s.charge(ctx, q, actor, in.ChargeID)
		if err != nil {
			return err
		}
		h, err := s.household(ctx, q, sub.HouseholdID)
		if err != nil {
			return err
		}
		today := s.today(ctx, q, actor)
		cv := s.chargeView(ch, SubscriptionView{Subscription: sub}, today)
		switch in.Kind {
		case KindReminder, KindConfirm:
			if !ch.Open() {
				return fmt.Errorf("%w: this charge is already %s", domain.ErrValidation, ch.Status)
			}
		case KindDecision:
			if ch.Status != domain.ChargePending || cv.CancelBy.IsZero() || ch.Decision != "" {
				return fmt.Errorf("%w: there is no keep-or-cancel question for this charge", domain.ErrValidation)
			}
		default:
			return fmt.Errorf("%w: kind must be reminder, decision or confirm", domain.ErrValidation)
		}
		on, clamped, ok := SnoozeDay(in.Kind, cv, today, in.Days)
		if !ok {
			return fmt.Errorf("%w: it is too late to snooze it: the date is today", domain.ErrValidation)
		}
		loc := h.Location()
		out.On, out.Clamped = on, clamped
		out.At = on.At(sub.Reminders.Hour, sub.Reminders.Minute, loc)
		key := fmt.Sprintf("%s%s:%s:%s", snoozePrefix, in.Kind, ch.ID, on)
		recipient := cmpOr(actor.MemberID, sub.PayerID, sub.CreatedBy)
		if out.Planned = s.askedOnQ(ctx, q, ch.ID, in.Kind, on, loc, key); !out.Planned {
			if err := q.EnqueueOutbox(ctx, db.EnqueueOutboxParams{
				ID: domain.NewID(domain.PrefixOutbox), Kind: in.Kind, DedupeKey: key, ChargeID: store.Ptr(ch.ID),
				MemberID: store.Ptr(recipient), DueAt: store.FormatTime(out.At), CreatedAt: store.FormatTime(s.now()),
			}); err != nil {
				return err
			}
		}
		_, err = c.record(q, "reminder.snooze", "reminder", ch.ID, nil, map[string]string{"kind": in.Kind, "on": on.String()})
		return err
	})
	return out, err
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// askedOn reports whether a message of kind about the charge is already
// planned or sent on day (in the household zone), other than the one with
// key except.
func (s *Service) askedOn(ctx context.Context, chargeID, kind string, day engine.Date, loc *time.Location, except string) bool {
	return s.askedOnQ(ctx, s.st.Q, chargeID, kind, day, loc, except)
}

func (s *Service) askedOnQ(ctx context.Context, q *db.Queries, chargeID, kind string, day engine.Date, loc *time.Location, except string) bool {
	rows, err := q.ListOutboxForChargeKind(ctx, db.ListOutboxForChargeKindParams{ChargeID: store.Ptr(chargeID), Kind: kind})
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.DedupeKey != except && engine.DateOf(store.ParseTime(r.DueAt).In(loc)) == day {
			return true
		}
	}
	return false
}

// --- duplicates --------------------------------------------------------------------

// duplicateGroups groups the active subscriptions the actor can see by
// service. Each group has a stable key: the same subscriptions always give
// the same key, a new member of the group gives a new one.
func (s *Service) duplicateGroups(ctx context.Context, actor Actor) ([]dupGroup, error) {
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return nil, err
	}
	return groupDuplicates(list.Subscriptions), nil
}

type dupGroup struct {
	Key  string
	Subs []SubscriptionView
}

func groupDuplicates(subs []SubscriptionView) []dupGroup {
	groups := map[string][]SubscriptionView{}
	for _, v := range subs {
		groups[serviceKey(v.Name)] = append(groups[serviceKey(v.Name)], v)
	}
	out := []dupGroup{}
	for _, k := range sortedKeys(groups) {
		if g := groups[k]; len(g) > 1 && k != "" {
			out = append(out, dupGroup{Key: DuplicateKey(g), Subs: g})
		}
	}
	return out
}

// DuplicateKey identifies a group of subscriptions of the same service.
func DuplicateKey(subs []SubscriptionView) string {
	ids := make([]string, len(subs))
	for i, v := range subs {
		ids[i] = v.ID
	}
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, ",")))
	return hex.EncodeToString(sum[:8])
}

func (s *Service) dismissed(ctx context.Context, householdID, kind string) map[string]bool {
	out := map[string]bool{}
	keys, _ := s.st.Q.ListDismissedFindings(ctx, db.ListDismissedFindingsParams{HouseholdID: householdID, Kind: kind})
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// DuplicateNotice is the group a duplicate message is about, as the
// recipient sees it.
type DuplicateNotice struct {
	Group DuplicateGroup
	// Subscriptions in the same order as Group.Subscriptions.
	Subscriptions []SubscriptionView
}

// Duplicate returns the duplicate group with key as the actor sees it. ok is
// false when it no longer exists or was said to be fine.
func (s *Service) Duplicate(ctx context.Context, actor Actor, key string) (DuplicateNotice, bool, error) {
	groups, err := s.duplicateGroups(ctx, actor)
	if err != nil {
		return DuplicateNotice{}, false, err
	}
	if s.dismissed(ctx, actor.HouseholdID, KindDuplicate)[key] {
		return DuplicateNotice{}, false, nil
	}
	for _, g := range groups {
		if g.Key == key {
			return DuplicateNotice{Group: g.view(), Subscriptions: g.Subs}, true, nil
		}
	}
	return DuplicateNotice{}, false, nil
}

func (g dupGroup) view() DuplicateGroup {
	refs := make([]SubscriptionRef, len(g.Subs))
	for i, v := range g.Subs {
		refs[i] = ref(v)
	}
	return DuplicateGroup{Key: g.Key, Service: g.Subs[0].Name, Subscriptions: refs}
}

// DismissDuplicateInput says a duplicate is fine.
type DismissDuplicateInput struct {
	Key    string `json:"key" jsonschema:"key of the duplicate group, from audit"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DismissResult confirms a finding was set aside.
type DismissResult struct {
	Key    string `json:"key"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DismissDuplicate records that paying a service more than once is on
// purpose: the group is no longer reported by audit and nobody is messaged
// about it again. A group that changes (a third subscription joins it) is a
// new group.
func (s *Service) DismissDuplicate(ctx context.Context, actor Actor, in DismissDuplicateInput) (DismissResult, error) {
	out := DismissResult(in)
	if s.dismissed(ctx, actor.HouseholdID, KindDuplicate)[in.Key] {
		return out, nil // someone already said so
	}
	if _, ok, err := s.Duplicate(ctx, actor, in.Key); err != nil {
		return out, err
	} else if !ok {
		return out, fmt.Errorf("duplicate %s: %w", in.Key, domain.ErrNotFound)
	}
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if err := q.DismissFinding(ctx, db.DismissFindingParams{HouseholdID: actor.HouseholdID, Kind: KindDuplicate, Key: in.Key,
			MemberID: store.Ptr(actor.MemberID), CreatedAt: store.FormatTime(s.now())}); err != nil {
			return err
		}
		if err := q.CancelOutboxRef(ctx, db.CancelOutboxRefParams{Kind: KindDuplicate, Ref: in.Key}); err != nil {
			return err
		}
		_, err := c.record(q, "finding.dismiss", "finding", in.Key, nil, map[string]string{"kind": KindDuplicate})
		return err
	})
	return out, err
}

// --- cards about to expire -----------------------------------------------------------

// CardNotice is a payment method about to expire with the subscriptions
// that use it, as the recipient sees them.
type CardNotice struct {
	PaymentMethod domain.PaymentMethod
	DaysLeft      int
	Subscriptions []SubscriptionView
	// Others are the payment methods they can move to.
	Others []domain.PaymentMethod
}

// Card returns what a card-expiry message about pmID says to the actor. ok
// is false when it no longer applies: the card was renewed or deleted, or
// nothing uses it any more.
func (s *Service) Card(ctx context.Context, actor Actor, pmID string) (CardNotice, bool, error) {
	info, err := s.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return CardNotice{}, false, err
	}
	out := CardNotice{}
	found := false
	for _, pm := range info.PaymentMethods {
		if pm.ID == pmID {
			out.PaymentMethod, found = pm, true
		} else {
			out.Others = append(out.Others, pm)
		}
	}
	if !found || out.PaymentMethod.ExpiresOn.IsZero() {
		return out, false, nil
	}
	out.DaysLeft = info.Today.DaysUntil(out.PaymentMethod.ExpiresOn)
	if out.DaysLeft < 0 || out.DaysLeft > CardNoticeDays {
		return out, false, nil
	}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return out, false, err
	}
	for _, v := range list.Subscriptions {
		if v.PaymentMethodID == pmID {
			out.Subscriptions = append(out.Subscriptions, v)
		}
	}
	return out, len(out.Subscriptions) > 0, nil
}

// --- planning the notices --------------------------------------------------------------

// planNotices enqueues one message per new duplicate group to the members
// who pay for it, and one per card about to expire to its owner (or to the
// members whose subscriptions use it). Dedupe keys make each one go out
// once. They arrive at the next reminder time.
func (s *Service) planNotices(ctx context.Context, householdID string, now time.Time) {
	q := s.st.Q
	h, err := s.household(ctx, q, householdID)
	if err != nil {
		return
	}
	loc := h.Location()
	today := engine.DateOf(now.In(loc))
	at := today.At(h.ReminderHour, h.ReminderMinute, loc)
	if at.Before(now) {
		at = today.AddDays(1).At(h.ReminderHour, h.ReminderMinute, loc)
	}
	members, err := q.ListMembers(ctx, householdID)
	if err != nil {
		return
	}
	enqueue := func(kind, key, member, ref string) {
		if err := q.EnqueueNotice(ctx, db.EnqueueNoticeParams{ID: domain.NewID(domain.PrefixOutbox), Kind: kind, DedupeKey: key,
			MemberID: store.Ptr(member), Ref: ref, DueAt: store.FormatTime(at), CreatedAt: store.FormatTime(now)}); err != nil {
			s.log.Error("plan: enqueue notice failed", "kind", kind, "err", err)
		}
	}
	payer := func(v SubscriptionView) string { return cmpOr(v.PayerID, v.CreatedBy) }
	fine := s.dismissed(ctx, householdID, KindDuplicate)
	for _, r := range members {
		m := store.MemberFromRow(r)
		actor := MemberActor(m, ScopeRead)
		groups, err := s.duplicateGroups(ctx, actor)
		if err != nil {
			s.log.Error("plan: duplicates failed", "err", err)
			return
		}
		for _, g := range groups {
			if fine[g.Key] || !slices.ContainsFunc(g.Subs, func(v SubscriptionView) bool { return payer(v) == m.ID }) {
				continue
			}
			enqueue(KindDuplicate, fmt.Sprintf("%s:%s:%s", KindDuplicate, g.Key, m.ID), m.ID, g.Key)
		}
	}
	pms, err := q.ListPaymentMethods(ctx, householdID)
	if err != nil {
		return
	}
	for _, r := range pms {
		pm := store.PaymentMethodFromRow(r)
		if pm.ExpiresOn.IsZero() {
			continue
		}
		if left := today.DaysUntil(pm.ExpiresOn); left < 0 || left > CardNoticeDays {
			continue
		}
		for _, r := range members {
			m := store.MemberFromRow(r)
			card, ok, err := s.Card(ctx, MemberActor(m, ScopeRead), pm.ID)
			if err != nil || !ok {
				continue
			}
			mine := pm.OwnerID == m.ID
			if pm.OwnerID == "" {
				mine = slices.ContainsFunc(card.Subscriptions, func(v SubscriptionView) bool { return payer(v) == m.ID })
			}
			if mine {
				enqueue(KindCardExpiry, fmt.Sprintf("%s:%s:%s:%s", KindCardExpiry, pm.ID, pm.ExpiresOn, m.ID), m.ID, pm.ID)
			}
		}
	}
}

// --- what cancelling saved ---------------------------------------------------------------

// StoppedSpend is what the household no longer pays because it cancelled
// subscriptions.
type StoppedSpend struct {
	Yearly        money.Money           `json:"yearly" jsonschema:"what the cancelled subscriptions would cost per year at their last price, in the display currency"`
	Count         int                   `json:"count"`
	Subscriptions []StoppedSubscription `json:"subscriptions" jsonschema:"most recently ended first"`
}

// StoppedSubscription is one cancelled subscription and what it cost.
type StoppedSubscription struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Yearly money.Money `json:"yearly" jsonschema:"per year at its last price, in the subscription currency"`
	// YearlyMain is Yearly converted into the display currency.
	YearlyMain money.Money `json:"yearlyMain" jsonschema:"per year, in the display currency"`
	Until      engine.Date `json:"until,omitzero" jsonschema:"last day of service"`
}

// stopped sums the subscriptions cancelled in Exhale (not paused). One whose
// service is still paid by another active subscription (a cancelled
// "Spotify" next to an active "Spotify Family") is a replacement, not a
// saving, and is left out.
func (s *Service) stopped(ctx context.Context, actor Actor, active []SubscriptionView, conv *converter, mainCurrency string, missing map[string]bool) (StoppedSpend, error) {
	out := StoppedSpend{Yearly: money.Money{Currency: mainCurrency}, Subscriptions: []StoppedSubscription{}}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusCancelled)})
	if err != nil {
		return out, err
	}
	still := map[string]bool{}
	for _, v := range active {
		still[serviceKey(v.Name)] = true
	}
	for _, v := range list.Subscriptions {
		if still[serviceKey(v.Name)] || v.YearlyCost.Minor <= 0 {
			continue
		}
		yearly, ok := conv.toMain(ctx, v.YearlyCost)
		if !ok {
			missing[v.YearlyCost.Currency] = true
			continue
		}
		out.Yearly, _ = out.Yearly.Add(yearly)
		out.Count++
		out.Subscriptions = append(out.Subscriptions, StoppedSubscription{ID: v.ID, Name: v.Name, Yearly: v.YearlyCost, YearlyMain: yearly, Until: v.CancelledUntil})
	}
	slices.SortFunc(out.Subscriptions, func(a, b StoppedSubscription) int { return b.Until.Compare(a.Until) })
	return out, nil
}
