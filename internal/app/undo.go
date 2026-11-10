package app

import (
	"context"
	"encoding/json"
	"slices"

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
	AuditID string `json:"auditId,omitempty" jsonschema:"id returned by a write (aud_…)"`
	// AuditIDs revert several changes together, e.g. every change of a bulk
	// edit: all of them or none.
	AuditIDs []string `json:"auditIds,omitempty" jsonschema:"several changes to revert together, all or none, e.g. the auditIds of bulk_update_subscriptions"`
	DryRun   bool     `json:"dryRun,omitempty"`
}

// UndoResult reports what was restored.
type UndoResult struct {
	Restored string `json:"restored" jsonschema:"entity id that was restored"`
	AuditID  string `json:"auditId"`
	// Undone counts the changes reverted (more than one with auditIds).
	Undone int `json:"undone"`
}

// Undo reverts a recorded change by restoring the entity's previous state
// (ADR 0018: every agent change is reversible).
func (s *Service) Undo(ctx context.Context, actor Actor, in UndoInput) (UndoResult, error) {
	ids := []string{}
	for _, id := range append([]string{in.AuditID}, in.AuditIDs...) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > MaxBulk {
		return UndoResult{}, domain.Invalid("pass auditId, or between 1 and %d auditIds", MaxBulk)
	}
	var out UndoResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		// Newest first, so changes to the same thing unwind in order.
		for i := len(ids) - 1; i >= 0; i-- {
			r, err := s.undoOne(ctx, q, c, actor, ids[i])
			if err != nil {
				return err
			}
			if out.AuditID == "" {
				out = r
			}
			out.Undone++
		}
		return nil
	})
	return out, err
}

// undoOne reverts one recorded change inside a write.
func (s *Service) undoOne(ctx context.Context, q *db.Queries, c *change, actor Actor, auditID string) (UndoResult, error) {
	a, err := q.GetAudit(ctx, auditID)
	if err != nil {
		return UndoResult{}, store.NotFound(err, "change %s: not found", auditID)
	}
	if a.UndoneBy != "" {
		return UndoResult{}, domain.Invalid("change %s was already undone", a.ID)
	}
	switch a.Entity {
	case "charge":
		var before domain.Charge
		if err := json.Unmarshal([]byte(a.Before), &before); err != nil {
			return UndoResult{}, err
		}
		cur, sub, err := s.charge(ctx, q, actor, a.EntityID)
		if err != nil {
			return UndoResult{}, err
		}
		before.Version = cur.Version
		if err := s.saveCharge(ctx, q, before); err != nil {
			return UndoResult{}, err
		}
		c.emit(bus.Event{Type: bus.ChargeUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: before.ID})
	case "subscription":
		if err := s.undoSubscription(ctx, q, actor, c, a); err != nil {
			return UndoResult{}, err
		}
	case "document", "logo":
		if err := s.undoFile(ctx, q, actor, c, a); err != nil {
			return UndoResult{}, err
		}
	default:
		return UndoResult{}, domain.Invalid("changes to %s cannot be undone yet", a.Entity)
	}
	id, err := c.record(q, "undo", a.Entity, a.EntityID, nil, map[string]string{"undid": a.ID})
	if err != nil {
		return UndoResult{}, err
	}
	if err := q.MarkAuditUndone(ctx, db.MarkAuditUndoneParams{UndoneBy: id, ID: a.ID}); err != nil {
		return UndoResult{}, err
	}
	return UndoResult{Restored: a.EntityID, AuditID: id}, nil
}

func (s *Service) undoSubscription(ctx context.Context, q *db.Queries, actor Actor, c *change, a db.AuditLog) error {
	today := s.today(ctx, q, actor)
	switch {
	case a.Before == "": // undo a creation: delete it
		if _, err := trashFilesOf(ctx, q, domain.Subscription{ID: a.EntityID, HouseholdID: actor.HouseholdID}, c.now); err != nil {
			return err
		}
		if err := q.DeleteSubscription(ctx, db.DeleteSubscriptionParams{ID: a.EntityID, HouseholdID: actor.HouseholdID}); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.SubscriptionDeleted, HouseholdID: actor.HouseholdID, SubscriptionID: a.EntityID})
		return nil
	case a.After == "": // undo a deletion: recreate it
		var gone deletedSubscription
		if err := json.Unmarshal([]byte(a.Before), &gone); err != nil {
			return err
		}
		sub := gone.Subscription
		if err := s.insertSubscription(ctx, q, sub); err != nil {
			return err
		}
		for _, ch := range gone.Charges {
			p := store.ChargeUpdate(ch, ch.UpdatedAt)
			if err := q.RestoreCharge(ctx, db.RestoreChargeParams{
				ID: ch.ID, SubscriptionID: sub.ID, OriginalDate: ch.OriginalDate.String(), DueDate: p.DueDate, AmountMinor: p.AmountMinor,
				Currency: p.Currency, ChargedMinor: p.ChargedMinor, ChargedCurrency: p.ChargedCurrency, Status: p.Status,
				ResolvedAt: p.ResolvedAt, Note: p.Note, Decision: p.Decision, UpdatedAt: p.UpdatedAt, Version: int64(ch.Version),
			}); err != nil {
				return err
			}
		}
		if err := restoreFiles(ctx, q, sub.HouseholdID, gone.Files); err != nil {
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
