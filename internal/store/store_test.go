package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndBacksUp(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Q.ListMembers(ctx, "none"); err != nil {
		t.Fatalf("schema not created: %v", err)
	}
	p, err := s.Backup(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() == 0 || filepath.Dir(p) != filepath.Join(dir, "backups") {
		t.Fatalf("backup %s: %v", p, err)
	}
	s.Close()
	// Reopening an up-to-date database must not migrate or back up again.
	s2, err := Open(ctx, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(entries))
	}
}
