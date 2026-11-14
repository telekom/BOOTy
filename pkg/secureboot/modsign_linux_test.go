//go:build linux

package secureboot

import (
	"bytes"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

// A write that fails after the first module was re-signed (here a later
// module hits RLIMIT_FSIZE, as on a full disk) must report ErrModulesModified.
func TestSignModuleTreeWriteFailureAfterFirstModule(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "lib/modules/6.8.0-1/updates/a.ko")
	later := filepath.Join(root, "lib/modules/6.9.0-2/updates/z.ko")
	writeFile(t, first, []byte("\x7fELF small"))
	big := append([]byte("\x7fELF"), bytes.Repeat([]byte{0}, 64<<10)...)
	writeFile(t, later, big)

	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatal(err)
	}
	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)
	lim := old
	lim.Cur = uint64(len(big))
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Fatal(err)
	}
	_, err := SignModuleTree(root, "BOOTy test")
	if rerr := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); rerr != nil {
		t.Fatal(rerr)
	}
	if !errors.Is(err, ErrModulesModified) {
		t.Fatalf("err = %v, want ErrModulesModified", err)
	}
	if got, _ := os.ReadFile(first); bytes.Equal(got, []byte("\x7fELF small")) {
		t.Fatal("the first module was not re-signed: the failure is not after the first write")
	}
}
