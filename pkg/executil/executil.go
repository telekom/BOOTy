package executil

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Commander abstracts command execution for testing.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecCommander executes real system commands via os/exec.
type ExecCommander struct{}

// maxOutputLen caps diagnostic output included in error messages.
const maxOutputLen = 1024

// sanitize replaces control characters with spaces to keep error messages
// on a single line in structured logs.
var sanitizer = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

// Run executes a system command and returns its combined output.
// On failure the error includes the sanitized, truncated raw command output
// and the resolved PATH so that missing-binary issues are immediately
// diagnosable. Newlines in the output are replaced with spaces to keep
// structured log values single-line.
func (e *ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		raw := sanitizer.Replace(strings.TrimSpace(string(out)))
		if len(raw) > maxOutputLen {
			raw = raw[:maxOutputLen] + "...(truncated)"
		}
		pathEnv := os.Getenv("PATH")
		if raw != "" {
			return out, fmt.Errorf("exec %s: %w [output: %s] [PATH: %s]", name, err, raw, pathEnv)
		}
		return out, fmt.Errorf("exec %s: %w [PATH: %s]", name, err, pathEnv)
	}
	return out, nil
}

// DumpPATH logs the current PATH and lists all executable files found in each
// directory. Call this in debug dumps so that missing-binary problems are
// obvious from the logs alone.
func DumpPATH() {
	pathEnv := os.Getenv("PATH")
	slog.Warn("debug dump", "label", "PATH", "data", pathEnv)

	const maxBinsPerDir = 200
	for _, dir := range filepath.SplitList(pathEnv) {
		bins, err := listExecutables(dir)
		if err != nil {
			slog.Warn("debug dump", "label", "PATH dir unreadable", "dir", dir, "error", err)
			continue
		}
		display := bins
		truncated := false
		if len(bins) > maxBinsPerDir {
			display = bins[:maxBinsPerDir]
			truncated = true
		}
		data := strings.Join(display, " ")
		if truncated {
			data += fmt.Sprintf(" ...(%d more)", len(bins)-maxBinsPerDir)
		}
		slog.Warn("debug dump", "label", "PATH binaries", "dir", dir, "count", len(bins),
			"data", data)
	}
}

// listExecutables returns sorted names of executable files in dir.
func listExecutables(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var bins []string
	for _, e := range entries {
		if isExecutable(dir, e) {
			bins = append(bins, e.Name())
		}
	}
	sort.Strings(bins)
	return bins, nil
}

// isExecutable checks if a directory entry is an executable file.
func isExecutable(dir string, e os.DirEntry) bool {
	if e.Type()&os.ModeSymlink != 0 {
		fi, err := os.Stat(filepath.Join(dir, e.Name())) //nolint:gosec // intentional PATH directory traversal for diagnostics
		if err != nil {
			return false
		}
		return fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
	}
	if !e.Type().IsRegular() {
		return false
	}
	fi, err := e.Info()
	if err != nil {
		return false
	}
	return fi.Mode()&0o111 != 0
}
