package update

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func execSelf(string) error { return errors.New("update: Windows updates through its installer") }

// startSetup runs Exhale-Setup.exe silently, detached from this process,
// which is about to exit. /open makes it open Exhale again once installed
// (deploy/desktop/windows/exhale.nsi).
func startSetup(file string) error {
	cmd := exec.Command(file, "/S", "/open") //nolint:gosec // the installer we just downloaded and checked
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
