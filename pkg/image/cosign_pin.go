package image

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Trust material pinned at start. A standby command can replace files in
// the ramdisk (a deploy archive swaps /deploy/file-system, provisioner files
// are copied over /), so the cosign keys and trusted roots that the base
// config names are read once, before any command, and every later load of
// such a path uses that copy, also when the file was missing then. Once
// pinned, a path that was not pinned is not read at all. The one-shot modes
// never pin and read the files.
var (
	pinMu      sync.Mutex
	pinned     map[string]pinnedFile // by clean path
	pinnedOnce bool
)

type pinnedFile struct {
	data     []byte
	err      error
	resolved string // the path with its symlinks resolved at pin time
}

// PinCosignFiles reads the files at paths now and makes every later load
// (LoadCosignPublicKeys, LoadCosignKeyless) return this content or read
// error, and fail for any other path. Empty paths are skipped.
func PinCosignFiles(paths ...string) {
	pinMu.Lock()
	defer pinMu.Unlock()
	if pinned == nil {
		pinned = make(map[string]pinnedFile)
	}
	pinnedOnce = true
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if _, ok := pinned[p]; ok {
			continue
		}
		data, err := os.ReadFile(p) //nolint:gosec // configured trust material path in the ramdisk
		pinned[p] = pinnedFile{data: data, err: err, resolved: ResolvePath(p)}
	}
}

// PinnedCosignPaths returns the pinned paths, as configured and with their
// symlinks resolved at pin time, or nil before PinCosignFiles.
func PinnedCosignPaths() []string {
	pinMu.Lock()
	defer pinMu.Unlock()
	out := make([]string, 0, 2*len(pinned))
	for p, f := range pinned {
		out = append(out, p, f.resolved)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ResolvePath returns p, made absolute, with the symlinks of its longest
// existing prefix resolved, so that two names of one file compare equal
// also when the file itself does not exist yet.
func ResolvePath(p string) string {
	// A relative path is resolved against the working directory, so that
	// it compares equal to the absolute destinations of a deploy archive.
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	var rest []string
	for dir := p; ; dir = filepath.Dir(dir) {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			slices.Reverse(rest)
			return filepath.Join(append([]string{r}, rest...)...)
		}
		if dir == filepath.Dir(dir) {
			return p
		}
		rest = append(rest, filepath.Base(dir))
	}
}

// readCosignFile returns the pinned content of path, or reads it when
// nothing was pinned.
func readCosignFile(path string) ([]byte, error) {
	pinMu.Lock()
	f, ok := pinned[filepath.Clean(path)]
	once := pinnedOnce
	pinMu.Unlock()
	switch {
	case ok:
		return f.data, f.err
	case once:
		return nil, fmt.Errorf("%s was not configured when standby started, so its trust material is not pinned", path)
	}
	return os.ReadFile(path) //nolint:gosec,wrapcheck // configured trust material path; the callers wrap
}

// unpinCosignFiles drops the pinned files; tests only.
func unpinCosignFiles() {
	pinMu.Lock()
	pinned, pinnedOnce = nil, false
	pinMu.Unlock()
}
