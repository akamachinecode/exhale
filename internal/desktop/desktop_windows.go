package desktop

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// runKey holds the programs Windows starts when the person signs in.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func misplaced(string) bool { return false }

// start makes sure the server runs. Exhale.exe also adds itself to the
// programs started at sign-in; the uninstaller removes that entry. A
// server of another version is left alone: the installer stops it before
// replacing the files.
func (a App) start(_ context.Context, up bool, _ string) (string, error) {
	if a.Login {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			return "", err
		}
		line := `"` + a.Exe + `" serve`
		if v, _, err := k.GetStringValue("Exhale"); err != nil || v != line {
			err = k.SetStringValue("Exhale", line)
			if err != nil {
				_ = k.Close()
				return "", err
			}
		}
		_ = k.Close()
	}
	if up {
		return "", nil
	}
	return "", a.spawn()
}

// DefaultLogFile is the log of a server started without a console.
func DefaultLogFile(dataDir string) string { return filepath.Join(dataDir, "exhale.log") }

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func browse(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() //nolint:gosec // an http:// address of this machine
}

// alert shows a message box: Exhale.exe has no console, and its errors
// would otherwise go nowhere.
func alert(title, message string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(message)
	_, _ = windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONERROR)
}

var getUserDefaultLocaleName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")

// systemLanguages is the person's locale, such as es-ES.
func systemLanguages() []string {
	buf := make([]uint16, 85)                                                                                 // LOCALE_NAME_MAX_LENGTH
	if n, _, _ := getUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n > 0 { //nolint:gosec // a buffer the call fills, as its signature asks
		return []string{windows.UTF16ToString(buf)}
	}
	return nil
}
