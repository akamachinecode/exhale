// Package update keeps Exhale up to date from its GitHub releases (ADR 0033).
// It finds the newest release, tells how this copy of Exhale was installed,
// and where Exhale may replace itself (the macOS app, the Windows app, a
// binary in a folder it can write) it downloads the new version, checks it
// against the release's checksums, puts it in place and restarts. Elsewhere
// (Docker, the Linux packages, a binary owned by root) it says which command
// to run.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultAPI is the GitHub API of Exhale's repository.
const DefaultAPI = "https://api.github.com/repos/akamachinecode/exhale"

// releasesURL is where people download releases by hand.
const releasesURL = "https://github.com/akamachinecode/exhale/releases"

// maxDownload bounds a downloaded file: the dmg is about 40 MB.
const maxDownload = 400 << 20

// cacheFor is how long a check is reused before GitHub is asked again;
// refreshing by hand asks again after refreshAfter.
const (
	cacheFor     = 6 * time.Hour
	refreshAfter = time.Minute
)

// Method is how this copy of Exhale was installed, which decides how it
// updates.
type Method string

const (
	MethodMacApp  Method = "macos-app"   // Exhale.app, from Exhale.dmg
	MethodWindows Method = "windows-app" // Exhale.exe, from Exhale-Setup.exe
	MethodBinary  Method = "binary"      // the exhale binary from an archive or install.sh
	MethodPackage Method = "package"     // the .deb or .rpm
	MethodDocker  Method = "docker"      // the container image
)

// Reasons why Exhale cannot update itself (Info.Reason).
const (
	ReasonDevelopment = "development" // a build from source, with no version
	ReasonManual      = "manual"      // Docker and the packages: updated from outside
	ReasonReadOnly    = "read-only"   // Exhale may not write where it is installed
	ReasonUnsupported = "unsupported" // no installer for this system
)

// Phase is where an update is.
type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseDownloading Phase = "downloading"
	PhaseInstalling  Phase = "installing"
	PhaseRestarting  Phase = "restarting"
	PhaseFailed      Phase = "failed"
)

// VersionInfo is what get_update answers.
type VersionInfo struct {
	Current     string          `json:"current" jsonschema:"the version running now; dev for a build from source"`
	Latest      string          `json:"latest,omitempty" jsonschema:"the newest release, once GitHub has been asked"`
	Available   bool            `json:"available" jsonschema:"the newest release is newer than this one"`
	NotesURL    string          `json:"notesUrl,omitempty" jsonschema:"the release's page, with what changed"`
	Notes       string          `json:"notes,omitempty" jsonschema:"what changed, in Markdown"`
	PublishedAt string          `json:"publishedAt,omitempty" jsonschema:"when the newest release came out, RFC 3339"`
	CheckedAt   string          `json:"checkedAt,omitempty" jsonschema:"when GitHub was last asked, RFC 3339"`
	CheckError  string          `json:"checkError,omitempty" jsonschema:"why the last check failed"`
	Method      Method          `json:"method" enum:"macos-app,windows-app,binary,package,docker" jsonschema:"how this copy of Exhale was installed"`
	CanApply    bool            `json:"canApply" jsonschema:"apply_update can install the newest release here"`
	Reason      string          `json:"reason,omitempty" enum:",development,manual,read-only,unsupported" jsonschema:"why Exhale cannot update itself here"`
	Command     string          `json:"command,omitempty" jsonschema:"what to run to update instead, when Exhale cannot do it itself"`
	Download    string          `json:"download,omitempty" jsonschema:"the file to download by hand instead"`
	Progress    InstallProgress `json:"progress"`
}

// InstallProgress is an update under way, or the last one that failed.
type InstallProgress struct {
	Phase   Phase  `json:"phase" enum:"idle,downloading,installing,restarting,failed"`
	Version string `json:"version,omitempty" jsonschema:"the version being installed"`
	Done    int64  `json:"done" jsonschema:"bytes downloaded"`
	Total   int64  `json:"total" jsonschema:"bytes to download, 0 when unknown"`
	Error   string `json:"error,omitempty" jsonschema:"why the update failed; Exhale kept running the version it had"`
}

// Errors of Apply.
var (
	ErrBusy     = errors.New("update: an update is already under way")
	ErrUpToDate = errors.New("update: this is already the newest version")
	ErrCannot   = errors.New("update: Exhale cannot update itself here")
	ErrNotYet   = errors.New("update: GitHub has not been asked yet")
)

// Release is a published version of Exhale.
type Release struct {
	Version   string
	URL       string
	Notes     string
	Published time.Time
	Assets    map[string]string // file name → download address
}

// Updater checks for new versions and installs them.
type Updater struct {
	Version string  // running now
	Install Install // how this copy was installed
	API     string  // GitHub API of the repository (DefaultAPI)
	Client  *http.Client
	Dir     string        // work folder for downloads: <data dir>/update
	Every   time.Duration // how often Serve checks; 0 never
	// OnlyWhenAsked: GitHub is asked only by a refresh (Check now), never
	// by itself (--no-update-check).
	OnlyWhenAsked bool
	Log           *slog.Logger
	// Restart stops the server; its caller then calls Finish, which starts
	// the new version. Without it, Exhale never updates itself.
	Restart func()
	Now     func() time.Time

	mu       sync.Mutex
	release  *Release
	checked  time.Time
	checkErr error
	progress InstallProgress
	finish   func() error
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (u *Updater) log() *slog.Logger {
	if u.Log != nil {
		return u.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (u *Updater) String() string { return "updates" }

// Serve checks for a new version a minute after the start, then every
// Every. It also clears what an earlier update left in Dir.
func (u *Updater) Serve(ctx context.Context) error {
	if u.Dir != "" {
		_ = os.RemoveAll(u.Dir)
	}
	if u.Every <= 0 {
		<-ctx.Done()
		return nil
	}
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		if _, err := u.Check(ctx, false); err != nil {
			u.log().Info("could not check for a new version", "err", err)
		}
		wait = u.Every
	}
}

// Check asks GitHub for the newest release, unless it was asked recently.
// refresh asks again unless it was asked within the last minute.
func (u *Updater) Check(ctx context.Context, refresh bool) (*Release, error) {
	u.mu.Lock()
	fresh := cacheFor
	if refresh {
		fresh = refreshAfter
	}
	if (!u.checked.IsZero() && u.now().Sub(u.checked) < fresh) || (u.OnlyWhenAsked && !refresh) {
		r, err := u.release, u.checkErr
		u.mu.Unlock()
		return r, err
	}
	u.mu.Unlock()
	r, err := u.fetch(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checked, u.checkErr = u.now(), err
	if err == nil {
		u.release = r
	}
	return u.release, err
}

// fetch reads the newest release from the GitHub API.
func (u *Updater) fetch(ctx context.Context) (*Release, error) {
	api := u.API
	if api == "" {
		api = DefaultAPI
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(api, "/")+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "exhale/"+u.Version)
	res, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", res.Status)
	}
	var body struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("reading the release: %w", err)
	}
	v := strings.TrimPrefix(body.TagName, "v")
	if _, ok := parse(v); !ok {
		return nil, fmt.Errorf("the newest release has no version: %q", body.TagName)
	}
	r := &Release{Version: v, URL: body.HTMLURL, Notes: body.Body, Published: body.PublishedAt, Assets: map[string]string{}}
	for _, a := range body.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// Info says what is installed, what is newest and how to update.
func (u *Updater) Info(ctx context.Context, refresh bool) VersionInfo {
	r, err := u.Check(ctx, refresh)
	u.mu.Lock()
	defer u.mu.Unlock()
	in := VersionInfo{Current: u.Version, Method: u.Install.Method, Progress: u.progress}
	if in.Progress.Phase == "" {
		in.Progress.Phase = PhaseIdle
	}
	if !u.checked.IsZero() {
		in.CheckedAt = u.checked.UTC().Format(time.RFC3339)
	}
	if err != nil {
		in.CheckError = err.Error()
	}
	if r != nil {
		in.Latest, in.NotesURL, in.Notes = r.Version, r.URL, r.Notes
		if !r.Published.IsZero() {
			in.PublishedAt = r.Published.UTC().Format(time.RFC3339)
		}
		in.Available = Newer(r.Version, u.Version)
	}
	in.CanApply, in.Reason = u.canApply()
	if !in.CanApply && r != nil {
		in.Command = u.Install.command(r.Version)
		in.Download = u.Install.download(r)
	}
	if !in.Available {
		in.CanApply = false
	}
	return in
}

// canApply reports whether Exhale can update itself here, or why not.
func (u *Updater) canApply() (bool, string) {
	if _, ok := parse(u.Version); !ok {
		return false, ReasonDevelopment
	}
	switch u.Install.Method {
	case MethodDocker, MethodPackage:
		return false, ReasonManual
	default:
	}
	if u.Install.asset("0.0.0") == "" {
		return false, ReasonUnsupported
	}
	if !u.Install.Writable {
		return false, ReasonReadOnly
	}
	if u.Restart == nil {
		return false, ReasonUnsupported
	}
	return true, ""
}

// Apply starts installing the newest release, in the background: Info
// follows it. When the new version is in place, the server restarts.
func (u *Updater) Apply(ctx context.Context) (VersionInfo, error) {
	r, err := u.Check(ctx, false)
	if r == nil {
		if err == nil {
			err = ErrNotYet
		}
		return VersionInfo{}, err
	}
	u.mu.Lock()
	switch u.progress.Phase {
	case PhaseDownloading, PhaseInstalling, PhaseRestarting:
		u.mu.Unlock()
		return VersionInfo{}, ErrBusy
	default: // idle, or failed and tried again
	}
	if !Newer(r.Version, u.Version) {
		u.mu.Unlock()
		return VersionInfo{}, ErrUpToDate
	}
	if ok, _ := u.canApply(); !ok {
		u.mu.Unlock()
		return VersionInfo{}, ErrCannot
	}
	u.progress = InstallProgress{Phase: PhaseDownloading, Version: r.Version}
	u.mu.Unlock()
	u.log().Info("updating Exhale", "from", u.Version, "to", r.Version, "method", u.Install.Method)
	go u.run(r) //nolint:gosec // the update outlives the request that started it
	return u.Info(ctx, false), nil
}

// Plan checks that Apply would start, without starting it (a dry run).
func (u *Updater) Plan(ctx context.Context) (VersionInfo, error) {
	in := u.Info(ctx, false)
	switch {
	case in.Latest == "":
		if in.CheckError != "" {
			return in, errors.New(in.CheckError)
		}
		return in, ErrNotYet
	case in.Progress.Phase == PhaseDownloading || in.Progress.Phase == PhaseInstalling || in.Progress.Phase == PhaseRestarting:
		return in, ErrBusy
	case !in.Available:
		return in, ErrUpToDate
	case !in.CanApply:
		return in, ErrCannot
	}
	return in, nil
}

// run downloads, checks and installs r, then asks for a restart.
func (u *Updater) run(r *Release) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	finish, err := u.install(ctx, r)
	u.mu.Lock()
	if err != nil {
		u.progress.Phase, u.progress.Error = PhaseFailed, err.Error()
		u.mu.Unlock()
		u.log().Warn("the update failed; this version keeps running", "to", r.Version, "err", err)
		return
	}
	u.progress.Phase = PhaseRestarting
	u.finish = finish
	u.mu.Unlock()
	u.log().Info("the new version is in place; restarting", "version", r.Version)
	// Long enough for the answer that shows "restarting" to go out.
	time.Sleep(500 * time.Millisecond)
	u.Restart()
}

// Finish starts the new version once the server has stopped: the new
// binary takes this process's place, or the Windows installer runs. It
// does nothing when no update is waiting.
func (u *Updater) Finish() error {
	u.mu.Lock()
	f := u.finish
	u.mu.Unlock()
	if f == nil {
		return nil
	}
	return f()
}

// Restarting reports whether the server stopped to start a new version.
func (u *Updater) Restarting() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.finish != nil
}

func (u *Updater) setPhase(p Phase) {
	u.mu.Lock()
	u.progress.Phase = p
	u.mu.Unlock()
}

// install downloads r's file for this system and puts it in place. It
// returns what Finish runs.
func (u *Updater) install(ctx context.Context, r *Release) (func() error, error) {
	name := u.Install.asset(r.Version)
	link, ok := r.Assets[name]
	if !ok {
		return nil, fmt.Errorf("the release has no %s", name)
	}
	sumsLink, ok := r.Assets["checksums.txt"]
	if !ok {
		return nil, errors.New("the release has no checksums.txt")
	}
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return nil, err
	}
	sums, err := u.get(ctx, sumsLink, 1<<20, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading checksums.txt: %w", err)
	}
	want, err := checksumOf(sums, name)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(u.Dir, name)
	if err := u.download(ctx, link, file, want); err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	u.setPhase(PhaseInstalling)
	switch u.Install.Method {
	case MethodMacApp:
		return u.installMacApp(ctx, file, r.Version)
	case MethodWindows:
		return u.installWindows(file)
	default:
		return u.installBinary(ctx, file, r.Version)
	}
}

// get reads a small file, such as checksums.txt.
func (u *Updater) get(ctx context.Context, link string, limit int64, w io.Writer) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "exhale/"+u.Version)
	res, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", res.Status)
	}
	if w != nil {
		u.mu.Lock()
		u.progress.Total = max(res.ContentLength, 0)
		u.mu.Unlock()
		n, err := io.Copy(w, io.LimitReader(res.Body, limit+1))
		if err == nil && n > limit {
			err = errors.New("the file is larger than expected")
		}
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("the file is larger than expected")
	}
	return b, err
}

// download saves link to file, and keeps it only if its SHA-256 is want.
func (u *Updater) download(ctx context.Context, link, file, want string) error {
	part := file + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // our own work folder
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = u.get(ctx, link, maxDownload, io.MultiWriter(f, h, counter{u}))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		_ = os.Remove(part)
		return fmt.Errorf("its checksum does not match the release's (got %s, want %s)", got, want)
	}
	return os.Rename(part, file)
}

// counter adds what is downloaded to the progress.
type counter struct{ u *Updater }

func (c counter) Write(p []byte) (int, error) {
	c.u.mu.Lock()
	c.u.progress.Done += int64(len(p))
	c.u.mu.Unlock()
	return len(p), nil
}

// checksumOf finds name's SHA-256 in a checksums.txt ("<hex>  <name>").
func checksumOf(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt does not list %s", name)
}

// Newer reports whether version a comes after b. A build without a version
// (dev) is never older than anything: it does not update.
func Newer(a, b string) bool {
	va, ok := parse(a)
	if !ok {
		return false
	}
	vb, ok := parse(b)
	if !ok {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// parse reads X.Y.Z (a leading v allowed); anything after a - or + is
// ignored: releases have none.
func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// arch is the CPU as the release's file names write it.
func arch() string {
	if runtime.GOARCH == "arm" {
		return "armv7"
	}
	return runtime.GOARCH
}
