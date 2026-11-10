package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Managing the people who share this Exhale. Like the other account
// endpoints it is not in the op catalog: agents do not add or remove people.

type memberRename struct {
	Body struct {
		ID   string `json:"id" doc:"Member to rename; yourself, or anyone if you are an owner."`
		Name string `json:"name" minLength:"1" maxLength:"60"`
	}
}

type memberRole struct {
	Body struct {
		ID   string `json:"id"`
		Role string `json:"role" enum:"owner,member"`
	}
}

type memberRemove struct {
	Body struct {
		ID      string `json:"id" doc:"Member to remove; your own id to leave."`
		PayerTo string `json:"payerTo,omitempty" doc:"Member who takes over what they paid for. Default: you."`
	}
}

// MemberDone confirms a change to the members.
type MemberDone struct {
	OK bool `json:"ok"`
}

func registerMembers(api huma.API, a *authAPI) {
	op := func(id, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodPost, Path: "/api/v1/members" + path, Summary: summary, Tags: []string{"Members"}}
	}
	done := func(ctx context.Context, err error) (*resp[MemberDone], error) {
		if err != nil {
			return nil, httpError(err)
		}
		if actor, ok := actorFrom(ctx); ok {
			a.svc.Bus().Publish(bus.Event{Type: bus.HouseholdUpdated, HouseholdID: actor.HouseholdID})
		}
		return &resp[MemberDone]{Body: MemberDone{OK: true}}, nil
	}
	huma.Register(api, op("members_rename", "/rename", "Rename a member"), func(ctx context.Context, in *memberRename) (*resp[MemberDone], error) {
		actor, err := a.member(ctx)
		if err != nil {
			return nil, err
		}
		return done(ctx, auth.RenameMember(ctx, a.svc.Store(), actor, in.Body.ID, in.Body.Name))
	})
	huma.Register(api, op("members_set_role", "/role", "Make a member an owner or a member (owners only)"), func(ctx context.Context, in *memberRole) (*resp[MemberDone], error) {
		actor, err := a.member(ctx)
		if err != nil {
			return nil, err
		}
		return done(ctx, auth.SetMemberRole(ctx, a.svc.Store(), actor, in.Body.ID, domain.Role(in.Body.Role)))
	})
	huma.Register(api, op("members_remove", "/remove", "Remove a member, or leave"), func(ctx context.Context, in *memberRemove) (*resp[MemberDone], error) {
		actor, err := a.member(ctx)
		if err != nil {
			return nil, err
		}
		return done(ctx, auth.RemoveMember(ctx, a.svc.Store(), actor, in.Body.ID, in.Body.PayerTo))
	})
}
