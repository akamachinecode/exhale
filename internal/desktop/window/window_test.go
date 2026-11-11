//go:build desktop

package window

import (
	"net/url"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestTrusted(t *testing.T) {
	base, _ := url.Parse("http://127.0.0.1:8484")
	b := &bridge{base: base}
	for _, c := range []struct {
		origin string
		want   bool
	}{
		{"http://127.0.0.1:8484", true},
		{"http://127.0.0.1:8484/", true},
		{"http://127.0.0.1:8484/settings?tab=data", true}, // macOS gives the frame's whole address
		{"http://127.0.0.1:8485", false},
		{"https://127.0.0.1:8484", false},
		{"https://accounts.example.com/authorize", false}, // a sign-in provider shown in the window
		{"", false},
	} {
		if got := b.trusted(&application.OriginInfo{Origin: c.origin}); got != c.want {
			t.Errorf("trusted(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
	if b.trusted(nil) {
		t.Error("a message without an origin was trusted")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"exhale-export.json":         "exhale-export.json",
		"../../.ssh/authorized_keys": "authorized_keys",
		`..\..\Windows\evil.exe`:     "evil.exe",
		"/etc/passwd":                "passwd",
		"":                           "exhale-download",
		"/":                          "exhale-download",
		".":                          "exhale-download",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
