package backup

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Defaults of the automatic backups.
const (
	DefaultKeep  = 14
	defaultEvery = 24 * time.Hour
	defaultDelay = time.Minute
	// maxSleep bounds every wait, so clock changes and a laptop waking up
	// are noticed within the hour (timers do not run while suspended).
	maxSleep = time.Hour
	minSleep = time.Minute
)

// Service makes one automatic backup a day while the server runs and keeps
// the newest Keep. It is a supervised service (platform.Service).
//
// It decides from the files on disk, not from memory: a backup is due when
// the newest automatic one is at least Every old. Restarting the server
// therefore never produces extra backups, and a day missed while the
// machine was off is made up soon after start.
type Service struct {
	DB   *sql.DB
	Dir  string
	Keep int // automatic backups to keep; 0 keeps all
	Log  *slog.Logger

	Every time.Duration // time between backups; default 24 h
	Delay time.Duration // wait after start before the first check; default 1 min

	// Now and Sleep are the clock; tests replace them.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) bool
}

func (s *Service) String() string { return "backups" }

// Serve runs until ctx is cancelled. A failed backup is logged and tried
// again within the hour; it never stops the server.
func (s *Service) Serve(ctx context.Context) error {
	delay := s.Delay
	if delay == 0 {
		delay = defaultDelay
	}
	wait := delay
	for s.sleep(ctx, wait) {
		wait = s.Tick(ctx)
	}
	return nil
}

// Tick makes a backup if one is due, prunes old ones, and returns how long
// to wait before the next check.
func (s *Service) Tick(ctx context.Context) time.Duration {
	every := s.Every
	if every <= 0 {
		every = defaultEvery
	}
	now := s.now()
	var latest time.Time
	files, err := List(s.Dir)
	if err != nil {
		s.Log.Error("automatic backup failed: cannot read the backup directory", "dir", s.Dir, "err", err)
		return maxSleep
	}
	if len(files) > 0 {
		latest = files[0].Time
	}
	if latest.IsZero() || now.Sub(latest) >= every {
		path, err := Snapshot(ctx, s.DB, s.Dir, KindAuto, now)
		if err != nil {
			if ctx.Err() == nil {
				s.Log.Error("automatic backup failed; trying again within the hour", "dir", s.Dir, "err", err)
			}
			return maxSleep
		}
		latest = now
		removed, err := Prune(s.Dir, s.Keep)
		if err != nil {
			s.Log.Warn("could not delete old backups", "dir", s.Dir, "err", err)
		}
		s.Log.Info("database backed up", "path", path, "keep", s.Keep, "removed", len(removed))
	}
	return min(max(latest.Add(every).Sub(now), minSleep), maxSleep)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) sleep(ctx context.Context, d time.Duration) bool {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
