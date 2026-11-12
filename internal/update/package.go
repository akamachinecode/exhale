package update

import (
	"context"
	"os"
	"os/exec"
)

// The Linux packages run the server as the exhale system user, which may
// not replace /usr/bin/exhale. Exhale's window runs as the person at the
// desktop, so it updates them instead (ADR 0033): it downloads the newest
// package, checks it, and installs it with pkexec, which asks for the
// person's password in the system's own dialog.

// PackageKind is the kind of package Exhale was installed from on this
// machine: deb when dpkg knows it, else rpm.
func PackageKind() string {
	if _, err := os.Stat("/var/lib/dpkg/info/exhale.list"); err == nil {
		return "deb"
	}
	return "rpm"
}

// packageAsset is the release file of the package for this machine.
func packageAsset(kind string) string { return "exhale_linux_" + arch() + "." + kind }

// DownloadPackage downloads the newest .deb or .rpm into Dir when it is
// newer than Version, checked against the release's checksums. It returns
// the file and its version.
func (u *Updater) DownloadPackage(ctx context.Context, kind string) (string, string, error) {
	r, err := u.Check(ctx, true)
	if err != nil {
		return "", "", err
	}
	if r == nil {
		return "", "", ErrNotYet
	}
	if !Newer(r.Version, u.Version) {
		return "", "", ErrUpToDate
	}
	file, err := u.fetchAsset(ctx, r, packageAsset(kind))
	if err != nil {
		return "", "", err
	}
	// The package manager reads it as root; the folder stays the person's.
	if err := os.Chmod(file, 0o644); err != nil { //nolint:gosec // a package anyone may read, as on the download page
		return "", "", err
	}
	if err := os.Chmod(u.Dir, 0o755); err != nil { //nolint:gosec // so root's package manager can reach the file
		return "", "", err
	}
	return file, r.Version, nil
}

// InstallPackage installs a downloaded package as root through pkexec,
// which shows the system's password dialog. The package restarts the
// service; the window follows its program (package window).
func InstallPackage(ctx context.Context, kind, file string) error {
	args := PackageCommand(kind, file, lookPath)
	return run(ctx, "/usr/bin/pkexec", args...)
}

// PackageCommand is the package manager's command that installs file.
func PackageCommand(kind, file string, has func(string) bool) []string {
	if kind == "deb" {
		return []string{"/usr/bin/apt-get", "install", "-y", file}
	}
	for _, m := range []string{"/usr/bin/dnf", "/usr/bin/zypper"} {
		if has(m) {
			if m == "/usr/bin/zypper" {
				return []string{m, "--non-interactive", "install", "--allow-unsigned-rpm", file}
			}
			return []string{m, "install", "-y", file}
		}
	}
	return []string{"/usr/bin/rpm", "-U", file}
}

func lookPath(p string) bool {
	_, err := exec.LookPath(p)
	return err == nil
}
