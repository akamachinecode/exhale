// Package platform (piece ⑮) holds process-level concerns: data directory,
// logging and supervision of long-running services.
package platform

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/thejerf/suture/v4"
)

// DefaultDataDir returns the per-OS default data directory.
func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Exhale")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Exhale")
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "exhale")
	}
	return filepath.Join(home, ".local", "share", "exhale")
}

// secretKeys are redacted from every log line.
var secretKeys = []string{"token", "password", "secret", "authorization"}

// NewLogger returns a logger: coloured text on a terminal, JSON when
// format is "json". Secrets are redacted in one place.
func NewLogger(w io.Writer, level, format string) *slog.Logger {
	lvl := slog.LevelInfo
	_ = lvl.UnmarshalText([]byte(level))
	redact := func(_ []string, a slog.Attr) slog.Attr {
		k := strings.ToLower(a.Key)
		for _, s := range secretKeys {
			if strings.Contains(k, s) {
				return slog.String(a.Key, "[redacted]")
			}
		}
		return a
	}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl, ReplaceAttr: redact}))
	}
	f, isFile := w.(*os.File)
	noColor := !isFile || os.Getenv("NO_COLOR") != ""
	if isFile && !noColor {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			noColor = true
		}
	}
	return slog.New(tint.NewHandler(w, &tint.Options{Level: lvl, TimeFormat: time.TimeOnly, NoColor: noColor, ReplaceAttr: redact}))
}

// Service is a long-running piece supervised with restarts.
type Service interface {
	Serve(ctx context.Context) error
	String() string
}

// Run supervises services until ctx is cancelled. A failing service is
// restarted with backoff without stopping the others (ADR 0009).
func Run(ctx context.Context, log *slog.Logger, services ...Service) error {
	sup := suture.New("exhale", suture.Spec{
		EventHook: func(e suture.Event) { log.Warn("supervisor", "event", e.String()) },
		Timeout:   8 * time.Second, // Docker gives 10 s on stop
	})
	for _, s := range services {
		sup.Add(s)
	}
	err := sup.Serve(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Func adapts a function to a Service.
type Func struct {
	Name string
	Fn   func(ctx context.Context) error
}

func (f Func) Serve(ctx context.Context) error { return f.Fn(ctx) }
func (f Func) String() string                  { return f.Name }
