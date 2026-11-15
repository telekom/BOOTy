package rescue

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points rootSSHDir at a temporary directory, so no test reads or
// removes the authorized_keys of the machine that runs the tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rescue-test")
	if err != nil {
		panic(err)
	}
	rootSSHDir = filepath.Join(dir, ".ssh")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
