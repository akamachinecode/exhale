package app

import (
	"context"
	"errors"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/update"
)

// UpdateInfo is the running version, the newest release and how this copy
// of Exhale updates (ADR 0033).
type UpdateInfo = update.VersionInfo

// GetUpdateInput asks for the newest release.
type GetUpdateInput struct {
	Refresh bool `json:"refresh,omitempty" query:"refresh" jsonschema:"ask GitHub again instead of using the last check (at most once a minute)"`
}

// ApplyUpdateInput installs the newest release.
type ApplyUpdateInput struct {
	DryRun bool `json:"dryRun,omitempty" jsonschema:"only check that the update can start, and say from which version to which"`
}

// SetUpdater gives the service the updater of exhale serve.
func (s *Service) SetUpdater(u *update.Updater) { s.updater = u }

// GetUpdate says whether a newer Exhale is out and how to install it.
func (s *Service) GetUpdate(ctx context.Context, actor Actor, in GetUpdateInput) (UpdateInfo, error) {
	if s.updater == nil {
		return UpdateInfo{}, domain.Invalid("this process does not update Exhale: ask the Exhale server")
	}
	info := s.updater.Info(ctx, in.Refresh)
	if s.requireOwner(ctx, s.st.Q, actor) != nil {
		// Members see what is out; only owners install it.
		info.CanApply = false
	}
	return info, nil
}

// ApplyUpdate installs the newest release and restarts Exhale. Owners
// only. Like SetupLidClosed it changes the computer, not the database, so
// it has no transaction to roll back and no undo: the dry run checks.
func (s *Service) ApplyUpdate(ctx context.Context, actor Actor, in ApplyUpdateInput) (UpdateInfo, error) {
	if err := s.requireOwner(ctx, s.st.Q, actor); err != nil {
		return UpdateInfo{}, err
	}
	if !actor.CanWrite() {
		return UpdateInfo{}, domain.Fail(ErrForbidden, "%s has read-only access", actor.Label)
	}
	if s.updater == nil {
		return UpdateInfo{}, domain.Invalid("this process does not update Exhale: ask the Exhale server")
	}
	var (
		info UpdateInfo
		err  error
	)
	if in.DryRun {
		info, err = s.updater.Plan(ctx)
	} else {
		info, err = s.updater.Apply(ctx)
	}
	switch {
	case errors.Is(err, update.ErrBusy):
		return info, domain.Conflict("Exhale is already updating")
	case errors.Is(err, update.ErrUpToDate):
		return info, domain.Invalid("Exhale is already up to date")
	case errors.Is(err, update.ErrCannot):
		return info, domain.Invalid("Exhale cannot update itself here: see how in get_update")
	case errors.Is(err, update.ErrNotYet):
		return info, domain.Invalid("Exhale could not find out the newest version yet")
	case err != nil:
		return info, domain.Invalid("Exhale could not reach GitHub: %s", err.Error())
	}
	if !in.DryRun {
		s.log.Info("update started", "by", actor.Label, "from", info.Current, "to", info.Latest)
	}
	return info, nil
}
