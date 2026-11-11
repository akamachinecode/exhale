//go:build desktop && linux

package window

import (
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// sandboxOff is WebKitGTK's switch for its web process sandbox.
const sandboxOff = "WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS"

// webkitSandbox keeps WebKitGTK's sandbox wherever it can run. WebKit runs
// each page inside bubblewrap, which needs unprivileged user namespaces;
// Ubuntu (since 24.04) lets AppArmor refuse them to programs without a
// profile, and WebKit then aborts the whole window. Only there, when
// bubblewrap really cannot start, the window goes without that sandbox:
// it shows Exhale's own pages from this computer.
func webkitSandbox(log *slog.Logger) {
	if os.Getenv(sandboxOff) != "" {
		return
	}
	restricted, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	if err != nil || strings.TrimSpace(string(restricted)) != "1" {
		return
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return
	}
	if exec.Command(bwrap, "--ro-bind", "/", "/", "--unshare-user", "--", "/bin/true").Run() == nil { //nolint:gosec // the system's bubblewrap, fixed arguments
		return
	}
	log.Warn("window: AppArmor does not let WebKit sandbox its pages here; showing Exhale without that sandbox")
	_ = os.Setenv(sandboxOff, "1")
}
