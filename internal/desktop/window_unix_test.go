//go:build !windows

package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeWindow writes an exhale-window next to an exhale that exits with code.
func fakeWindow(t *testing.T, code string) (exe string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "exhale-window"), []byte("#!/bin/sh\nexit "+code+"\n"), 0o755); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	return filepath.Join(dir, "exhale")
}

func TestWindowProgram(t *testing.T) {
	exe := fakeWindow(t, "0")
	if got := windowProgram(exe); got != filepath.Join(filepath.Dir(exe), "exhale-window") {
		t.Errorf("windowProgram = %q", got)
	}
	if got := windowProgram(filepath.Join(t.TempDir(), "exhale")); got != "" {
		t.Errorf("windowProgram without one = %q", got)
	}
	// Not executable: not a window program.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "exhale-window"), []byte("x"), 0o600)
	if got := windowProgram(filepath.Join(dir, "exhale")); got != "" {
		t.Errorf("windowProgram of a plain file = %q", got)
	}
}

func TestRunWindow(t *testing.T) {
	ctx := context.Background()
	if err := runWindow(ctx, windowProgram(fakeWindow(t, "0")), "http://127.0.0.1:8484", "", false); err != nil {
		t.Errorf("a window closed normally: %v", err)
	}
	// 127: the loader did not find its libraries, a server without a desktop.
	if err := runWindow(ctx, windowProgram(fakeWindow(t, "127")), "http://127.0.0.1:8484", "", false); !errors.Is(err, errNoWindow) {
		t.Errorf("missing libraries: %v, want errNoWindow", err)
	}
	if err := runWindow(ctx, windowProgram(fakeWindow(t, "3")), "http://127.0.0.1:8484", "", false); err == nil || errors.Is(err, errNoWindow) {
		t.Errorf("a window that failed: %v", err)
	}
}

func TestRunWindowPassesDataDirAndBackground(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "exhale-window"), []byte(script), 0o755); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	if err := runWindow(context.Background(), filepath.Join(dir, "exhale-window"), "http://127.0.0.1:8484", "/data dir", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out) //nolint:gosec // the test's own file
	if want := "--url http://127.0.0.1:8484 --data-dir /data dir --background\n"; string(got) != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestBackgroundWithoutWindowShowsNothing(t *testing.T) {
	// The libraries are missing: at login, no browser opens.
	a := App{Exe: fakeWindow(t, "127"), Background: true}
	if err := a.show(context.Background(), "http://127.0.0.1:1"); err != nil {
		t.Errorf("show = %v", err)
	}
	// And the background flag reaches its own window.
	var bg bool
	a.Window = func(_ string, background bool) error { bg = background; return nil }
	if err := a.show(context.Background(), "http://127.0.0.1:1"); err != nil || !bg {
		t.Errorf("show = %v, background %v", err, bg)
	}
}

func TestShowPrefersItsOwnWindow(t *testing.T) {
	var shown string
	a := App{Exe: fakeWindow(t, "3"), Window: func(url string, _ bool) error { shown = url; return nil }}
	if err := a.show(context.Background(), "http://127.0.0.1:8484"); err != nil || shown != "http://127.0.0.1:8484" {
		t.Errorf("show = %v, shown %q", err, shown)
	}
	// Without its own window, the program next to it runs (and fails here).
	a.Window = nil
	if err := a.show(context.Background(), "http://127.0.0.1:8484"); err == nil {
		t.Error("the window program next to it did not run")
	}
}
