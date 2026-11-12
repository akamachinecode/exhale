// Package app (piece ⑦) holds every business operation of Exhale. The web,
// the chat adapters, the scheduler and AI agents all call the same use cases
// (ADR 0003, 0018).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
	"github.com/akamachinecode/exhale/internal/update"
)

// ActorKind says who performs an operation.
type ActorKind string

const (
	ActorMember ActorKind = "member"
	ActorAgent  ActorKind = "agent"
	ActorSystem ActorKind = "system"
)

// Scope is what an actor may do (ADR 0018).
type Scope string

const (
	ScopeRead    Scope = "read"
	ScopeWrite   Scope = "write"
	ScopeTrusted Scope = "trusted"
)

// Actor is the authenticated caller of a use case.
type Actor struct {
	Kind        ActorKind
	MemberID    string
	HouseholdID string
	Label       string // "Alex", "Claude Desktop (MCP)", "scheduler"
	Scope       Scope
}

// CanWrite reports whether the actor may change data.
func (a Actor) CanWrite() bool {
	return a.Kind == ActorSystem || a.Scope == ScopeWrite || a.Scope == ScopeTrusted
}

// ErrForbidden is returned when the actor lacks the required scope.
var ErrForbidden = errors.New("forbidden")

// ErrDryRun is used internally to roll back preview transactions.
var errDryRun = errors.New("dry run")

// AuditRef identifies a change recorded in the audit log.
type AuditRef struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Entity   string `json:"entity"`
	EntityID string `json:"entityId"`
}

type auditSinkKey struct{}

// CollectAudits returns a context that records every change committed with
// it. Adapters that must offer undo for any use case (the in-app assistant)
// read the ids from the returned slice after the call, without each use
// case having to return them.
func CollectAudits(ctx context.Context) (context.Context, *[]AuditRef) {
	refs := &[]AuditRef{}
	return context.WithValue(ctx, auditSinkKey{}, refs), refs
}

// Service implements the use cases.
type Service struct {
	st  *store.Store
	bus *bus.Bus
	log *slog.Logger
	now func() time.Time
	// keepAwake applies the keep-awake setting (nil outside exhale serve).
	keepAwake *KeepAwake
	// updater checks for and installs new versions (nil outside exhale serve).
	updater *update.Updater
}

// New returns a Service. now may be nil (time.Now).
func New(st *store.Store, b *bus.Bus, log *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{st: st, bus: b, log: log, now: now}
}

// Now returns the service clock.
func (s *Service) Now() time.Time { return s.now() }

// Store returns the underlying store (for adapters that need raw queries,
// such as message bookkeeping).
func (s *Service) Store() *store.Store { return s.st }

// Bus returns the event bus.
func (s *Service) Bus() *bus.Bus { return s.bus }

// change is the bookkeeping of one write: audit entries and events are
// collected inside the transaction and only published after commit.
type change struct {
	actor  Actor
	audits []db.CreateAuditParams
	events []bus.Event
	now    time.Time
}

func (c *change) record(q *db.Queries, action, entity, id string, before, after any) (string, error) {
	b, err := marshalOrEmpty(before)
	if err != nil {
		return "", err
	}
	a, err := marshalOrEmpty(after)
	if err != nil {
		return "", err
	}
	p := db.CreateAuditParams{
		ID: domain.NewID("aud"), At: store.FormatTime(c.now), ActorKind: string(c.actor.Kind),
		ActorID: c.actor.MemberID, ActorLabel: c.actor.Label, Action: action, Entity: entity,
		EntityID: id, Before: b, After: a,
	}
	if err := q.CreateAudit(context.Background(), p); err != nil {
		return "", err
	}
	c.audits = append(c.audits, p)
	return p.ID, nil
}

func (c *change) emit(e bus.Event) {
	e.ActorLabel = c.actor.Label
	e.At = c.now
	c.events = append(c.events, e)
}

func marshalOrEmpty(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// write runs fn in a transaction on behalf of actor. With dryRun the
// transaction is rolled back and no events are published, so the caller
// gets an exact preview of the result.
func (s *Service) write(ctx context.Context, actor Actor, dryRun bool, fn func(q *db.Queries, c *change) error) error {
	if !actor.CanWrite() {
		return domain.Fail(ErrForbidden, "%s has read-only access", actor.Label)
	}
	c := &change{actor: actor, now: s.now()}
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		if err := fn(q, c); err != nil {
			return err
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if dryRun && errors.Is(err, errDryRun) {
		return nil
	}
	if err != nil {
		return err
	}
	if sink, ok := ctx.Value(auditSinkKey{}).(*[]AuditRef); ok {
		for _, a := range c.audits {
			*sink = append(*sink, AuditRef{ID: a.ID, Action: a.Action, Entity: a.Entity, EntityID: a.EntityID})
		}
	}
	for _, e := range c.events {
		if len(c.audits) > 0 && e.AuditID == "" {
			e.AuditID = c.audits[len(c.audits)-1].ID
		}
		s.bus.Publish(e)
	}
	return nil
}

// household loads the actor's household.
func (s *Service) household(ctx context.Context, q *db.Queries, id string) (domain.Household, error) {
	h, err := q.GetHousehold(ctx, id)
	if err != nil {
		return domain.Household{}, store.NotFound(err, "household: not found")
	}
	return store.HouseholdFromRow(h), nil
}

func (s *Service) subscription(ctx context.Context, q *db.Queries, actor Actor, id string) (domain.Subscription, error) {
	r, err := q.GetSubscription(ctx, db.GetSubscriptionParams{ID: id, HouseholdID: actor.HouseholdID})
	if err != nil {
		return domain.Subscription{}, store.NotFound(err, "subscription %s: not found", id)
	}
	sub, err := store.SubscriptionFromRow(r)
	if err != nil {
		return sub, err
	}
	if actor.Kind == ActorMember || actor.Kind == ActorAgent {
		if !sub.VisibleTo(actor.MemberID) {
			return domain.Subscription{}, domain.Missing("subscription %s: not found", id)
		}
	}
	return sub, nil
}

func (s *Service) charge(ctx context.Context, q *db.Queries, actor Actor, id string) (domain.Charge, domain.Subscription, error) {
	r, err := q.GetCharge(ctx, id)
	if err != nil {
		return domain.Charge{}, domain.Subscription{}, store.NotFound(err, "charge %s: not found", id)
	}
	sub, err := s.subscription(ctx, q, actor, r.SubscriptionID)
	if err != nil {
		return domain.Charge{}, domain.Subscription{}, err
	}
	return store.ChargeFromRow(r), sub, nil
}
