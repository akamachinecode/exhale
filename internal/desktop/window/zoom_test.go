//go:build desktop

package window

import "testing"

func TestMacTitlebarAction(t *testing.T) {
	for pref, want := range map[string]titlebarAction{
		"":         titlebarZoom,
		"Fill":     titlebarZoom,
		"Maximize": titlebarZoom,
		"Minimize": titlebarMinimise,
		"None":     titlebarNothing,
	} {
		if got := macTitlebarAction(pref); got != want {
			t.Errorf("macTitlebarAction(%q) = %d, want %d", pref, got, want)
		}
	}
}
