package awake

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests take real inhibitor locks and drop them. They only run with
// EXHALE_TEST_AWAKE=1, on a machine with systemd.

func realLocks(t *testing.T) {
	t.Helper()
	if os.Getenv("EXHALE_TEST_AWAKE") != "1" {
		t.Skip("set EXHALE_TEST_AWAKE=1 to take a real inhibitor lock")
	}
	if err := New().Available(); err != nil {
		t.Skip(err)
	}
}

// TestLinuxHoldAndRelease takes the lock Exhale takes. Blocking sleep needs
// an active session (polkit): elsewhere the refusal must come back as an
// error, which is what people see in the web.
func TestLinuxHoldAndRelease(t *testing.T) {
	realLocks(t)
	k := New()
	err := k.Hold(false)
	if err != nil {
		if strings.Contains(err.Error(), "systemd-inhibit:") {
			t.Skipf("refused, as reported to people: %v", err)
		}
		t.Fatal(err)
	}
	if !strings.Contains(inhibitors(t), "Sending reminders") {
		k.Release()
		t.Fatal("no Exhale lock while holding")
	}
	if err := k.Lost(); err != nil {
		t.Fatal(err)
	}
	k.Release()
	if strings.Contains(inhibitors(t), "Sending reminders") {
		t.Fatal("the lock outlived Release")
	}
	k.Release() // a second release does nothing
}

// idleKeeper takes only an idle lock, which any session may take, to test
// the helper's life cycle.
func idleKeeper(why string) *procKeeper {
	return &procKeeper{
		available: func() error { return nil },
		command: func(bool) (string, *exec.Cmd) {
			cmd := exec.Command("systemd-inhibit", "--what=idle", "--mode=block", "--who=Exhale test", "--why="+why, "sleep", "infinity")
			cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
			return "systemd-inhibit", cmd
		},
	}
}

func TestLinuxHelperLifecycle(t *testing.T) {
	realLocks(t)
	k := idleKeeper("exhale-lifecycle")
	if err := k.Hold(false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inhibitors(t), "exhale-lifecycle") {
		k.Release()
		t.Fatal("no lock while holding")
	}
	// Killing the helper is noticed as a lost hold.
	_ = k.p.cmd.Process.Kill()
	<-k.p.done
	if k.Lost() == nil {
		t.Fatal("a killed helper must be reported as lost")
	}
	k.Release()
	if err := k.Hold(false); err != nil {
		t.Fatal(err)
	}
	k.Release()
	if k.Lost() != nil || strings.Contains(inhibitors(t), "exhale-lifecycle") {
		t.Fatal("the lock outlived Release")
	}
}

// TestLinuxLockDiesWithExhale runs a child that holds a lock and exits
// without releasing it: the helper must go with it (Pdeathsig).
func TestLinuxLockDiesWithExhale(t *testing.T) {
	if os.Getenv("EXHALE_TEST_AWAKE_CHILD") == "1" {
		if err := idleKeeper("exhale-orphan").Hold(false); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	realLocks(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxLockDiesWithExhale$")
	cmd.Env = append(os.Environ(), "EXHALE_TEST_AWAKE_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	deadline := time.Now().Add(3 * time.Second)
	for strings.Contains(inhibitors(t), "exhale-orphan") {
		if time.Now().After(deadline) {
			t.Fatal("the lock outlived the process that took it")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func inhibitors(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("systemd-inhibit", "--list", "--no-pager").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
