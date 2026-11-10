package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Managing the people who share an Exhale (ADR 0006): an owner can remove a
// member or change who is an owner; anyone can rename themselves. There is
// always at least one owner.

func memberOf(ctx context.Context, st *store.Store, actor app.Actor, id string) (db.Member, error) {
	m, err := st.Q.GetMember(ctx, id)
	if err != nil || m.HouseholdID != actor.HouseholdID {
		return db.Member{}, fmt.Errorf("member %s: %w", id, domain.ErrNotFound)
	}
	return m, nil
}

func lastOwner(ctx context.Context, q *db.Queries, m db.Member) (bool, error) {
	if m.Role != string(domain.RoleOwner) {
		return false, nil
	}
	n, err := q.CountOwners(ctx, m.HouseholdID)
	return n <= 1, err
}

// RenameMember changes a member's display name. Members rename themselves;
// owners can rename anyone.
func RenameMember(ctx context.Context, st *store.Store, actor app.Actor, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return fmt.Errorf("%w: the name must have 1 to 60 characters", domain.ErrValidation)
	}
	m, err := memberOf(ctx, st, actor, id)
	if err != nil {
		return err
	}
	if m.ID != actor.MemberID {
		if err := requireOwner(ctx, st, actor); err != nil {
			return err
		}
	}
	return st.Q.UpdateMemberName(ctx, db.UpdateMemberNameParams{Name: name, ID: m.ID, HouseholdID: m.HouseholdID})
}

// SetMemberRole makes a member an owner or a plain member. Owners only; the
// last owner cannot be demoted.
func SetMemberRole(ctx context.Context, st *store.Store, actor app.Actor, id string, role domain.Role) error {
	if role != domain.RoleOwner && role != domain.RoleMember {
		return fmt.Errorf("%w: role must be owner or member", domain.ErrValidation)
	}
	if err := requireOwner(ctx, st, actor); err != nil {
		return err
	}
	m, err := memberOf(ctx, st, actor, id)
	if err != nil {
		return err
	}
	return st.Tx(ctx, func(q *db.Queries) error {
		if last, err := lastOwner(ctx, q, m); err != nil {
			return err
		} else if last && role != domain.RoleOwner {
			return fmt.Errorf("%w: make someone else an owner first", domain.ErrValidation)
		}
		return q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{Role: string(role), ID: m.ID, HouseholdID: m.HouseholdID})
	})
}

// RemoveMember removes a member with their chats, tokens and sessions. What
// they paid for moves to another member (the caller by default) so no
// subscription is left without a payer. Owners remove others; anyone can
// leave, except the last owner.
func RemoveMember(ctx context.Context, st *store.Store, actor app.Actor, id, payerTo string) error {
	m, err := memberOf(ctx, st, actor, id)
	if err != nil {
		return err
	}
	leaving := m.ID == actor.MemberID
	if !leaving {
		if err := requireOwner(ctx, st, actor); err != nil {
			return err
		}
	}
	if payerTo == "" && !leaving {
		payerTo = actor.MemberID
	}
	if payerTo == m.ID {
		return fmt.Errorf("%w: choose someone else to take over the payments", domain.ErrValidation)
	}
	if payerTo != "" {
		if _, err := memberOf(ctx, st, actor, payerTo); err != nil {
			return err
		}
	}
	return st.Tx(ctx, func(q *db.Queries) error {
		if last, err := lastOwner(ctx, q, m); err != nil {
			return err
		} else if last {
			return fmt.Errorf("%w: the only owner cannot leave; make someone else an owner first", domain.ErrValidation)
		}
		if err := q.ReassignPayer(ctx, db.ReassignPayerParams{ToID: store.Ptr(payerTo), UpdatedAt: store.FormatTime(time.Now()), HouseholdID: m.HouseholdID, FromID: store.Ptr(m.ID)}); err != nil {
			return err
		}
		return q.DeleteMember(ctx, db.DeleteMemberParams{ID: m.ID, HouseholdID: m.HouseholdID})
	})
}
