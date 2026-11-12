package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Install is this copy of Exhale: how it was installed and whether it may
// replace itself.
type Install struct {
	Method  Method
	Exe     string // this program, symlinks resolved
	Bundle  string // macOS: the Exhale.app that holds Exe
	Package string // deb or rpm
	Service bool   // a systemd service set up by install.sh --service
	// Writable: Exhale may put a new version where it is installed.
	Writable bool
	GOOS     string
}

// Detect finds out how the program at exe was installed. desktopApp is set
// in the builds of Exhale.app and Exhale.exe.
func Detect(exe string, desktopApp bool) Install {
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return detect(runtime.GOOS, exe, desktopApp, osEnv{})
}

// env is what detect reads of the system, so tests can stand in for it.
type env interface {
	exists(path string) bool
	writable(dir string) bool
	getenv(key string) string
}

type osEnv struct{}

func (osEnv) exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (osEnv) writable(dir string) bool { return writable(dir) }

func (osEnv) getenv(k string) string { return os.Getenv(k) }

func detect(goos, exe string, desktopApp bool, e env) Install {
	in := Install{Exe: exe, GOOS: goos, Method: MethodBinary}
	// The folder of a path as goos writes it (tests stand in for others).
	dir := path.Dir
	if goos == "windows" {
		dir = filepath.Dir
	}
	switch {
	case goos == "darwin" && strings.Contains(exe, ".app/Contents/MacOS/"):
		in.Method = MethodMacApp
		in.Bundle = exe[:strings.Index(exe, ".app/Contents/MacOS/")+len(".app")]
		// Swapping the bundle is two renames in its folder. Nothing is
		// written inside the app to find out: macOS would warn about it.
		in.Writable = e.writable(dir(in.Bundle))
	case goos == "windows" && desktopApp:
		in.Method = MethodWindows
		in.Writable = e.writable(dir(exe))
	case goos == "linux" && (e.exists("/.dockerenv") || e.exists("/run/.containerenv")):
		in.Method = MethodDocker
	case goos == "linux" && (exe == "/usr/bin/exhale" || exe == "/usr/bin/exhale-window"):
		in.Method = MethodPackage
		in.Package = "rpm"
		if e.exists("/var/lib/dpkg/info/exhale.list") {
			in.Package = "deb"
		}
	default:
		in.Writable = e.writable(dir(exe))
		in.Service = goos == "linux" && e.getenv("INVOCATION_ID") != ""
	}
	return in
}

// asset is the release file this install updates from; empty when there
// is none it can install by itself.
func (in Install) asset(version string) string {
	switch in.Method {
	case MethodMacApp:
		return "Exhale.dmg"
	case MethodWindows:
		return "Exhale-Setup.exe"
	case MethodBinary:
		if in.GOOS == "windows" {
			return ""
		}
		return fmt.Sprintf("exhale_%s_%s_%s.tar.gz", version, in.GOOS, arch())
	default: // packages and Docker update from outside
		return ""
	}
}

// command is what to run to update by hand to version, where Exhale cannot
// do it itself; empty when downloading a file is the way.
func (in Install) command(version string) string {
	base := releasesURL + "/download/v" + version + "/"
	switch in.Method {
	case MethodDocker:
		return "docker compose pull && docker compose up -d"
	case MethodPackage:
		file := "exhale_linux_" + arch() + "." + in.Package
		if in.Package == "deb" {
			return "curl -fLO " + base + file + " && sudo apt install ./" + file
		}
		return "sudo dnf install " + base + file
	case MethodBinary:
		if in.GOOS != "linux" {
			return ""
		}
		cmd := "curl -fsSLO https://raw.githubusercontent.com/akamachinecode/exhale/main/scripts/install.sh && sh install.sh"
		if in.Service {
			cmd += " --service"
		}
		return cmd
	default: // the apps update themselves, or download the installer
		return ""
	}
}

// download is the release file to download by hand, where there is no
// command.
func (in Install) download(r *Release) string {
	if in.command(r.Version) != "" {
		return ""
	}
	name := in.asset(r.Version)
	if name == "" && in.GOOS == "windows" {
		name = fmt.Sprintf("exhale_%s_windows_%s.zip", r.Version, arch())
	}
	if link, ok := r.Assets[name]; ok {
		return link
	}
	return r.URL
}

// installBinary puts the binary in the archive at file in place of this
// one. Finish then runs it in this process's place.
func (u *Updater) installBinary(ctx context.Context, file, version string) (func() error, error) {
	exe := u.Install.Exe
	next := filepath.Join(filepath.Dir(exe), fmt.Sprintf(".exhale-%s-%d", version, os.Getpid()))
	if err := extract(file, "exhale", next); err != nil {
		return nil, err
	}
	if err := checkVersion(ctx, next, version); err != nil {
		_ = os.Remove(next)
		return nil, err
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Remove(next)
		return nil, err
	}
	return func() error { return execSelf(exe) }, nil
}

// extract writes the file called name in the tar.gz archive to dst.
func extract(archive, name, dst string) error {
	f, err := os.Open(archive) //nolint:gosec // our own download, checked
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no %s", name)
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != name {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec // an executable, like the one it replaces
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, maxDownload))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
		return err
	}
}

// checkVersion runs `program version` and expects version: a binary that
// does not start on this machine is never put in place.
func checkVersion(ctx context.Context, program, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, program, "version").Output() //nolint:gosec // the release we just checked
	if err != nil {
		return fmt.Errorf("the new version does not start here: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != "exhale "+version {
		return fmt.Errorf("the new version says %q, not %q", got, "exhale "+version)
	}
	return nil
}

// installMacApp copies Exhale.app out of the disk image at file and puts it
// in place of the running one. Finish then runs the new binary in this
// process's place; the window follows by itself (package window).
func (u *Updater) installMacApp(ctx context.Context, file, version string) (func() error, error) {
	bundle := u.Install.Bundle
	mnt := filepath.Join(u.Dir, "mnt")
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return nil, err
	}
	if err := run(ctx, "/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, file); err != nil {
		return nil, err
	}
	defer func() { _ = run(context.Background(), "/usr/bin/hdiutil", "detach", "-force", mnt) }()
	// Copied next to the old one, so the swap is a rename on one volume.
	next := filepath.Join(filepath.Dir(bundle), fmt.Sprintf(".Exhale-%s-%d.app", version, os.Getpid()))
	_ = os.RemoveAll(next)
	if err := run(ctx, "/usr/bin/ditto", filepath.Join(mnt, "Exhale.app"), next); err != nil {
		_ = os.RemoveAll(next)
		return nil, err
	}
	exeIn := func(b string) string { return filepath.Join(b, strings.TrimPrefix(u.Install.Exe, bundle)) }
	if err := checkVersion(ctx, exeIn(next), version); err != nil {
		_ = os.RemoveAll(next)
		return nil, err
	}
	old := filepath.Join(filepath.Dir(bundle), fmt.Sprintf(".Exhale-old-%d.app", os.Getpid()))
	if err := os.Rename(bundle, old); err != nil {
		_ = os.RemoveAll(next)
		return nil, u.handOver(file, fmt.Errorf("macOS did not let Exhale replace itself: %w", err))
	}
	if err := os.Rename(next, bundle); err != nil {
		_ = os.Rename(old, bundle)
		_ = os.RemoveAll(next)
		return nil, u.handOver(file, fmt.Errorf("macOS did not let Exhale replace itself: %w", err))
	}
	_ = os.RemoveAll(old)
	return func() error { return execSelf(u.Install.Exe) }, nil
}

// handOver opens the downloaded disk image in Finder when Exhale could not
// replace itself, so dragging the app to Applications finishes the job.
func (u *Updater) handOver(dmg string, err error) error {
	if oerr := exec.Command("/usr/bin/open", dmg).Run(); oerr == nil { //nolint:gosec // our own download, checked
		return fmt.Errorf("%w. The new version is open in Finder: drag Exhale to Applications to finish", err)
	}
	return err
}

// installWindows keeps the downloaded installer: Finish runs it silently
// once the server has stopped. It replaces Exhale.exe and opens Exhale again.
func (u *Updater) installWindows(file string) (func() error, error) {
	return func() error { return startSetup(file) }, nil
}

// run runs a system tool, with its output in the error.
func run(ctx context.Context, name string, args ...string) error {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed system tools, our own arguments
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(out.String()))
	}
	return nil
}
