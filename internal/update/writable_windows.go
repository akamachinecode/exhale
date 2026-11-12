package update

import "os"

// writable tries to create a file in dir: Windows has no access(2) that
// knows about ACLs.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".exhale-update-*")
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return true
}
