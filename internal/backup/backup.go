// Package backup copies the SQLite database safely: a consistent snapshot
// with VACUUM INTO (copying the file of a live database can corrupt the
// copy), a rolling set of automatic snapshots while the server runs, and a
// restore that never overwrites data (ADR 0009, ADR 0020).
//
// Files are named <kind>-<UTC timestamp>.db. Only automatic backups
// (kind "auto") are ever deleted by this package.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, to check and read backup files
)

// Kinds of backup. The kind is the first part of the file name.
const (
	KindAuto   = "auto"   // made by the server, pruned to the newest N
	KindManual = "manual" // made by `exhale backup`, never deleted
)

// DBName is the database file inside the data directory.
const DBName = "exhale.db"

const stampLayout = "20060102T150405Z"

var (
	kindRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	autoRE = regexp.MustCompile(`^` + KindAuto + `-(\d{8}T\d{6}Z)\.db$`)
)

// Snapshot writes a consistent copy of the database to
// dir/<kind>-<UTC timestamp of now>.db and returns its path. The server can
// keep running. The file is complete or absent: it is written under a
// temporary name and renamed once it has been checked.
func Snapshot(ctx context.Context, db *sql.DB, dir, kind string, now time.Time) (string, error) {
	if !kindRE.MatchString(kind) {
		return "", fmt.Errorf("backup: invalid kind %q", kind)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.db", kind, now.UTC().Format(stampLayout)))
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("backup: %s already exists", path)
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("backup: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp) // best effort; the error below is the one that matters
		return "", fmt.Errorf("backup: write %s: %w", path, err)
	}
	if err := finish(ctx, tmp, path); err != nil {
		_ = os.Remove(tmp) // best effort; the error below is the one that matters
		return "", err
	}
	return path, nil
}

// finish makes a freshly written copy private, checks it, flushes it to
// disk and moves it to its final name.
func finish(ctx context.Context, tmp, path string) error {
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := Verify(ctx, tmp); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_RDWR, 0) //nolint:gosec // our own temporary file
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// Verify checks that path is a healthy Exhale database: SQLite reads every
// page (PRAGMA quick_check) and the schema carries Exhale's migration table.
// The file is opened read-only and is not modified.
func Verify(ctx context.Context, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("backup: %s is not a file", path)
	}
	db, err := sql.Open("sqlite", fileURI(path, "mode=ro&immutable=1"))
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", path, err)
	}
	defer db.Close()
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&res); err != nil {
		return fmt.Errorf("backup: %s is not a readable SQLite database: %w", path, err)
	}
	if res != "ok" {
		return fmt.Errorf("backup: %s is damaged: %s", path, res)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'").Scan(&n); err != nil {
		return fmt.Errorf("backup: %s is not a readable SQLite database: %w", path, err)
	}
	if n == 0 {
		return fmt.Errorf("backup: %s is not an Exhale database", path)
	}
	return nil
}

// fileURI builds a SQLite URI for a path on any OS, with the path escaped.
func fileURI(path, query string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows: file:///C:/…
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: query}).String()
}

// File is an automatic backup found on disk.
type File struct {
	Path string
	Time time.Time // from the file name, UTC
}

// List returns the automatic backups in dir, newest first. A directory that
// does not exist yet has none.
func List(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	var out []File
	for _, e := range entries {
		m := autoRE.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		t, err := time.Parse(stampLayout, m[1])
		if err != nil {
			continue
		}
		out = append(out, File{Path: filepath.Join(dir, e.Name()), Time: t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// Prune deletes the automatic backups in dir beyond the newest keep and
// returns the paths it removed. keep <= 0 keeps everything. Files of any
// other kind (manual, pre-migrate, or anything else in dir) are never touched.
func Prune(dir string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	files, err := List(dir)
	if err != nil || len(files) <= keep {
		return nil, err
	}
	var removed []string
	for _, f := range files[keep:] {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("backup: %w", err)
		}
		removed = append(removed, f.Path)
	}
	return removed, nil
}

// ErrExists is returned by Restore when the data directory already has a
// database and force is not set.
var ErrExists = errors.New("backup: the data directory already has a database")

// ErrInUse is returned by Restore when another process has the database open.
var ErrInUse = errors.New("backup: the database is in use")

// Restore copies the backup file src into dataDir as the database.
//
// It refuses when dataDir already has a database, unless force is set; and
// even then it refuses when another process (a running Exhale) has that
// database open. Nothing is deleted: the database it replaces is renamed
// next to the new one and its path is returned in replaced.
func Restore(ctx context.Context, src, dataDir string, force bool, now time.Time) (dst, replaced string, err error) {
	if err := Verify(ctx, src); err != nil {
		return "", "", err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return "", "", fmt.Errorf("backup: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", "", fmt.Errorf("backup: create %s: %w", dataDir, err)
	}
	dst = filepath.Join(dataDir, DBName)
	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", "", fmt.Errorf("backup: %w", err)
	}
	dstInfo, err := os.Stat(dst)
	switch {
	case err == nil:
		if os.SameFile(srcInfo, dstInfo) {
			return "", "", fmt.Errorf("backup: %s is the database itself, not a backup", src)
		}
		if !force {
			return "", "", fmt.Errorf("%w: %s", ErrExists, dst)
		}
		if err := release(ctx, dst); err != nil {
			return "", "", err
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", "", fmt.Errorf("backup: %w", err)
	}

	// Copy first, under a temporary name: if anything fails, the data
	// directory is exactly as it was.
	tmp := dst + ".restoring"
	if err := copyFile(src, tmp); err != nil {
		_ = os.Remove(tmp) // best effort; the error below is the one that matters
		return "", "", err
	}
	// Move the old database (and any write-ahead files, which belong to it
	// and must not be read with the new one) out of the way.
	suffix := ".replaced-" + now.UTC().Format(stampLayout)
	for _, name := range []string{DBName, DBName + "-wal", DBName + "-shm"} {
		old := filepath.Join(dataDir, name)
		if _, err := os.Lstat(old); err != nil {
			continue
		}
		if err := os.Rename(old, old+suffix); err != nil {
			_ = os.Remove(tmp) // best effort; the error below is the one that matters
			return "", "", fmt.Errorf("backup: set the current database aside: %w", err)
		}
		if name == DBName {
			replaced = old + suffix
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", replaced, fmt.Errorf("backup: %w", err)
	}
	return dst, replaced, nil
}

// release makes sure nobody else has the database at path open, and folds
// its write-ahead log into the file so the file alone is the whole database.
// Leaving WAL mode needs exclusive access, so SQLite itself tells us whether
// another process (a running server keeps a lock for as long as it is open)
// is there.
func release(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", fileURI(path, "_pragma=busy_timeout(500)"))
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var mode string
	err = db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode)
	if err == nil && !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("%w: stop Exhale first (%s)", ErrInUse, path)
	}
	if err != nil {
		if isBusy(err) {
			return fmt.Errorf("%w: stop Exhale first (%s)", ErrInUse, path)
		}
		return fmt.Errorf("backup: cannot check that %s is free (%w); stop Exhale, move the file away yourself and restore again", path, err)
	}
	return nil
}

func isBusy(err error) bool {
	s := err.Error()
	return strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "database is locked")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // the backup file the user asked to restore
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // inside the data directory
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("backup: copy to %s: %w", dst, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("backup: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}
