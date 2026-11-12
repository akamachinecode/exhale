//go:build darwin || windows

package awake

import (
	"os"
	"path/filepath"
)

// marker is the file that says a lid hold may still be in place.
type marker struct{ path string }

func newMarker(dir string) marker {
	if dir == "" {
		dir = os.TempDir()
	}
	return marker{filepath.Join(dir, "keep-awake-lid")}
}

func (m marker) write(content []byte) error { return os.WriteFile(m.path, content, 0o600) }

func (m marker) read() ([]byte, bool) {
	b, err := os.ReadFile(m.path)
	return b, err == nil
}

func (m marker) remove() { _ = os.Remove(m.path) }
