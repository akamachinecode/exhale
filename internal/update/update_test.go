package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.5.0", "0.4.0", true},
		{"0.4.1", "0.4.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.10.0", "0.9.0", true},
		{"v0.5.0", "0.4.0", true},
		{"0.4.0", "0.4.0", false},
		{"0.3.9", "0.4.0", false},
		{"0.5.0", "dev", false},
		{"dev", "0.4.0", false},
		{"0.5", "0.4.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestChecksumOf(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	sums := []byte(strings.Repeat("0", 64) + "  Exhale.dmg\n" + sum + "  exhale_0.5.0_linux_amd64.tar.gz\n")
	got, err := checksumOf(sums, "exhale_0.5.0_linux_amd64.tar.gz")
	if err != nil || got != sum {
		t.Fatalf("checksumOf = %q, %v", got, err)
	}
	if _, err := checksumOf(sums, "Exhale-Setup.exe"); err == nil {
		t.Fatal("a file that is not listed must fail")
	}
}

type fakeEnv struct {
	files    map[string]bool
	readOnly map[string]bool
	vars     map[string]string
}

func (f fakeEnv) exists(p string) bool   { return f.files[p] }
func (f fakeEnv) writable(d string) bool { return !f.readOnly[d] }
func (f fakeEnv) getenv(k string) string { return f.vars[k] }

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name     string
		goos     string
		exe      string
		desktop  bool
		env      fakeEnv
		method   Method
		writable bool
		command  string
	}{
		{name: "mac app", goos: "darwin", exe: "/Applications/Exhale.app/Contents/MacOS/exhale", desktop: true, method: MethodMacApp, writable: true},
		{name: "mac app, read-only", goos: "darwin", exe: "/Applications/Exhale.app/Contents/MacOS/exhale", desktop: true,
			env: fakeEnv{readOnly: map[string]bool{"/Applications": true}}, method: MethodMacApp},
		{name: "windows app", goos: "windows", exe: `C:\Users\a\AppData\Local\Programs\Exhale\Exhale.exe`, desktop: true, method: MethodWindows, writable: true},
		{name: "docker", goos: "linux", exe: "/usr/local/bin/exhale", env: fakeEnv{files: map[string]bool{"/.dockerenv": true}},
			method: MethodDocker, command: "docker compose pull && docker compose up -d"},
		{name: "rpm", goos: "linux", exe: "/usr/bin/exhale", method: MethodPackage,
			command: "sudo dnf install https://github.com/akamachinecode/exhale/releases/download/v0.5.0/exhale_linux_" + arch() + ".rpm"},
		{name: "deb", goos: "linux", exe: "/usr/bin/exhale", env: fakeEnv{files: map[string]bool{"/var/lib/dpkg/info/exhale.list": true}}, method: MethodPackage,
			command: "curl -fLO https://github.com/akamachinecode/exhale/releases/download/v0.5.0/exhale_linux_" + arch() + ".deb && sudo apt install ./exhale_linux_" + arch() + ".deb"},
		{name: "binary in a home folder", goos: "linux", exe: "/home/a/.local/bin/exhale", method: MethodBinary, writable: true},
		{name: "install.sh service", goos: "linux", exe: "/usr/local/bin/exhale",
			env:    fakeEnv{readOnly: map[string]bool{"/usr/local/bin": true}, vars: map[string]string{"INVOCATION_ID": "x"}},
			method: MethodBinary, command: "curl -fsSLO https://raw.githubusercontent.com/akamachinecode/exhale/main/scripts/install.sh && sh install.sh --service"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := detect(c.goos, c.exe, c.desktop, c.env)
			if in.Method != c.method || in.Writable != c.writable {
				t.Fatalf("detect = %+v, want method %s, writable %v", in, c.method, c.writable)
			}
			if got := in.command("0.5.0"); !in.Writable && got != c.command {
				t.Fatalf("command = %q, want %q", got, c.command)
			}
		})
	}
	if b := detect("darwin", "/Applications/Exhale.app/Contents/MacOS/exhale", true, fakeEnv{}).Bundle; b != "/Applications/Exhale.app" {
		t.Fatalf("bundle = %q", b)
	}
}

// release serves a fake GitHub: the API and the files of one release.
type release struct {
	srv     *httptest.Server
	version string
	files   map[string][]byte
	sums    map[string]string // overrides a file's checksum
	calls   atomic.Int32
}

func newRelease(t *testing.T, version string, files map[string][]byte) *release {
	t.Helper()
	r := &release{version: version, files: files, sums: map[string]string{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/releases/latest":
			r.calls.Add(1)
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			assets := []asset{{"checksums.txt", r.srv.URL + "/dl/checksums.txt"}}
			for name := range r.files {
				assets = append(assets, asset{name, r.srv.URL + "/dl/" + name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v" + r.version, "html_url": "https://example.test/v" + r.version,
				"body": "## Features\n\n* an update button", "published_at": "2026-11-12T10:00:00Z", "assets": assets,
			})
		case req.URL.Path == "/dl/checksums.txt":
			for name, b := range r.files {
				sum := sha256.Sum256(b)
				s := hex.EncodeToString(sum[:])
				if o, ok := r.sums[name]; ok {
					s = o
				}
				fmt.Fprintf(w, "%s  %s\n", s, name)
			}
		case strings.HasPrefix(req.URL.Path, "/dl/"):
			b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/dl/")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// archive is a release tarball whose exhale is a script that says version.
func archive(t *testing.T, version string) []byte {
	t.Helper()
	script := []byte("#!/bin/sh\necho exhale " + version + "\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"LICENSE", []byte("AGPL")}, {"exhale", script}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func binaryUpdater(t *testing.T, api string) (*Updater, string, *atomic.Int32) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the binary update runs on Unix systems")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "exhale")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho exhale 0.4.0\n"), 0o755); err != nil { //nolint:gosec // a test program
		t.Fatal(err)
	}
	var restarts atomic.Int32
	u := &Updater{
		Version: "0.4.0", API: api, Dir: filepath.Join(dir, "work"),
		Install: Install{Method: MethodBinary, Exe: exe, GOOS: runtime.GOOS, Writable: true},
		Restart: func() { restarts.Add(1) },
	}
	return u, exe, &restarts
}

func waitPhase(t *testing.T, u *Updater, want ...Phase) VersionInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		in := u.Info(context.Background(), false)
		for _, w := range want {
			if in.Progress.Phase == w {
				return in
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase %s (%s), want %v", in.Progress.Phase, in.Progress.Error, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestApplyBinary(t *testing.T) {
	name := fmt.Sprintf("exhale_0.5.0_%s_%s.tar.gz", runtime.GOOS, arch())
	rel := newRelease(t, "0.5.0", map[string][]byte{name: archive(t, "0.5.0")})
	u, exe, restarts := binaryUpdater(t, rel.srv.URL)
	ctx := context.Background()

	in := u.Info(ctx, false)
	if !in.Available || !in.CanApply || in.Latest != "0.5.0" || in.Current != "0.4.0" || in.Notes == "" || in.NotesURL == "" {
		t.Fatalf("info = %+v", in)
	}
	if _, err := u.Plan(ctx); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := u.Apply(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := u.Apply(ctx); !errors.Is(err, ErrBusy) {
		// It may already be done; then a second one is refused as busy too.
		t.Fatalf("second apply: %v", err)
	}
	in = waitPhase(t, u, PhaseRestarting, PhaseFailed)
	if in.Progress.Phase != PhaseRestarting {
		t.Fatalf("progress = %+v", in.Progress)
	}
	if in.Progress.Done == 0 {
		t.Fatalf("no bytes counted: %+v", in.Progress)
	}
	got, err := os.ReadFile(exe) //nolint:gosec // the test's own file
	if err != nil || !strings.Contains(string(got), "exhale 0.5.0") {
		t.Fatalf("the binary was not replaced: %q, %v", got, err)
	}
	for range 100 {
		if restarts.Load() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 1 || !u.Restarting() {
		t.Fatalf("restarts = %d, restarting = %v", restarts.Load(), u.Restarting())
	}
	if rel.calls.Load() != 1 {
		t.Fatalf("GitHub asked %d times, want 1 (cached)", rel.calls.Load())
	}
}

func TestApplyRefusesABadChecksum(t *testing.T) {
	name := fmt.Sprintf("exhale_0.5.0_%s_%s.tar.gz", runtime.GOOS, arch())
	rel := newRelease(t, "0.5.0", map[string][]byte{name: archive(t, "0.5.0")})
	rel.sums[name] = strings.Repeat("0", 64)
	u, exe, restarts := binaryUpdater(t, rel.srv.URL)
	if _, err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	in := waitPhase(t, u, PhaseFailed, PhaseRestarting)
	if in.Progress.Phase != PhaseFailed || !strings.Contains(in.Progress.Error, "checksum") {
		t.Fatalf("progress = %+v", in.Progress)
	}
	if got, _ := os.ReadFile(exe); !strings.Contains(string(got), "exhale 0.4.0") { //nolint:gosec // the test's own file
		t.Fatalf("the binary changed: %q", got)
	}
	if restarts.Load() != 0 || u.Restarting() {
		t.Fatal("a failed update must not restart")
	}
	// A failed update can be tried again.
	if _, err := u.Apply(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitPhase(t, u, PhaseFailed, PhaseRestarting)
}

func TestApplyRefusesABinaryThatSaysAnotherVersion(t *testing.T) {
	name := fmt.Sprintf("exhale_0.5.0_%s_%s.tar.gz", runtime.GOOS, arch())
	rel := newRelease(t, "0.5.0", map[string][]byte{name: archive(t, "0.4.9")})
	u, exe, _ := binaryUpdater(t, rel.srv.URL)
	if _, err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	in := waitPhase(t, u, PhaseFailed, PhaseRestarting)
	if in.Progress.Phase != PhaseFailed {
		t.Fatalf("progress = %+v", in.Progress)
	}
	if got, _ := os.ReadFile(exe); !strings.Contains(string(got), "exhale 0.4.0") { //nolint:gosec // the test's own file
		t.Fatalf("the binary changed: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".exhale-") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

func TestNothingToApply(t *testing.T) {
	rel := newRelease(t, "0.4.0", nil)
	u, _, _ := binaryUpdater(t, rel.srv.URL)
	in := u.Info(context.Background(), false)
	if in.Available || in.CanApply {
		t.Fatalf("info = %+v", in)
	}
	if _, err := u.Apply(context.Background()); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("apply: %v", err)
	}

	dev, _, _ := binaryUpdater(t, rel.srv.URL)
	dev.Version = "dev"
	if in := dev.Info(context.Background(), false); in.CanApply || in.Reason != ReasonDevelopment {
		t.Fatalf("dev info = %+v", in)
	}

	docker := &Updater{Version: "0.3.0", API: rel.srv.URL, Install: Install{Method: MethodDocker, GOOS: "linux"}, Restart: func() {}}
	in = docker.Info(context.Background(), false)
	if !in.Available || in.CanApply || in.Reason != ReasonManual || in.Command == "" {
		t.Fatalf("docker info = %+v", in)
	}
	if _, err := docker.Apply(context.Background()); !errors.Is(err, ErrCannot) {
		t.Fatalf("docker apply: %v", err)
	}
}

func TestCheckFailsAndRecovers(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "rate limited", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.5.0","html_url":"x","assets":[]}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	u := &Updater{Version: "0.4.0", API: srv.URL, Now: func() time.Time { return now }}
	if in := u.Info(context.Background(), false); in.CheckError == "" || in.Latest != "" {
		t.Fatalf("info = %+v", in)
	}
	fail = false
	// Within the minute, a refresh reuses the failed check.
	if in := u.Info(context.Background(), true); in.CheckError == "" {
		t.Fatalf("info = %+v", in)
	}
	now = now.Add(2 * time.Minute)
	if in := u.Info(context.Background(), true); in.CheckError != "" || in.Latest != "0.5.0" || !in.Available {
		t.Fatalf("info = %+v", in)
	}
}

func TestDownloadPackage(t *testing.T) {
	name := "exhale_linux_" + arch() + ".rpm"
	body := []byte("not really an rpm")
	rel := newRelease(t, "0.5.0", map[string][]byte{name: body})
	u := &Updater{Version: "0.4.0", API: rel.srv.URL, Dir: filepath.Join(t.TempDir(), "work")}
	file, v, err := u.DownloadPackage(context.Background(), "rpm")
	if err != nil || v != "0.5.0" || filepath.Base(file) != name {
		t.Fatalf("DownloadPackage = %q, %q, %v", file, v, err)
	}
	if got, _ := os.ReadFile(file); string(got) != string(body) { //nolint:gosec // the test's own file
		t.Fatalf("content = %q", got)
	}
	if _, _, err := u.DownloadPackage(context.Background(), "deb"); err == nil {
		t.Fatal("a package the release does not have must fail")
	}
	u.Version = "0.5.0"
	if _, _, err := u.DownloadPackage(context.Background(), "rpm"); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("up to date: %v", err)
	}
}

func TestPackageCommand(t *testing.T) {
	has := func(set ...string) func(string) bool {
		return func(p string) bool {
			for _, s := range set {
				if s == p {
					return true
				}
			}
			return false
		}
	}
	for _, c := range []struct {
		kind string
		has  func(string) bool
		want string
	}{
		{"deb", has(), "/usr/bin/apt-get install -y /tmp/x"},
		{"rpm", has("/usr/bin/dnf"), "/usr/bin/dnf install -y /tmp/x"},
		{"rpm", has("/usr/bin/zypper"), "/usr/bin/zypper --non-interactive install --allow-unsigned-rpm /tmp/x"},
		{"rpm", has(), "/usr/bin/rpm -U /tmp/x"},
	} {
		if got := strings.Join(PackageCommand(c.kind, "/tmp/x", c.has), " "); got != c.want {
			t.Errorf("PackageCommand(%s) = %q, want %q", c.kind, got, c.want)
		}
	}
}
