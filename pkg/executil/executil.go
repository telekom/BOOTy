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
	slog.Error("DEBUG", "label", "PATH", "data", pathEnv)

	dirs := filepath.SplitList(pathEnv)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			slog.Error("DEBUG", "label", "PATH dir unreadable", "dir", dir, "error", err)
			continue
		}
		var bins []string
		for _, e := range entries {
			if e.Type().IsRegular() || e.Type()&os.ModeSymlink != 0 {
				bins = append(bins, e.Name())
			}
		}
		sort.Strings(bins)
		slog.Error("DEBUG", "label", "PATH binaries", "dir", dir, "count", len(bins),
			"data", strings.Join(bins, " "))
	}
}
