// Package desktop is Exhale installed as a desktop app (ADR 0029, 0030).
// Opening the app makes sure the server runs in the background, as the
// person who opened it and from the next login on, then shows the web UI in
// a window of its own (package window), or in the browser where there is
// none.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/i18n"
)

// ID names Exhale to the operating system: the bundle identifier of the
// macOS app and the label of its launchd agent (as in deploy/launchd).
const ID = "io.github.akamachinecode.exhale"

// VersionHeader carries the server's version in the answer of /healthz, so
// an upgraded app can tell that an older server is still running.
const VersionHeader = "X-Exhale-Version"

// startTimeout bounds the wait for a server that was just started: the
// first start creates and migrates the database.
const startTimeout = 60 * time.Second

// errNotPlaced stops an app opened from a place it cannot run from.
var errNotPlaced = errors.New("exhale: open the app from the Applications folder")

// errSystemService is a stopped system service, which only root can start.
var errSystemService = errors.New("exhale is not running: start it with sudo systemctl start exhale")

// App is this program, about to be opened.
type App struct {
	Exe     string // this program, with symlinks resolved
	Listen  string // address the server listens on, as given to serve
	Version string // version of this program
	DataDir string // the server's data directory
	LogFile string // where a server started from here writes its log
	// Login is set in the desktop builds (Exhale.app, Exhale.exe): they
	// also make Exhale start when the person logs in.
	Login bool
	// Window shows the web UI in a window of its own. It is set in builds
	// with the desktop tag; without it, a window program next to this one
	// (exhale-window in the Linux packages) is used, or else the browser.
	Window func(url string) error
	// Browser opens the browser even where there is a window.
	Browser bool
}

// Open shows the web UI, first starting the server in the background when
// it does not answer.
func (a App) Open(ctx context.Context) error {
	lang := i18n.Match(systemLanguages()...)
	if a.Login && misplaced(a.Exe) {
		alert(tr(lang, "Move Exhale to Applications"), tr(lang, "Drag Exhale into the Applications folder, then open it from there."))
		return errNotPlaced
	}
	url := "http://" + browseAddr(a.Listen)
	up, version := probe(ctx, url)
	want, err := a.start(ctx, up, version)
	if err == nil {
		err = waitFor(ctx, url, want, startTimeout)
	}
	if errors.Is(err, errSystemService) {
		alert(tr(lang, "Exhale is not running"), tr(lang, "Start it with: sudo systemctl start exhale"))
		return err
	}
	if err != nil {
		alert(tr(lang, "Exhale did not start"), err.Error()+"\n\n"+fmt.Sprintf(tr(lang, "Its log may say why: %s"), a.logPath()))
		return err
	}
	return a.show(ctx, url)
}

// show puts the web UI in front of the person: in a window of this program,
// in the window program next to it, or in the browser.
func (a App) show(ctx context.Context, url string) error {
	switch {
	case a.Browser:
		return browse(url)
	case a.Window != nil:
		return a.Window(url)
	}
	if p := windowProgram(a.Exe); p != "" {
		err := runWindow(ctx, p, url)
		if !errors.Is(err, errNoWindow) {
			return err
		}
	}
	return browse(url)
}

// errNoWindow is a window program that cannot start here: its libraries
// are missing (a server without a desktop) or too old.
var errNoWindow = errors.New("exhale: no window on this system")

// windowProgram is exhale-window next to exe, if it is installed.
func windowProgram(exe string) string {
	p := filepath.Join(filepath.Dir(exe), "exhale-window")
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		return p
	}
	return ""
}

// runWindow runs the window program until its window is closed. The dynamic
// loader answers 127 when the program's libraries are missing.
func runWindow(ctx context.Context, program, url string) error {
	cmd := exec.CommandContext(ctx, program, "--url", url) //nolint:gosec // our own program, installed next to this one
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 127 {
		return errNoWindow
	}
	return err
}

// tr translates a text of the dialogs (the catalogs in internal/i18n).
func tr(lang, text string) string { return i18n.Translate(lang, text) }

// browseAddr turns a listen address into one a browser on this machine can
// open: a server listening on every interface answers on loopback too.
func browseAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// probe reports whether Exhale answers at url, and its version.
func probe(ctx context.Context, url string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/healthz", nil)
	if err != nil {
		return false, ""
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, ""
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 16))
	if res.StatusCode != http.StatusOK || string(body) != "ok" {
		return false, ""
	}
	return true, res.Header.Get(VersionHeader)
}

// waitFor waits until Exhale answers at url, in version want when one is
// given (a restarted server replaces an older one).
func waitFor(ctx context.Context, url, want string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		up, version := probe(ctx, url)
		if up && (want == "" || version == "" || version == want) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("exhale did not answer at %s within %s", url, timeout)
		case <-tick.C:
		}
	}
}

// spawn starts `exhale serve` detached from this process, which then exits.
func (a App) spawn() error {
	cmd := exec.Command(a.Exe, "serve", "--listen", a.Listen, "--data-dir", a.DataDir, "--log-file", a.logPath()) //nolint:gosec // this same program, with its own flags
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// logPath is where the server's log can be read.
func (a App) logPath() string {
	if a.LogFile != "" {
		return a.LogFile
	}
	return DefaultLogFile(a.DataDir)
}

// languagesFromEnv reads the POSIX locale variables: "es_ES.UTF-8" → "es_ES".
func languagesFromEnv() []string {
	var out []string
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		for _, v := range strings.Split(os.Getenv(k), ":") {
			v, _, _ = strings.Cut(v, ".")
			if v != "" && v != "C" && v != "POSIX" {
				out = append(out, v)
			}
		}
	}
	return out
}

// parseAppleLanguages reads `defaults read -g AppleLanguages`, a property
// list array such as ( "es-ES", en ).
func parseAppleLanguages(out string) []string {
	out = strings.NewReplacer("(", "", ")", "", `"`, "").Replace(out)
	var langs []string
	for _, f := range strings.FieldsFunc(out, func(r rune) bool { return r == ',' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			langs = append(langs, f)
		}
	}
	return langs
}
