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

// Keep this computer awake (ADR 0028, 0032): on a desktop that sleeps,
// reminders are not sent and chat buttons stop answering. The owner chooses
// when Exhale keeps the machine from sleeping (a standing rule: always or
// between two times), may start a session on top of it (for an hour, until
// a time, until stopped), and may keep a laptop going with its lid closed,
// only on power, or down to a battery level. KeepAwake applies it.

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
	AwakePaused      = "paused"      // on battery: not plugged in, or below the floor (see Reason)
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
	LidClosed     bool `json:"lidClosed" jsonschema:"keep a laptop awake with its lid closed too (on a Mac, needs setup_lid_closed once)"`
	OnlyOnPower   bool `json:"onlyOnPower" jsonschema:"only while plugged in: a laptop on battery sleeps as usual"`
	BatteryFloor  int  `json:"batteryFloor" minimum:"0" maximum:"95" jsonschema:"on battery, stop keeping it awake at or below this charge, in percent (0: never stop)"`
	// Session keeps it awake on top of the rule, for a while or until stopped.
	Session KeepAwakeSession `json:"session,omitzero"`
}

// KeepAwakeSession keeps the computer awake now, whatever the rule says.
type KeepAwakeSession struct {
	Active bool `json:"active"`
	// Until is when it ends (RFC 3339); empty: when it is stopped.
	Until string `json:"until,omitempty" jsonschema:"when the session ends, RFC 3339; empty when it lasts until stopped"`
	// Left is the time it had left when the server stopped, in seconds:
	// a computer that restarts gets it back in full.
	Left int `json:"left,omitempty" jsonschema:"seconds the session had left when Exhale stopped; it resumes with them"`
}

func defaultKeepAwake() KeepAwakeSetting {
	return KeepAwakeSetting{Mode: KeepAwakeOff, From: "08:00", To: "23:00", BatteryFloor: 20}
}

// sessionEnd is when the session ends: zero when it is not active or lasts
// until stopped; ok is false when there is none now.
func sessionEnd(k KeepAwakeSetting, now time.Time) (end time.Time, ok bool) {
	if !k.Session.Active {
		return time.Time{}, false
	}
	if k.Session.Until == "" {
		return time.Time{}, true
	}
	end, err := time.Parse(time.RFC3339, k.Session.Until)
	if err != nil || !end.After(now) {
		return time.Time{}, false
	}
	return end, true
}

// KeepAwakeStatus says whether the computer is kept awake right now.
type KeepAwakeStatus struct {
	State string `json:"state" enum:"off,active,waiting,paused,unavailable,failed,unknown" jsonschema:"active: kept awake now; waiting: outside the hours; paused: on battery (reason); unavailable: not possible on this machine (reason); failed: the system refused (error); unknown: the server process applies it, not this one"`
	// Why it is paused or unavailable.
	Reason string `json:"reason,omitempty" enum:"container,unsupported,other,power,battery" jsonschema:"container: Exhale runs in a container; unsupported: this operating system; other: see error; power: on battery and set to keep awake only while plugged in; battery: the battery is at or below the floor"`
	Error  string `json:"error,omitempty" jsonschema:"what the system said, in its words"`
	// Until is when keeping awake ends by itself: the session's end, or the
	// end of the hours.
	Until string `json:"until,omitempty" jsonschema:"when it stops by itself, RFC 3339 (the session's end or the end of the hours); empty when it does not"`
	// Session is true when a session, not the rule, keeps it awake now.
	Session bool `json:"session" jsonschema:"a session started by hand keeps it awake now"`
	// Lid is whether a closed lid is kept awake too.
	Lid      string `json:"lid" enum:"off,active,setup,unavailable,failed" jsonschema:"off: not asked or not active now; active: a closed lid does not send it to sleep; setup: a Mac needs setup_lid_closed once; unavailable: not on this system (lidError); failed: the system refused (lidError)"`
	LidError string `json:"lidError,omitempty"`
	// Power is the computer's power source, when the system says.
	Power KeepAwakePower `json:"power,omitzero"`
}

// KeepAwakePower is the computer's power source.
type KeepAwakePower struct {
	HasBattery bool `json:"hasBattery" jsonschema:"false on a desktop"`
	OnBattery  bool `json:"onBattery"`
	Percent    int  `json:"percent" jsonschema:"battery charge, 0 to 100"`
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
	LidClosed     *bool   `json:"lidClosed,omitempty" jsonschema:"keep a laptop awake with its lid closed too (on a Mac, needs setup_lid_closed once)"`
	OnlyOnPower   *bool   `json:"onlyOnPower,omitempty" jsonschema:"only while plugged in"`
	BatteryFloor  *int    `json:"batteryFloor,omitempty" minimum:"0" maximum:"95" jsonschema:"on battery, stop at or below this charge in percent; 0 never stops"`
	DryRun        bool    `json:"dryRun,omitempty"`
}

// Session actions.
const (
	SessionStart  = "start"
	SessionExtend = "extend"
	SessionStop   = "stop"
)

// KeepAwakeSessionInput starts, extends or stops a session that keeps the
// computer awake now, whatever the rule says.
type KeepAwakeSessionInput struct {
	Action  string `json:"action" enum:"start,extend,stop" jsonschema:"start: keep it awake now (for minutes, until a time, or until stopped when neither is given); extend: add minutes to the session (starts one when there is none); stop: end the session, the rule applies again"`
	Minutes int    `json:"minutes,omitempty" minimum:"0" maximum:"10080" jsonschema:"how long, in minutes (start, extend)"`
	Until   string `json:"until,omitempty" jsonschema:"start only: until this time of day, HH:MM in the household time zone (the next time it comes)"`
	DryRun  bool   `json:"dryRun,omitempty"`
}

// SetupLidClosedInput asks this computer for the permission to keep it
// awake with its lid closed.
type SetupLidClosedInput struct {
	DryRun bool `json:"dryRun,omitempty"`
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
		if in.LidClosed != nil {
			set.LidClosed = *in.LidClosed
		}
		if in.OnlyOnPower != nil {
			set.OnlyOnPower = *in.OnlyOnPower
		}
		if in.BatteryFloor != nil {
			if *in.BatteryFloor < 0 || *in.BatteryFloor > 95 {
				return domain.Invalid("the battery level must be between 0 and 95")
			}
			set.BatteryFloor = *in.BatteryFloor
		}
		if set.Mode == KeepAwakeHours && set.From == set.To {
			return domain.Invalid("the hours must start and end at different times")
		}
		return saveKeepAwake(ctx, q, c, actor, before, stored, set)
	})
	return s.keepAwakeAnswer(ctx, actor, set, in.DryRun, err)
}

// saveKeepAwake stores the setting and records the change, so it can be
// undone.
func saveKeepAwake(ctx context.Context, q *db.Queries, c *change, actor Actor, before KeepAwakeSetting, stored bool, set KeepAwakeSetting) error {
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
}

// keepAwakeAnswer applies a saved change at once and answers with the
// setting and its new status.
func (s *Service) keepAwakeAnswer(ctx context.Context, actor Actor, set KeepAwakeSetting, dryRun bool, err error) (KeepAwakeInfo, error) {
	if err != nil {
		return KeepAwakeInfo{}, err
	}
	if s.keepAwake != nil && !dryRun {
		s.keepAwake.Apply(ctx) // the status the answer shows is the new one
	}
	info, err := s.GetKeepAwake(ctx, actor, struct{}{})
	info.KeepAwakeSetting = set // a dry run was rolled back
	return info, err
}

// KeepAwakeSession starts, extends or stops a session that keeps the
// computer awake now. Owners only.
func (s *Service) KeepAwakeSession(ctx context.Context, actor Actor, in KeepAwakeSessionInput) (KeepAwakeInfo, error) {
	var set KeepAwakeSetting
	now := s.Now()
	loc := s.awakeLocation(ctx) // before the transaction: SQLite has one writer
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if err := s.requireOwner(ctx, q, actor); err != nil {
			return err
		}
		before, stored := readKeepAwake(ctx, q)
		set = before
		if in.Minutes < 0 || in.Minutes > 7*24*60 {
			return domain.Invalid("minutes must be between 1 and 10080 (a week)")
		}
		switch in.Action {
		case SessionStart:
			set.Session = KeepAwakeSession{Active: true}
			switch {
			case in.Until != "":
				m, ok := clockMinutes(strings.TrimSpace(in.Until))
				if !ok {
					return domain.Invalid("times must look like 08:00")
				}
				set.Session.Until = nextClock(now.In(loc), m).UTC().Format(time.RFC3339)
			case in.Minutes > 0:
				set.Session.Until = now.Add(time.Duration(in.Minutes) * time.Minute).UTC().Format(time.RFC3339)
			}
		case SessionExtend:
			if in.Minutes == 0 {
				return domain.Invalid("say how many minutes to add")
			}
			end, ok := sessionEnd(before, now)
			switch {
			case ok && end.IsZero():
				return nil // until stopped: nothing to extend
			case !ok:
				end = now
			}
			set.Session = KeepAwakeSession{Active: true, Until: end.Add(time.Duration(in.Minutes) * time.Minute).UTC().Format(time.RFC3339)}
		case SessionStop:
			set.Session = KeepAwakeSession{}
		default:
			return domain.Invalid("unknown action %q", in.Action)
		}
		return saveKeepAwake(ctx, q, c, actor, before, stored, set)
	})
	return s.keepAwakeAnswer(ctx, actor, set, in.DryRun, err)
}

// SetupLidClosed asks this computer for the permission to keep it awake
// with its lid closed: on a Mac, the administrator's password, in a dialog
// on its screen, to allow `pmset disablesleep` (ADR 0032). Owners only.
func (s *Service) SetupLidClosed(ctx context.Context, actor Actor, in SetupLidClosedInput) (KeepAwakeInfo, error) {
	if err := s.requireOwner(ctx, s.st.Q, actor); err != nil {
		return KeepAwakeInfo{}, err
	}
	if !actor.CanWrite() {
		return KeepAwakeInfo{}, domain.Fail(ErrForbidden, "%s has read-only access", actor.Label)
	}
	k := s.keepAwake
	if k == nil || k.Lid == nil {
		return KeepAwakeInfo{}, domain.Invalid("this process does not keep the computer awake: ask the Exhale server")
	}
	if !in.DryRun {
		// No transaction is open while the dialog waits for the person.
		if err := k.SetupLid(ctx); err != nil {
			return KeepAwakeInfo{}, domain.Invalid("the computer did not allow it: %s", err.Error())
		}
		k.Apply(ctx)
	}
	return s.GetKeepAwake(ctx, actor, struct{}{})
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
	// Lid keeps the computer awake with its lid closed; nil: not here.
	Lid awake.Lid
	// Power reads the power source; nil: unknown (never paused).
	Power func() awake.Power
	// MaxSleep bounds every wait, so a change made by another process
	// (exhale mcp) or a clock jump is noticed.
	MaxSleep time.Duration
	// RetryAfter is how long a refused hold waits before it is tried again
	// with the same setting.
	RetryAfter time.Duration

	mu       sync.Mutex
	setupMu  sync.Mutex
	lidHeld  bool
	lidAt    time.Time // when holding the lid last failed
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
// sleep again. It first undoes a lid hold a crash left behind and resumes a
// session the last stop paused; when it stops, it pauses the session.
func (k *KeepAwake) Serve(ctx context.Context) error {
	events, stop := k.svc.Bus().Subscribe(16)
	defer stop()
	if k.Lid != nil {
		if err := k.Lid.Recover(); err != nil {
			k.log.Warn("could not let a closed laptop sleep again after the last stop", "err", err)
		} else {
			k.log.Debug("lid recovery done")
		}
	}
	k.resumeSession(ctx)
	defer func() {
		k.Release()
		k.pauseSession()
	}()
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

// pauseSession saves the time a timed session has left: when the server
// stops (a restart of the computer, an update), the session resumes with
// it rather than running out while nothing kept the computer awake.
func (k *KeepAwake) pauseSession() {
	ctx := context.Background()
	set, ok := readKeepAwake(ctx, k.svc.st.Q)
	end, active := sessionEnd(set, k.svc.Now())
	if !ok || !active || end.IsZero() || set.Session.Left > 0 {
		return
	}
	set.Session.Left = int(end.Sub(k.svc.Now()).Seconds())
	k.putSetting(ctx, set)
}

// resumeSession gives back a paused session its time.
func (k *KeepAwake) resumeSession(ctx context.Context) {
	set, ok := readKeepAwake(ctx, k.svc.st.Q)
	if !ok || !set.Session.Active || set.Session.Left <= 0 {
		return
	}
	set.Session.Until = k.svc.Now().Add(time.Duration(set.Session.Left) * time.Second).UTC().Format(time.RFC3339)
	set.Session.Left = 0
	k.putSetting(ctx, set)
	k.log.Info("resuming the keep-awake session", "until", set.Session.Until)
}

// putSetting stores a change the server makes by itself: not a change of
// the owner's, so it is not in the audit log.
func (k *KeepAwake) putSetting(ctx context.Context, set KeepAwakeSetting) {
	b, err := json.Marshal(set)
	if err == nil {
		err = k.svc.st.Q.PutSetting(ctx, db.PutSettingParams{Key: keepAwakeKey, Value: string(b)})
	}
	if err != nil {
		k.log.Warn("could not save the keep-awake session", "err", err)
	}
}

// Release lets the machine sleep again, lid included.
func (k *KeepAwake) Release() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.release()
}

func (k *KeepAwake) release() {
	k.releaseLid()
	if k.held {
		k.keeper.Release()
		k.held = false
		k.log.Info("this computer may sleep again")
	}
}

func (k *KeepAwake) releaseLid() {
	if !k.lidHeld {
		return
	}
	if err := k.Lid.Release(); err != nil {
		// Still held: the next Apply, a minute from now, tries again, and
		// the marker makes the next start try too.
		k.log.Warn("could not let a closed laptop sleep again", "err", err)
		return
	}
	k.lidHeld = false
	k.log.Info("a closed lid sends this computer to sleep again")
}

// SetupLid asks the computer for the permission to hold the lid.
func (k *KeepAwake) SetupLid(ctx context.Context) error {
	k.setupMu.Lock() // one dialog at a time
	defer k.setupMu.Unlock()
	return k.Lid.Setup(ctx)
}

// Apply holds or releases the machine as the setting asks now, and returns
// when that next changes by itself (zero: not by itself).
func (k *KeepAwake) Apply(ctx context.Context) time.Time {
	set, _ := readKeepAwake(ctx, k.svc.st.Q)
	now := k.svc.Now()
	on, next := wanted(set, now.In(k.svc.awakeLocation(ctx)))
	// until is when keeping awake stops by itself, as people read it: the
	// later of the session's end and the end of the hours, never with
	// "always" or a session until stopped.
	var until time.Time
	if on && set.Mode == KeepAwakeHours {
		until = next
	}
	session := false
	if end, ok := sessionEnd(set, now); ok {
		session = true
		switch {
		case end.IsZero():
			until = time.Time{}
		case !on || end.After(until):
			until = end
		}
		if !end.IsZero() && (next.IsZero() || end.Before(next)) {
			next = end
		}
		on = true
	}
	if set.Mode == KeepAwakeAlways {
		until = time.Time{}
	}
	var pw awake.Power
	if k.Power != nil {
		pw = k.Power()
	}
	k.mu.Lock()
	prev := k.status
	st := k.apply(set, on, pw, now)
	if st.State == AwakeActive {
		st.Session = session
		if !until.IsZero() {
			st.Until = until.UTC().Format(time.RFC3339)
		}
	}
	if pw.Known && pw.HasBattery {
		st.Power = KeepAwakePower{HasBattery: true, OnBattery: pw.OnBattery, Percent: pw.Percent}
	}
	k.status = st
	k.mu.Unlock()
	if quiet(st) != quiet(prev) {
		if st.State == AwakeFailed || st.State == AwakeUnavailable {
			k.log.Warn("cannot keep this computer awake", "state", st.State, "reason", st.Reason, "err", st.Error)
		}
		if st.Lid == "failed" {
			k.log.Warn("cannot keep this computer awake with its lid closed", "err", st.LidError)
		}
		if h, err := k.svc.st.Q.FirstHousehold(ctx); err == nil {
			k.svc.Bus().Publish(bus.Event{Type: bus.KeepAwakeUpdated, HouseholdID: h.ID, EntityID: keepAwakeKey})
		}
	}
	return next
}

// quiet is the status without the battery's charge, which changes every
// few minutes and is not worth telling every page about.
func quiet(st KeepAwakeStatus) KeepAwakeStatus {
	st.Power.Percent = 0
	return st
}

func (k *KeepAwake) apply(set KeepAwakeSetting, on bool, pw awake.Power, now time.Time) KeepAwakeStatus {
	if err := k.keeper.Available(); err != nil {
		k.release()
		st := KeepAwakeStatus{State: AwakeUnavailable, Reason: "other", Error: err.Error(), Lid: "off"}
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
			return KeepAwakeStatus{State: AwakeOff, Lid: "off"}
		}
		return KeepAwakeStatus{State: AwakeWaiting, Lid: "off"}
	}
	// A laptop on battery: not plugged in, or too low to go on.
	if pw.Known && pw.HasBattery && pw.OnBattery {
		reason := ""
		switch {
		case set.OnlyOnPower:
			reason = "power"
		case set.BatteryFloor > 0 && pw.Percent <= set.BatteryFloor:
			reason = "battery"
		}
		if reason != "" {
			if k.held {
				k.log.Info("pausing: this computer runs on battery", "reason", reason, "percent", pw.Percent)
			}
			k.release()
			k.failedAt = time.Time{}
			return KeepAwakeStatus{State: AwakePaused, Reason: reason, Lid: "off"}
		}
	}
	st := k.hold(set, now)
	if st.State != AwakeActive {
		k.releaseLid()
		st.Lid = "off"
		return st
	}
	st.Lid, st.LidError = k.applyLid(set.LidClosed, now)
	return st
}

// hold keeps the system awake, the display too when asked.
func (k *KeepAwake) hold(set KeepAwakeSetting, now time.Time) KeepAwakeStatus {
	if k.held && k.display == set.KeepDisplayOn {
		return KeepAwakeStatus{State: AwakeActive}
	}
	if !k.failedAt.IsZero() && k.failedOn == set && now.Sub(k.failedAt) < k.RetryAfter && k.status.State == AwakeFailed {
		return KeepAwakeStatus{State: AwakeFailed, Error: k.status.Error}
	}
	if k.held {
		k.keeper.Release()
		k.held = false
	}
	if err := k.keeper.Hold(set.KeepDisplayOn); err != nil {
		k.failedAt, k.failedOn = now, set
		return KeepAwakeStatus{State: AwakeFailed, Error: err.Error()}
	}
	k.failedAt = time.Time{}
	k.held, k.display = true, set.KeepDisplayOn
	k.log.Info("keeping this computer awake", "display", set.KeepDisplayOn)
	return KeepAwakeStatus{State: AwakeActive}
}

// applyLid holds or releases the lid while the system is held.
func (k *KeepAwake) applyLid(want bool, now time.Time) (state, errText string) {
	if !want || k.Lid == nil {
		k.releaseLid()
		if want {
			return "unavailable", awake.ErrUnsupported.Error()
		}
		return "off", ""
	}
	if k.lidHeld {
		return "active", ""
	}
	if err := k.Lid.Available(); err != nil {
		switch {
		case errors.Is(err, awake.ErrNeedsSetup):
			return "setup", ""
		case errors.Is(err, awake.ErrContainer), errors.Is(err, awake.ErrUnsupported):
			return "unavailable", err.Error()
		}
		return "failed", err.Error()
	}
	if !k.lidAt.IsZero() && now.Sub(k.lidAt) < k.RetryAfter && k.status.Lid == "failed" {
		return "failed", k.status.LidError
	}
	if err := k.Lid.Hold(); err != nil {
		k.lidAt = now
		return "failed", err.Error()
	}
	k.lidAt = time.Time{}
	k.lidHeld = true
	k.log.Info("keeping this computer awake with its lid closed")
	return "active", ""
}
