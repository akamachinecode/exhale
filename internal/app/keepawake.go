package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/akamachinecode/exhale/internal/awake"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Keep this computer awake (ADR 0028): on a desktop that sleeps, reminders
// are not sent and chat buttons stop answering. The owner chooses when
// Exhale keeps the machine from sleeping; KeepAwake applies it.

// keepAwakeKey is the settings key of the choice, stored as JSON.
const keepAwakeKey = "keep_awake"

// Keep-awake modes.
const (
	KeepAwakeOff    = "off"
	KeepAwakeAlways = "always"
	KeepAwakeHours  = "hours"
)

// Keep-awake states, as the web shows them.
const (
	AwakeOff         = "off"         // the owner turned it off
	AwakeActive      = "active"      // the machine is kept awake now
	AwakeWaiting     = "waiting"     // outside the hours
	AwakeUnavailable = "unavailable" // not possible here (see Reason)
	AwakeFailed      = "failed"      // the system refused (see Error)
	AwakeUnknown     = "unknown"     // this process does not apply it (exhale mcp)
)

// KeepAwakeSetting is when Exhale keeps the computer it runs on awake.
type KeepAwakeSetting struct {
	Mode string `json:"mode" enum:"off,always,hours" jsonschema:"off: the computer sleeps as usual; always: kept awake while Exhale runs; hours: only between from and to"`
	From string `json:"from" jsonschema:"start of the hours, HH:MM in the household time zone"`
	To   string `json:"to" jsonschema:"end of the hours, HH:MM in the household time zone; earlier than from for hours that cross midnight"`
	// The system is kept awake; the display may still sleep unless this is set.
	KeepDisplayOn bool `json:"keepDisplayOn" jsonschema:"also keep the display on (default false: only the system stays awake)"`
}

func defaultKeepAwake() KeepAwakeSetting {
	return KeepAwakeSetting{Mode: KeepAwakeOff, From: "08:00", To: "23:00"}
}

// KeepAwakeStatus says whether the computer is kept awake right now.
type KeepAwakeStatus struct {
	State string `json:"state" enum:"off,active,waiting,unavailable,failed,unknown" jsonschema:"active: kept awake now; waiting: outside the hours; unavailable: not possible on this machine (reason); failed: the system refused (error); unknown: the server process applies it, not this one"`
	// Why it is unavailable.
	Reason string `json:"reason,omitempty" enum:"container,unsupported,other" jsonschema:"container: Exhale runs in a container; unsupported: this operating system; other: see error"`
	Error  string `json:"error,omitempty" jsonschema:"what the system said, in its words"`
}

// KeepAwakeInfo is the setting with its live status.
type KeepAwakeInfo struct {
	KeepAwakeSetting
	Timezone string          `json:"timezone" jsonschema:"the time zone of from and to (the household's)"`
	Status   KeepAwakeStatus `json:"status"`
}

// SetKeepAwakeInput changes when the computer is kept awake. Only the
// fields you pass change.
type SetKeepAwakeInput struct {
	Mode          *string `json:"mode,omitempty" enum:"off,always,hours" jsonschema:"off: the computer sleeps as usual; always: kept awake while Exhale runs; hours: only between from and to"`
	From          *string `json:"from,omitempty" jsonschema:"start of the hours, HH:MM in the household time zone"`
	To            *string `json:"to,omitempty" jsonschema:"end of the hours, HH:MM; earlier than from for hours that cross midnight, e.g. 20:00 to 02:00"`
	KeepDisplayOn *bool   `json:"keepDisplayOn,omitempty" jsonschema:"also keep the display on; false lets it sleep while the system stays awake"`
	DryRun        bool    `json:"dryRun,omitempty"`
}

// GetKeepAwake returns the keep-awake setting and whether it is in effect.
func (s *Service) GetKeepAwake(ctx context.Context, _ Actor, _ struct{}) (KeepAwakeInfo, error) {
	set, _ := readKeepAwake(ctx, s.st.Q)
	info := KeepAwakeInfo{KeepAwakeSetting: set, Timezone: s.awakeLocation(ctx).String(), Status: KeepAwakeStatus{State: AwakeUnknown}}
	if k := s.keepAwake; k != nil {
		info.Status = k.Status()
	}
	return info, nil
}

// SetKeepAwake changes when the computer Exhale runs on is kept awake.
// Owners only.
func (s *Service) SetKeepAwake(ctx context.Context, actor Actor, in SetKeepAwakeInput) (KeepAwakeInfo, error) {
	var set KeepAwakeSetting
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if err := s.requireOwner(ctx, q, actor); err != nil {
			return err
		}
		before, stored := readKeepAwake(ctx, q)
		set = before
		if in.Mode != nil {
			switch *in.Mode {
			case KeepAwakeOff, KeepAwakeAlways, KeepAwakeHours:
				set.Mode = *in.Mode
			default:
				return domain.Invalid("unknown mode %q", *in.Mode)
			}
		}
		for _, f := range []struct {
			in  *string
			out *string
		}{{in.From, &set.From}, {in.To, &set.To}} {
			if f.in == nil {
				continue
			}
			t, err := time.Parse("15:04", strings.TrimSpace(*f.in))
			if err != nil {
				return domain.Invalid("times must look like 08:00")
			}
			*f.out = t.Format("15:04")
		}
		if in.KeepDisplayOn != nil {
			set.KeepDisplayOn = *in.KeepDisplayOn
		}
		if set.Mode == KeepAwakeHours && set.From == set.To {
			return domain.Invalid("the hours must start and end at different times")
		}
		if stored && set == before {
			return nil
		}
		b, err := json.Marshal(set)
		if err != nil {
			return err
		}
		if err := q.PutSetting(ctx, db.PutSettingParams{Key: keepAwakeKey, Value: string(b)}); err != nil {
			return err
		}
		var prev any
		if stored {
			prev = before
		}
		if _, err := c.record(q, "keep_awake.update", "setting", keepAwakeKey, prev, set); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.KeepAwakeUpdated, HouseholdID: actor.HouseholdID, EntityID: keepAwakeKey})
		return nil
	})
	if err != nil {
		return KeepAwakeInfo{}, err
	}
	if s.keepAwake != nil && !in.DryRun {
		s.keepAwake.Apply(ctx) // the status the answer shows is the new one
	}
	info, err := s.GetKeepAwake(ctx, actor, struct{}{})
	info.KeepAwakeSetting = set // a dry run was rolled back
	return info, err
}

// undoSetting restores a setting recorded by a change (keep_awake is the
// only one recorded so far).
func (s *Service) undoSetting(ctx context.Context, q *db.Queries, actor Actor, c *change, a db.AuditLog) error {
	if err := s.requireOwner(ctx, q, actor); err != nil {
		return err
	}
	if a.Before == "" {
		if err := q.DeleteSetting(ctx, a.EntityID); err != nil {
			return err
		}
	} else if err := q.PutSetting(ctx, db.PutSettingParams{Key: a.EntityID, Value: a.Before}); err != nil {
		return err
	}
	c.emit(bus.Event{Type: bus.KeepAwakeUpdated, HouseholdID: actor.HouseholdID, EntityID: a.EntityID})
	return nil
}

// requireOwner refuses members who are not owners of the household.
func (s *Service) requireOwner(ctx context.Context, q *db.Queries, actor Actor) error {
	if actor.Kind == ActorSystem {
		return nil
	}
	m, err := q.GetMember(ctx, actor.MemberID)
	if err != nil || m.Role != string(domain.RoleOwner) {
		return domain.Fail(ErrForbidden, "only a household owner can change its settings")
	}
	return nil
}

// readKeepAwake returns the stored setting, or the default (stored false).
func readKeepAwake(ctx context.Context, q *db.Queries) (KeepAwakeSetting, bool) {
	set := defaultKeepAwake()
	v, err := q.GetSetting(ctx, keepAwakeKey)
	if err != nil {
		return set, false
	}
	if json.Unmarshal([]byte(v), &set) != nil {
		return defaultKeepAwake(), false
	}
	return set, true
}

// awakeLocation is the time zone of the hours: the household's.
func (s *Service) awakeLocation(ctx context.Context) *time.Location {
	h, err := s.st.Q.FirstHousehold(ctx)
	if err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// clockMinutes parses HH:MM into minutes after midnight.
func clockMinutes(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// wanted reports whether the machine should be kept awake at now (in the
// hours' time zone) and when that next changes (zero: never by itself).
func wanted(k KeepAwakeSetting, now time.Time) (bool, time.Time) {
	switch k.Mode {
	case KeepAwakeAlways:
		return true, time.Time{}
	case KeepAwakeHours:
		from, ok1 := clockMinutes(k.From)
		to, ok2 := clockMinutes(k.To)
		if !ok1 || !ok2 || from == to {
			return false, time.Time{}
		}
		m := now.Hour()*60 + now.Minute()
		var on bool
		if from < to {
			on = m >= from && m < to
		} else { // crosses midnight, e.g. 20:00 to 02:00
			on = m >= from || m < to
		}
		edge := from
		if on {
			edge = to
		}
		return on, nextClock(now, edge)
	default:
		return false, time.Time{}
	}
}

// nextClock is the first time after now whose wall clock reads minutes.
func nextClock(now time.Time, minutes int) time.Time {
	y, mo, d := now.Date()
	t := time.Date(y, mo, d, minutes/60, minutes%60, 0, 0, now.Location())
	if !t.After(now) {
		t = time.Date(y, mo, d+1, minutes/60, minutes%60, 0, 0, now.Location())
	}
	return t
}

// KeepAwake applies the keep-awake setting with a Keeper: when the server
// starts, on every change and at the edges of the hours. It is a
// supervised service of exhale serve.
type KeepAwake struct {
	svc    *Service
	keeper awake.Keeper
	log    *slog.Logger
	// MaxSleep bounds every wait, so a change made by another process
	// (exhale mcp) or a clock jump is noticed.
	MaxSleep time.Duration
	// RetryAfter is how long a refused hold waits before it is tried again
	// with the same setting.
	RetryAfter time.Duration

	mu       sync.Mutex
	held     bool
	display  bool
	status   KeepAwakeStatus
	failedAt time.Time
	failedOn KeepAwakeSetting
}

// NewKeepAwake returns the controller and attaches it to svc, whose
// keep-awake operations then report its status.
func NewKeepAwake(svc *Service, keeper awake.Keeper, log *slog.Logger) *KeepAwake {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	k := &KeepAwake{svc: svc, keeper: keeper, log: log, MaxSleep: time.Minute, RetryAfter: 15 * time.Minute,
		status: KeepAwakeStatus{State: AwakeUnknown}}
	svc.keepAwake = k
	return k
}

func (k *KeepAwake) String() string { return "keep-awake" }

// Status is the state after the last Apply.
func (k *KeepAwake) Status() KeepAwakeStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.status
}

// Serve applies the setting until ctx is cancelled, then lets the machine
// sleep again.
func (k *KeepAwake) Serve(ctx context.Context) error {
	events, stop := k.svc.Bus().Subscribe(16)
	defer stop()
	defer k.Release()
	for {
		next := k.Apply(ctx)
		wait := k.MaxSleep
		if !next.IsZero() {
			if d := next.Sub(k.svc.Now()); d < wait {
				wait = max(d, 0)
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-events: // a change of the setting or of the time zone
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Release lets the machine sleep again.
func (k *KeepAwake) Release() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.release()
}

func (k *KeepAwake) release() {
	if k.held {
		k.keeper.Release()
		k.held = false
		k.log.Info("this computer may sleep again")
	}
}

// Apply holds or releases the machine as the setting asks now, and returns
// when that next changes by itself (zero: not by itself).
func (k *KeepAwake) Apply(ctx context.Context) time.Time {
	set, _ := readKeepAwake(ctx, k.svc.st.Q)
	now := k.svc.Now().In(k.svc.awakeLocation(ctx))
	on, next := wanted(set, now)
	k.mu.Lock()
	prev := k.status
	k.status = k.apply(set, on, now)
	st := k.status
	k.mu.Unlock()
	if st != prev {
		if st.State == AwakeFailed || st.State == AwakeUnavailable {
			k.log.Warn("cannot keep this computer awake", "state", st.State, "reason", st.Reason, "err", st.Error)
		}
		if h, err := k.svc.st.Q.FirstHousehold(ctx); err == nil {
			k.svc.Bus().Publish(bus.Event{Type: bus.KeepAwakeUpdated, HouseholdID: h.ID, EntityID: keepAwakeKey})
		}
	}
	return next
}

func (k *KeepAwake) apply(set KeepAwakeSetting, on bool, now time.Time) KeepAwakeStatus {
	if err := k.keeper.Available(); err != nil {
		k.release()
		st := KeepAwakeStatus{State: AwakeUnavailable, Reason: "other", Error: err.Error()}
		switch {
		case errors.Is(err, awake.ErrContainer):
			st.Reason, st.Error = "container", ""
		case errors.Is(err, awake.ErrUnsupported):
			st.Reason, st.Error = "unsupported", ""
		}
		return st
	}
	if k.held {
		if err := k.keeper.Lost(); err != nil {
			k.log.Warn("this computer was no longer kept awake", "err", err)
			k.keeper.Release()
			k.held = false
		}
	}
	if !on {
		k.release()
		k.failedAt = time.Time{}
		if set.Mode == KeepAwakeOff {
			return KeepAwakeStatus{State: AwakeOff}
		}
		return KeepAwakeStatus{State: AwakeWaiting}
	}
	if k.held && k.display == set.KeepDisplayOn {
		return KeepAwakeStatus{State: AwakeActive}
	}
	if !k.failedAt.IsZero() && k.failedOn == set && now.Sub(k.failedAt) < k.RetryAfter && k.status.State == AwakeFailed {
		return k.status
	}
	k.release()
	if err := k.keeper.Hold(set.KeepDisplayOn); err != nil {
		k.failedAt, k.failedOn = now, set
		return KeepAwakeStatus{State: AwakeFailed, Error: err.Error()}
	}
	k.failedAt = time.Time{}
	k.held, k.display = true, set.KeepDisplayOn
	k.log.Info("keeping this computer awake", "display", set.KeepDisplayOn)
	return KeepAwakeStatus{State: AwakeActive}
}
