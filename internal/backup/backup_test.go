package backup

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/store"
)

var quiet = slog.New(slog.DiscardHandler)

// openStore opens a real Exhale database in a temp dir with one marker row.
func openStore(t *testing.T, marker string) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.DB.Exec("CREATE TABLE marker (v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("INSERT INTO marker (v) VALUES (?)", marker); err != nil {
		t.Fatal(err)
	}
	return st, dir
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", fileURI(path, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v string
	if err := db.QueryRow("SELECT v FROM marker").Scan(&v); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return v
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

var t0 = time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

func TestSnapshotIsAConsistentNamedCopy(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t, "before")
	out := filepath.Join(t.TempDir(), "nested", "backups")

	// A time in another zone must still be named in UTC.
	madrid := time.FixedZone("CET", 3600)
	path, err := Snapshot(ctx, st.DB, out, KindManual, t0.In(madrid))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(out, "manual-20261110T093000Z.db"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	// Later writes must not reach the copy.
	if _, err := st.DB.Exec("UPDATE marker SET v = 'after'"); err != nil {
		t.Fatal(err)
	}
	if got := readMarker(t, path); got != "before" {
		t.Fatalf("copy has %q, want the value at backup time", got)
	}
	if err := Verify(ctx, path); err != nil {
		t.Fatal(err)
	}
	if got := names(t, out); len(got) != 1 {
		t.Fatalf("leftover files: %v", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("backup is readable by others: %v", fi.Mode())
	}
	// The same second again must not overwrite.
	if _, err := Snapshot(ctx, st.DB, out, KindManual, t0); err == nil {
		t.Fatal("second snapshot in the same second overwrote the first")
	}
	if _, err := Snapshot(ctx, st.DB, out, "../evil", t0); err == nil {
		t.Fatal("accepted a kind with a path in it")
	}
}

func TestVerifyRejectsWhatIsNotAnExhaleDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte(strings.Repeat("not a database ", 200)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, garbage); err == nil {
		t.Fatal("accepted a text file")
	}

	other := filepath.Join(dir, "other.db")
	db, err := sql.Open("sqlite", fileURI(other, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := Verify(ctx, other); err == nil || !strings.Contains(err.Error(), "not an Exhale database") {
		t.Fatalf("other SQLite database: %v", err)
	}

	if err := Verify(ctx, filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("accepted a missing file")
	}
	if err := Verify(ctx, dir); err == nil {
		t.Fatal("accepted a directory")
	}
}

func TestPruneKeepsNewestAutomaticOnly(t *testing.T) {
	dir := t.TempDir()
	touch := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{
		"auto-20261101T000000Z.db", "auto-20261102T000000Z.db", "auto-20261103T000000Z.db",
		"auto-20261104T000000Z.db", "auto-20261105T000000Z.db",
		// Never ours to delete:
		"manual-20260101T000000Z.db", "pre-migrate-v3-20260101T000000Z.db",
		"auto-20261101T000000Z.db.tmp", "auto-notes.txt", "auto-2026.db", "exhale.db",
	} {
		touch(n)
	}
	if err := os.Mkdir(filepath.Join(dir, "auto-20250101T000000Z.db"), 0o700); err != nil {
		t.Fatal(err)
	}

	files, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 || filepath.Base(files[0].Path) != "auto-20261105T000000Z.db" {
		t.Fatalf("List = %v", files)
	}
	if removed, err := Prune(dir, 0); err != nil || len(removed) != 0 {
		t.Fatalf("keep 0 must keep everything: %v %v", removed, err)
	}
	removed, err := Prune(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed %v", removed)
	}
	want := []string{
		"auto-20250101T000000Z.db", "auto-20261101T000000Z.db.tmp", "auto-20261104T000000Z.db",
		"auto-20261105T000000Z.db", "auto-2026.db", "auto-notes.txt", "exhale.db",
		"manual-20260101T000000Z.db", "pre-migrate-v3-20260101T000000Z.db",
	}
	sort.Strings(want) // os.ReadDir order
	got := names(t, dir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("left on disk:\n got %v\nwant %v", got, want)
	}
	if files, _ := List(filepath.Join(dir, "does-not-exist")); len(files) != 0 {
		t.Fatalf("missing dir: %v", files)
	}
}

// fakeClock is the service's clock: sleeping advances it instantly.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
	stop   func(c *fakeClock) bool
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil || (c.stop != nil && c.stop(c)) {
		return false
	}
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return true
}

func TestServiceBacksUpDailyAndPrunes(t *testing.T) {
	st, _ := openStore(t, "x")
	dir := filepath.Join(t.TempDir(), "backups")
	clock := &fakeClock{now: t0}
	// Run for a little over five days of fake time.
	clock.stop = func(c *fakeClock) bool { return c.now.Sub(t0) > 5*24*time.Hour+30*time.Minute }
	svc := &Service{DB: st.DB, Dir: dir, Keep: 3, Log: quiet, Now: clock.Now, Sleep: clock.Sleep}

	if err := svc.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if clock.sleeps[0] != time.Minute {
		t.Fatalf("first backup should come a minute after start, waited %v", clock.sleeps[0])
	}
	for _, d := range clock.sleeps {
		if d > time.Hour || d < time.Minute {
			t.Fatalf("slept %v: checks must stay between a minute and an hour apart", d)
		}
	}
	// Six backups were made (start + 5 days); the newest three remain.
	want := []string{"auto-20261113T093100Z.db", "auto-20261114T093100Z.db", "auto-20261115T093100Z.db"}
	if got := names(t, dir); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("backups on disk:\n got %v\nwant %v", got, want)
	}
	for _, n := range want {
		if err := Verify(context.Background(), filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceDoesNotRepeatAfterRestart(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t, "x")
	dir := t.TempDir()
	clock := &fakeClock{now: t0}
	svc := &Service{DB: st.DB, Dir: dir, Keep: 14, Log: quiet, Now: clock.Now, Sleep: clock.Sleep}

	if wait := svc.Tick(ctx); wait != time.Hour {
		t.Fatalf("after a backup the next check is in %v, want 1h", wait)
	}
	// A restart 10 minutes later: today's backup exists, nothing new.
	clock.now = t0.Add(10 * time.Minute)
	restarted := &Service{DB: st.DB, Dir: dir, Keep: 14, Log: quiet, Now: clock.Now, Sleep: clock.Sleep}
	restarted.Tick(ctx)
	if got := names(t, dir); len(got) != 1 {
		t.Fatalf("restart made another backup: %v", got)
	}
	// Close to the 24 h mark it sleeps only until then.
	clock.now = t0.Add(23*time.Hour + 40*time.Minute)
	if wait := restarted.Tick(ctx); wait != 20*time.Minute {
		t.Fatalf("wait = %v, want 20m", wait)
	}
	// The machine was off for three days: one backup on return, not three.
	clock.now = t0.Add(72 * time.Hour)
	restarted.Tick(ctx)
	if got := names(t, dir); len(got) != 2 {
		t.Fatalf("after three days off: %v", got)
	}
}

func TestServiceSurvivesAFailingBackup(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t, "x")
	// The backup "directory" is a file: every backup fails.
	blocked := filepath.Join(t.TempDir(), "backups")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: t0}
	svc := &Service{DB: st.DB, Dir: blocked, Keep: 14, Log: quiet, Now: clock.Now, Sleep: clock.Sleep}
	if wait := svc.Tick(ctx); wait != time.Hour {
		t.Fatalf("after a failure the retry is in %v, want 1h", wait)
	}
	// The path is fixed: the next check succeeds.
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Hour)
	svc.Tick(ctx)
	if got := names(t, blocked); len(got) != 1 {
		t.Fatalf("no backup after the directory was fixed: %v", got)
	}
}

func TestServiceStopsWhenCancelled(t *testing.T) {
	st, _ := openStore(t, "x")
	ctx, cancel := context.WithCancel(context.Background())
	svc := &Service{DB: st.DB, Dir: t.TempDir(), Log: quiet, Delay: time.Hour} // real clock
	done := make(chan error, 1)
	go func() { done <- svc.Serve(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestRestoreIntoEmptyDataDir(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t, "precious")
	bak, err := Snapshot(ctx, st.DB, t.TempDir(), KindManual, t0)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "fresh")
	dst, replaced, err := Restore(ctx, bak, dataDir, false, t0)
	if err != nil {
		t.Fatal(err)
	}
	if dst != filepath.Join(dataDir, DBName) || replaced != "" {
		t.Fatalf("dst=%s replaced=%s", dst, replaced)
	}
	// The restored directory opens as a normal Exhale database.
	st2, err := store.Open(ctx, dataDir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var v string
	if err := st2.DB.QueryRow("SELECT v FROM marker").Scan(&v); err != nil || v != "precious" {
		t.Fatalf("restored data: %q %v", v, err)
	}
	// The backup itself is untouched.
	if got := readMarker(t, bak); got != "precious" {
		t.Fatalf("backup changed: %q", got)
	}
}

func TestRestoreRefusesToOverwrite(t *testing.T) {
	ctx := context.Background()
	old, _ := openStore(t, "old")
	bak, err := Snapshot(ctx, old.DB, t.TempDir(), KindManual, t0)
	if err != nil {
		t.Fatal(err)
	}
	cur, dataDir := openStore(t, "current")

	// Without --force: refused, nothing changes.
	if _, _, err := Restore(ctx, bak, dataDir, false, t0); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	// With --force but the database open elsewhere (a running server): refused.
	if _, _, err := Restore(ctx, bak, dataDir, true, t0); !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
	var v string
	if err := cur.DB.QueryRow("SELECT v FROM marker").Scan(&v); err != nil || v != "current" {
		t.Fatalf("the running database was touched: %q %v", v, err)
	}
	if got := names(t, dataDir); strings.Contains(strings.Join(got, ","), "replaced") || strings.Contains(strings.Join(got, ","), "restoring") {
		t.Fatalf("a refused restore left files behind: %v", got)
	}

	// Server stopped, --force: restored, and the old database is kept.
	cur.Close()
	dst, replaced, err := Restore(ctx, bak, dataDir, true, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMarker(t, dst); got != "old" {
		t.Fatalf("restored database has %q", got)
	}
	if want := filepath.Join(dataDir, DBName+".replaced-20261110T093000Z"); replaced != want {
		t.Fatalf("replaced = %s, want %s", replaced, want)
	}
	if got := readMarker(t, replaced); got != "current" {
		t.Fatalf("the replaced database lost its data: %q", got)
	}
	for _, n := range names(t, dataDir) {
		if n == DBName+"-wal" || n == DBName+"-shm" {
			t.Fatalf("a stale %s would be read with the restored database", n)
		}
	}
}

func TestRestoreRejectsBadSources(t *testing.T) {
	ctx := context.Background()
	st, dataDir := openStore(t, "x")
	st.Close()

	// The live database itself is not a backup of itself.
	if _, _, err := Restore(ctx, filepath.Join(dataDir, DBName), dataDir, true, t0); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("restoring the database onto itself: %v", err)
	}
	garbage := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(garbage, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	if _, _, err := Restore(ctx, garbage, fresh, false, t0); err == nil {
		t.Fatal("restored a file that is not a database")
	}
	if _, err := os.Stat(filepath.Join(fresh, DBName)); err == nil {
		t.Fatal("a rejected restore created a database")
	}
}
