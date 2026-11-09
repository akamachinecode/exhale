package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// AuditEntry is one recorded change.
type AuditEntry struct {
	ID         string `json:"id"`
	At         string `json:"at"`
	ActorKind  string `json:"actorKind"`
	ActorLabel string `json:"actor"`
	Action     string `json:"action"`
	Entity     string `json:"entity"`
	EntityID   string `json:"entityId"`
	UndoneBy   string `json:"undoneBy,omitempty"`
}

// AuditLogInput limits the audit log.
type AuditLogInput struct {
	Limit int `json:"limit,omitempty" query:"limit" jsonschema:"default 50" maximum:"500"`
}

// AuditLog is the list of recent changes.
type AuditLog struct {
	Entries []AuditEntry `json:"entries"`
}

// ListAudit returns recent changes made by members, agents and the system.
func (s *Service) ListAudit(ctx context.Context, _ Actor, in AuditLogInput) (AuditLog, error) {
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 50
	}
	rows, err := s.st.Q.ListAudit(ctx, int64(in.Limit))
	if err != nil {
		return AuditLog{}, err
	}
	out := AuditLog{Entries: []AuditEntry{}}
	for _, r := range rows {
		out.Entries = append(out.Entries, AuditEntry{ID: r.ID, At: r.At, ActorKind: r.ActorKind, ActorLabel: r.ActorLabel, Action: r.Action, Entity: r.Entity, EntityID: r.EntityID, UndoneBy: r.UndoneBy})
	}
	return out, nil
}

// UndoInput identifies the change to revert.
type UndoInput struct {
	AuditID string `json:"auditId" jsonschema:"id returned by a write (aud_…)"`
	DryRun  bool   `json:"dryRun,omitempty"`
}

// UndoResult reports what was restored.
type UndoResult struct {
	Restored string `json:"restored" jsonschema:"entity id that was restored"`
	AuditID  string `json:"auditId"`
}

// Undo reverts a recorded change by restoring the entity's previous state
// (ADR 0018: every agent change is reversible).
func (s *Service) Undo(ctx context.Context, actor Actor, in UndoInput) (UndoResult, error) {
	var out UndoResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		a, err := q.GetAudit(ctx, in.AuditID)
		if err != nil {
			return store.NotFound(err, "change "+in.AuditID)
		}
		if a.UndoneBy != "" {
			return fmt.Errorf("%w: change %s was already undone", domain.ErrValidation, a.ID)
		}
		switch a.Entity {
		case "charge":
			var before domain.Charge
			if err := json.Unmarshal([]byte(a.Before), &before); err != nil {
				return err
			}
			cur, sub, err := s.charge(ctx, q, actor, a.EntityID)
			if err != nil {
				return err
			}
			before.Version = cur.Version
			if err := s.saveCharge(ctx, q, before); err != nil {
				return err
			}
			c.emit(bus.Event{Type: bus.ChargeUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: before.ID})
		case "subscription":
			if err := s.undoSubscription(ctx, q, actor, c, a); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: changes to %s cannot be undone yet", domain.ErrValidation, a.Entity)
		}
		id, err := c.record(q, "undo", a.Entity, a.EntityID, nil, map[string]string{"undid": a.ID})
		if err != nil {
			return err
		}
		if err := q.MarkAuditUndone(ctx, db.MarkAuditUndoneParams{UndoneBy: id, ID: a.ID}); err != nil {
			return err
		}
		out = UndoResult{Restored: a.EntityID, AuditID: id}
		return nil
	})
	return out, err
}

func (s *Service) undoSubscription(ctx context.Context, q *db.Queries, actor Actor, c *change, a db.AuditLog) error {
	today := s.today(ctx, q, actor)
	switch {
	case a.Before == "": // undo a creation: delete it
		if err := q.DeleteSubscription(ctx, db.DeleteSubscriptionParams{ID: a.EntityID, HouseholdID: actor.HouseholdID}); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionDeleted, HouseholdID: actor.HouseholdID, SubscriptionID: a.EntityID})
		return nil
	case a.After == "": // undo a deletion: recreate it
		var sub domain.Subscription
		if err := json.Unmarshal([]byte(a.Before), &sub); err != nil {
			return err
		}
		if err := s.insertSubscription(ctx, q, sub); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionCreated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
		return s.materialize(ctx, q, sub, today)
	default: // undo an update: restore the previous state
		var before domain.Subscription
		if err := json.Unmarshal([]byte(a.Before), &before); err != nil {
			return err
		}
		cur, err := s.subscription(ctx, q, actor, a.EntityID)
		if err != nil {
			return err
		}
		before.Version = cur.Version
		if err := s.saveSubscription(ctx, q, before); err != nil {
			return err
		}
		if err := q.DeleteFuturePendingCharges(ctx, db.DeleteFuturePendingChargesParams{SubscriptionID: before.ID, OriginalDate: today.String()}); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: before.HouseholdID, SubscriptionID: before.ID})
		return s.materialize(ctx, q, before, today)
	}
}
