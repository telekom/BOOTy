package image

import (
	"os"
	"path/filepath"
	"sync"
)

// Trust material pinned at start. A standby command can replace files in
// the ramdisk (a deploy archive swaps /deploy/file-system, provisioner files
// are copied over /), so the cosign keys and trusted roots that the base
// config names are read once, before any command, and every later load of
// such a path uses that copy, also when the file was missing then.
var (
	pinMu  sync.Mutex
	pinned map[string]pinnedFile
)

type pinnedFile struct {
	data []byte
	err  error
}

// PinCosignFiles reads the files at paths now and makes every later load
// of these paths (LoadCosignPublicKeys, LoadCosignKeyless) return this
// content or read error. Empty paths are skipped.
func PinCosignFiles(paths ...string) {
	pinMu.Lock()
	defer pinMu.Unlock()
	if pinned == nil {
		pinned = make(map[string]pinnedFile)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if _, ok := pinned[p]; ok {
			continue
		}
		data, err := os.ReadFile(p) //nolint:gosec // configured trust material path in the ramdisk
		pinned[p] = pinnedFile{data, err}
	}
}

// readCosignFile returns the pinned content of path, or reads it.
func readCosignFile(path string) ([]byte, error) {
	pinMu.Lock()
	f, ok := pinned[filepath.Clean(path)]
	pinMu.Unlock()
	if ok {
		return f.data, f.err
	}
	return os.ReadFile(path) //nolint:gosec,wrapcheck // configured trust material path; the callers wrap
}

// unpinCosignFiles drops the pinned files; tests only.
func unpinCosignFiles() {
	pinMu.Lock()
	pinned = nil
	pinMu.Unlock()
}
