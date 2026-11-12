//go:build !windows

package update

import "golang.org/x/sys/unix"

// writable asks the system whether this process may create files in dir,
// without creating one: permissions and read-only volumes both say no.
func writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }
