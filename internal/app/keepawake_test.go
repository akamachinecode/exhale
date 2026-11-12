package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/awake"
)

// fakeKeeper records what the controller asks of the operating system.
type fakeKeeper struct {
	avail   error
	holdErr error
	lost    error
	holding bool
	display bool
	holds   int
}

func (f *fakeKeeper) Available() error { return f.avail }
func (f *fakeKeeper) Hold(display bool) error {
	f.holds++
	if f.holdErr != nil {
		return f.holdErr
	}
	f.holding, f.display = true, display
	return nil
}
func (f *fakeKeeper) Release()    { f.holding, f.lost = false, nil }
func (f *fakeKeeper) Lost() error { return f.lost }

func ptr[T any](v T) *T { return &v }

func awakeFixture(t *testing.T) (*fixture, *fakeKeeper, *KeepAwake) {
	t.Helper()
	f := newFixture(t) // household in Europe/Madrid
	k := &fakeKeeper{}
	return f, k, NewKeepAwake(f.svc, k, nil)
}

// at sets the clock to a wall time in Madrid.
func (f *fixture) at(t *testing.T, s string) {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Madrid")
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	f.now = tm
}

func (f *fixture) setAwake(t *testing.T, in SetKeepAwakeInput) KeepAwakeInfo {
	t.Helper()
	info, err := f.svc.SetKeepAwake(context.Background(), f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestKeepAwakeOffByDefault(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ka.Apply(context.Background())
	info, _ := f.svc.GetKeepAwake(context.Background(), f.owner, struct{}{})
	if info.Mode != KeepAwakeOff || info.KeepDisplayOn || info.From != "08:00" || info.To != "23:00" || info.Timezone != "Europe/Madrid" {
		t.Fatalf("default = %+v", info)
	}
	if info.Status.State != AwakeOff || k.holding || k.holds != 0 {
		t.Fatalf("status %+v, holding %v", info.Status, k.holding)
	}
}

func TestKeepAwakeAlwaysAndDisplay(t *testing.T) {
	f, k, _ := awakeFixture(t)
	info := f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways)})
	if info.Status.State != AwakeActive || !k.holding || k.display {
		t.Fatalf("always: %+v, holding %v display %v", info.Status, k.holding, k.display)
	}
	// Keeping the display on too takes a new hold.
	f.setAwake(t, SetKeepAwakeInput{KeepDisplayOn: ptr(true)})
	if !k.holding || !k.display || k.holds != 2 {
		t.Fatalf("display: holding %v display %v holds %d", k.holding, k.display, k.holds)
	}
	// Applying again changes nothing.
	f.svc.keepAwake.Apply(context.Background())
	if k.holds != 2 {
		t.Fatalf("re-applied the same hold: %d holds", k.holds)
	}
	info = f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeOff)})
	if info.Status.State != AwakeOff || k.holding {
		t.Fatalf("off: %+v, holding %v", info.Status, k.holding)
	}
}

func TestKeepAwakeHours(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ctx := context.Background()
	f.at(t, "2026-11-05 07:30")
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeHours), From: ptr("8:00"), To: ptr("23:00")})
	if ka.Status().State != AwakeWaiting || k.holding {
		t.Fatalf("07:30: %+v", ka.Status())
	}
	next := ka.Apply(ctx)
	if want := "2026-11-05 08:00 CET"; next.Format("2006-01-02 15:04 MST") != want {
		t.Fatalf("next edge = %v, want %s", next, want)
	}
	f.at(t, "2026-11-05 08:00")
	next = ka.Apply(ctx)
	if ka.Status().State != AwakeActive || !k.holding || next.Format("15:04") != "23:00" {
		t.Fatalf("08:00: %+v, next %v", ka.Status(), next)
	}
	f.at(t, "2026-11-05 23:00")
	next = ka.Apply(ctx)
	if ka.Status().State != AwakeWaiting || k.holding || next.Format("2006-01-02 15:04") != "2026-11-06 08:00" {
		t.Fatalf("23:00: %+v, next %v", ka.Status(), next)
	}
}

func TestKeepAwakeHoursAcrossMidnight(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ctx := context.Background()
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeHours), From: ptr("22:00"), To: ptr("06:30")})
	for _, c := range []struct {
		at   string
		on   bool
		next string
	}{
		{"2026-11-05 21:59", false, "2026-11-05 22:00"},
		{"2026-11-05 22:00", true, "2026-11-06 06:30"},
		{"2026-11-06 00:00", true, "2026-11-06 06:30"},
		{"2026-11-06 06:29", true, "2026-11-06 06:30"},
		{"2026-11-06 06:30", false, "2026-11-06 22:00"},
		{"2026-11-06 12:00", false, "2026-11-06 22:00"},
	} {
		f.at(t, c.at)
		next := ka.Apply(ctx)
		if k.holding != c.on || next.Format("2006-01-02 15:04") != c.next {
			t.Errorf("%s: holding %v (want %v), next %v (want %s)", c.at, k.holding, c.on, next, c.next)
		}
	}
}

func TestKeepAwakeHoursFollowTheHouseholdTimezone(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ctx := context.Background()
	f.now = time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC) // 09:30 in Madrid
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeHours), From: ptr("09:00"), To: ptr("10:00")})
	if !k.holding {
		t.Fatal("09:30 in Madrid is inside the hours")
	}
	if _, err := f.svc.UpdateHousehold(ctx, f.owner, UpdateHouseholdInput{Timezone: ptr("UTC")}); err != nil {
		t.Fatal(err)
	}
	ka.Apply(ctx)
	if k.holding {
		t.Fatal("08:30 in UTC is outside the hours")
	}
}

func TestKeepAwakeUnavailableAndFailures(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ctx := context.Background()
	k.avail = awake.ErrContainer
	info := f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways)})
	if info.Status != (KeepAwakeStatus{State: AwakeUnavailable, Reason: "container", Lid: "off"}) || k.holds != 0 {
		t.Fatalf("container: %+v", info.Status)
	}
	k.avail = awake.ErrUnsupported
	ka.Apply(ctx)
	if ka.Status().Reason != "unsupported" {
		t.Fatalf("unsupported: %+v", ka.Status())
	}
	k.avail = errors.New("systemd-inhibit was not found")
	ka.Apply(ctx)
	if st := ka.Status(); st.Reason != "other" || st.Error != "systemd-inhibit was not found" {
		t.Fatalf("other: %+v", st)
	}

	// The system refuses: the error shows, and is not retried every minute.
	k.avail, k.holdErr = nil, errors.New("Access denied")
	ka.Apply(ctx)
	if st := ka.Status(); st.State != AwakeFailed || st.Error != "Access denied" || k.holds != 1 {
		t.Fatalf("refused: %+v (%d holds)", st, k.holds)
	}
	f.now = f.now.Add(time.Minute)
	ka.Apply(ctx)
	if k.holds != 1 || ka.Status().State != AwakeFailed {
		t.Fatalf("retried too soon: %d holds", k.holds)
	}
	k.holdErr = nil
	f.now = f.now.Add(ka.RetryAfter)
	ka.Apply(ctx)
	if ka.Status().State != AwakeActive || !k.holding {
		t.Fatalf("retry: %+v", ka.Status())
	}

	// The helper dies: it is noticed and held again.
	k.holding, k.lost = false, errors.New("caffeinate: killed")
	ka.Apply(ctx)
	if ka.Status().State != AwakeActive || !k.holding || k.holds != 3 {
		t.Fatalf("lost hold: %+v, %d holds", ka.Status(), k.holds)
	}
}

func TestKeepAwakeValidationOwnersAndUndo(t *testing.T) {
	f, k, _ := awakeFixture(t)
	ctx := context.Background()
	for _, in := range []SetKeepAwakeInput{
		{Mode: ptr("sometimes")},
		{From: ptr("25:00")},
		{Mode: ptr(KeepAwakeHours), From: ptr("09:00"), To: ptr("09:00")},
	} {
		if _, err := f.svc.SetKeepAwake(ctx, f.owner, in); err == nil {
			t.Errorf("%+v was accepted", in)
		}
	}
	// A dry run previews without holding.
	info, err := f.svc.SetKeepAwake(ctx, f.owner, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways), DryRun: true})
	if err != nil || info.Mode != KeepAwakeAlways || k.holding {
		t.Fatalf("dry run: %+v %v, holding %v", info, err, k.holding)
	}
	if got, _ := f.svc.GetKeepAwake(ctx, f.owner, struct{}{}); got.Mode != KeepAwakeOff {
		t.Fatalf("dry run saved: %+v", got)
	}

	// Members who are not owners cannot change it.
	member := f.owner
	member.MemberID = "mem_nobody"
	if _, err := f.svc.SetKeepAwake(ctx, member, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner: %v", err)
	}

	// Every change is recorded and can be undone.
	ctx2, refs := CollectAudits(ctx)
	if _, err := f.svc.SetKeepAwake(ctx2, f.owner, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways)}); err != nil {
		t.Fatal(err)
	}
	ctx3, refs2 := CollectAudits(ctx)
	f.setAwake(t, SetKeepAwakeInput{KeepDisplayOn: ptr(true)})
	if _, err := f.svc.SetKeepAwake(ctx3, f.owner, SetKeepAwakeInput{Mode: ptr(KeepAwakeHours), From: ptr("09:00")}); err != nil {
		t.Fatal(err)
	}
	if len(*refs) != 1 || len(*refs2) != 1 || (*refs)[0].Action != "keep_awake.update" {
		t.Fatalf("audits: %+v %+v", *refs, *refs2)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: (*refs2)[0].ID}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetKeepAwake(ctx, f.owner, struct{}{})
	if got.Mode != KeepAwakeAlways || !got.KeepDisplayOn || got.From != "08:00" {
		t.Fatalf("after undo: %+v", got)
	}
	// Undoing the first change goes back to no setting at all.
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: (*refs)[0].ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetKeepAwake(ctx, f.owner, struct{}{}); got.KeepAwakeSetting != defaultKeepAwake() {
		t.Fatalf("after undoing the first change: %+v", got)
	}
}

func TestKeepAwakeServeAppliesChangesAndReleases(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ka.MaxSleep = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ka.Serve(ctx); close(done) }()
	// A change made without the controller (as exhale mcp would) is picked
	// up from its event.
	f.svc.keepAwake = nil
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways)})
	deadline := time.Now().Add(2 * time.Second)
	for ka.Status().State != AwakeActive {
		if time.Now().After(deadline) {
			t.Fatalf("the change did not apply: %+v", ka.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if k.holding {
		t.Fatal("still holding after the server stopped")
	}
}

// fakeLid records what the controller asks of the lid switch.
type fakeLid struct {
	avail     error
	holding   bool
	holds     int
	releases  int
	recovered int
	setups    int
}

func (l *fakeLid) Available() error { return l.avail }
func (l *fakeLid) Setup(context.Context) error {
	l.setups++
	l.avail = nil
	return nil
}
func (l *fakeLid) Hold() error    { l.holds++; l.holding = true; return nil }
func (l *fakeLid) Release() error { l.releases++; l.holding = false; return nil }
func (l *fakeLid) Recover() error { l.recovered++; return nil }

func (f *fixture) session(t *testing.T, in KeepAwakeSessionInput) KeepAwakeInfo {
	t.Helper()
	info, err := f.svc.KeepAwakeSession(context.Background(), f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestKeepAwakeSessions(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ctx := context.Background()
	f.at(t, "2026-11-05 10:00")

	// For an hour, on top of a rule that is off.
	info := f.session(t, KeepAwakeSessionInput{Action: SessionStart, Minutes: 60})
	if info.Status.State != AwakeActive || !info.Status.Session || !k.holding || info.Status.Until != "2026-11-05T10:00:00Z" {
		t.Fatalf("for an hour: %+v", info.Status)
	}
	// Extended by 30 minutes, from its end.
	info = f.session(t, KeepAwakeSessionInput{Action: SessionExtend, Minutes: 30})
	if info.Status.Until != "2026-11-05T10:30:00Z" {
		t.Fatalf("extended: %+v", info.Status)
	}
	// It ends by itself.
	f.at(t, "2026-11-05 11:29")
	if next := ka.Apply(ctx); next.UTC().Format("15:04") != "10:30" || !k.holding {
		t.Fatalf("11:29: next %v, holding %v", next, k.holding)
	}
	f.at(t, "2026-11-05 11:30")
	ka.Apply(ctx)
	if st := ka.Status(); st.State != AwakeOff || k.holding {
		t.Fatalf("11:30: %+v", st)
	}

	// Until a time of day: the next time it comes, in Madrid.
	info = f.session(t, KeepAwakeSessionInput{Action: SessionStart, Until: "09:00"})
	if info.Session.Until != "2026-11-06T08:00:00Z" {
		t.Fatalf("until 09:00: %+v", info.Session)
	}
	// Until stopped, then stopped: the rule applies again.
	info = f.session(t, KeepAwakeSessionInput{Action: SessionStart})
	if !info.Session.Active || info.Session.Until != "" || info.Status.Until != "" || !k.holding {
		t.Fatalf("until stopped: %+v", info)
	}
	info = f.session(t, KeepAwakeSessionInput{Action: SessionStop})
	if info.Session.Active || k.holding || info.Status.State != AwakeOff {
		t.Fatalf("stopped: %+v", info)
	}
	// A session is a change like any other: an unknown action is refused.
	if _, err := f.svc.KeepAwakeSession(ctx, f.owner, KeepAwakeSessionInput{Action: "nap"}); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestKeepAwakeSessionOverHours(t *testing.T) {
	f, _, _ := awakeFixture(t)
	f.at(t, "2026-11-05 22:30")
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeHours), From: ptr("08:00"), To: ptr("23:00")})
	// A session that ends after the hours: until its end.
	info := f.session(t, KeepAwakeSessionInput{Action: SessionStart, Minutes: 120})
	if info.Status.Until != "2026-11-05T23:30:00Z" {
		t.Fatalf("session after the hours: %+v", info.Status)
	}
	// One that ends before them: until the end of the hours.
	info = f.session(t, KeepAwakeSessionInput{Action: SessionStart, Minutes: 10})
	if info.Status.Until != "2026-11-05T22:00:00Z" {
		t.Fatalf("session inside the hours: %+v", info.Status)
	}
}

func TestKeepAwakeSessionSurvivesARestart(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ka.MaxSleep = time.Hour
	// Serve reads the clock on its own goroutine: a clock safe to move.
	var mu sync.Mutex
	f.at(t, "2026-11-05 10:00")
	clock := f.now
	f.svc.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	move := func(at string) {
		f.at(t, at)
		mu.Lock()
		clock = f.now
		mu.Unlock()
	}
	f.session(t, KeepAwakeSessionInput{Action: SessionStart, Minutes: 60})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ka.Serve(ctx); close(done) }()
	waitStatus(t, ka, func(st KeepAwakeStatus) bool { return st.State == AwakeActive })
	move("2026-11-05 10:20") // 40 minutes left when the computer restarts
	cancel()
	<-done
	if k.holding {
		t.Fatal("still holding after the server stopped")
	}
	set, _ := readKeepAwake(context.Background(), f.svc.st.Q)
	if set.Session.Left != 40*60 {
		t.Fatalf("left = %d", set.Session.Left)
	}
	// Back two hours later: it resumes with its 40 minutes.
	move("2026-11-05 12:20")
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { _ = ka.Serve(ctx); close(done) }()
	// 12:20 in Madrid is 11:20 UTC; 40 minutes later, 12:00 UTC.
	waitStatus(t, ka, func(st KeepAwakeStatus) bool { return st.Until == "2026-11-05T12:00:00Z" })
	cancel()
	<-done
}

// waitStatus waits for the controller, running in Serve, to reach a status.
func waitStatus(t *testing.T, ka *KeepAwake, ok func(KeepAwakeStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok(ka.Status()) {
		if time.Now().After(deadline) {
			t.Fatalf("status never came: %+v", ka.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKeepAwakeOnBattery(t *testing.T) {
	f, k, ka := awakeFixture(t)
	ctx := context.Background()
	pw := awake.Power{Known: true, HasBattery: true, OnBattery: true, Percent: 50}
	ka.Power = func() awake.Power { return pw }
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways)})
	if st := ka.Status(); st.State != AwakeActive || st.Power != (KeepAwakePower{HasBattery: true, OnBattery: true, Percent: 50}) {
		t.Fatalf("50%% on battery: %+v", st)
	}
	// At the floor (20% by default), it lets the laptop sleep.
	pw.Percent = 20
	ka.Apply(ctx)
	if st := ka.Status(); st.State != AwakePaused || st.Reason != "battery" || k.holding {
		t.Fatalf("20%%: %+v", st)
	}
	// Plugged in, it goes on.
	pw.OnBattery = false
	ka.Apply(ctx)
	if ka.Status().State != AwakeActive || !k.holding {
		t.Fatalf("plugged in: %+v", ka.Status())
	}
	// Only while plugged in: on battery it waits, whatever the charge.
	f.setAwake(t, SetKeepAwakeInput{OnlyOnPower: ptr(true)})
	pw.OnBattery, pw.Percent = true, 90
	ka.Apply(ctx)
	if st := ka.Status(); st.State != AwakePaused || st.Reason != "power" || k.holding {
		t.Fatalf("only on power: %+v", st)
	}
	// A desktop has no battery: never paused.
	pw = awake.Power{Known: true}
	ka.Apply(ctx)
	if ka.Status().State != AwakeActive {
		t.Fatalf("desktop: %+v", ka.Status())
	}
	if _, err := f.svc.SetKeepAwake(ctx, f.owner, SetKeepAwakeInput{BatteryFloor: ptr(99)}); err == nil {
		t.Fatal("a floor of 99% was accepted")
	}
}

func TestKeepAwakeLidClosed(t *testing.T) {
	f, _, ka := awakeFixture(t)
	ctx := context.Background()
	lid := &fakeLid{avail: awake.ErrNeedsSetup}
	ka.Lid = lid
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways), LidClosed: ptr(true)})
	if st := ka.Status(); st.Lid != "setup" || lid.holding {
		t.Fatalf("before setup: %+v", st)
	}
	info, err := f.svc.SetupLidClosed(ctx, f.owner, SetupLidClosedInput{})
	if err != nil || info.Status.Lid != "active" || !lid.holding || lid.setups != 1 {
		t.Fatalf("after setup: %+v %v", info.Status, err)
	}
	// Paused on battery: the lid goes back to sleeping the laptop.
	ka.Power = func() awake.Power { return awake.Power{Known: true, HasBattery: true, OnBattery: true, Percent: 10} }
	ka.Apply(ctx)
	if lid.holding || lid.releases != 1 || ka.Status().Lid != "off" {
		t.Fatalf("on a low battery: %+v", ka.Status())
	}
	ka.Power = nil
	ka.Apply(ctx)
	if !lid.holding {
		t.Fatal("not held again on power")
	}
	// Turned off: released.
	f.setAwake(t, SetKeepAwakeInput{LidClosed: ptr(false)})
	if lid.holding || ka.Status().Lid != "off" {
		t.Fatalf("turned off: %+v", ka.Status())
	}
	// No lid switch on this system.
	ka.Lid = nil
	f.setAwake(t, SetKeepAwakeInput{LidClosed: ptr(true)})
	if ka.Status().Lid != "unavailable" {
		t.Fatalf("no lid: %+v", ka.Status())
	}
}

func TestKeepAwakeServeRecoversTheLid(t *testing.T) {
	_, _, ka := awakeFixture(t)
	lid := &fakeLid{}
	ka.Lid = lid
	ka.MaxSleep = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ka.Serve(ctx); close(done) }()
	cancel()
	<-done
	if lid.recovered != 1 {
		t.Fatalf("recovered %d times", lid.recovered)
	}
}

// failingLid refuses to let go until told.
type failingLid struct {
	fakeLid
	releaseErr error
}

func (l *failingLid) Release() error {
	if l.releaseErr != nil {
		return l.releaseErr
	}
	return l.fakeLid.Release()
}

func TestKeepAwakeRetriesReleasingTheLid(t *testing.T) {
	f, _, ka := awakeFixture(t)
	ctx := context.Background()
	lid := &failingLid{}
	ka.Lid = lid
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeAlways), LidClosed: ptr(true)})
	if !lid.holding {
		t.Fatal("not held")
	}
	lid.releaseErr = errors.New("sudo: a password is required")
	f.setAwake(t, SetKeepAwakeInput{Mode: ptr(KeepAwakeOff)})
	if !lid.holding {
		t.Fatal("released despite the error")
	}
	lid.releaseErr = nil
	ka.Apply(ctx)
	if lid.holding {
		t.Fatal("not retried")
	}
}
