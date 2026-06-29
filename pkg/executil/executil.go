package executil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Commander abstracts command execution for testing.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecCommander executes real system commands via os/exec.
type ExecCommander struct{}

// maxOutputLen caps diagnostic output included in error messages.
const maxOutputLen = 1024

var managedChildren sync.Map

// Cmd wraps os/exec.Cmd and records started child PIDs until Wait completes.
// PID 1 uses this registry to avoid stealing exit statuses from normal
// os/exec owners while still reaping unmanaged adopted daemon children.
type Cmd struct {
	*exec.Cmd
	managedPID int
}

// ExitError aliases os/exec.ExitError for callers using managed commands.
type ExitError = exec.ExitError

// ErrNotFound aliases os/exec.ErrNotFound for callers using managed commands.
var ErrNotFound = exec.ErrNotFound

// CommandContext creates a managed command.
func CommandContext(ctx context.Context, name string, args ...string) *Cmd {
	return &Cmd{Cmd: exec.CommandContext(ctx, name, args...)}
}

// LookPath resolves a binary path using os/exec.
func LookPath(file string) (string, error) {
	path, err := exec.LookPath(file)
	if err != nil {
		return "", fmt.Errorf("look up %s: %w", file, err)
	}
	return path, nil
}

// RegisterManagedChild marks pid as owned by an exec.Cmd Wait caller.
func RegisterManagedChild(pid int) {
	if pid > 0 {
		managedChildren.Store(pid, struct{}{})
	}
}

// UnregisterManagedChild removes pid from the exec-owned child set.
func UnregisterManagedChild(pid int) {
	if pid > 0 {
		managedChildren.Delete(pid)
	}
}

// IsManagedChild reports whether pid is currently owned by an exec.Cmd Wait caller.
func IsManagedChild(pid int) bool {
	_, ok := managedChildren.Load(pid)
	return ok
}

// Start starts the command and registers its child PID for PID 1 reaping.
func (c *Cmd) Start() error {
	if err := c.Cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.Path, err)
	}
	if c.Process != nil {
		c.managedPID = c.Process.Pid
		RegisterManagedChild(c.managedPID)
	}
	return nil
}

// Wait waits for the command and releases its managed child PID registration.
func (c *Cmd) Wait() error {
	err := c.Cmd.Wait()
	UnregisterManagedChild(c.managedPID)
	c.managedPID = 0
	if err != nil {
		return fmt.Errorf("wait for %s: %w", c.Path, err)
	}
	return nil
}

// Run starts the command and waits for completion.
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// CombinedOutput runs the command and returns combined stdout and stderr.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run()
	return b.Bytes(), err
}

// Output runs the command and returns stdout.
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	var stderr bytes.Buffer
	captureStderr := c.Stderr == nil
	if captureStderr {
		c.Stderr = &stderr
	}
	err := c.Run()
	if err != nil && captureStderr {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitErr.Stderr = stderr.Bytes()
		}
	}
	return b.Bytes(), err
}

// sanitize replaces control characters with spaces to keep error messages
// on a single line in structured logs.
var sanitizer = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

// Run executes a system command and returns its combined output.
// On failure the error includes the sanitized, truncated raw command output.
// Newlines in the output are replaced with spaces to keep structured log
// values single-line. The resolved PATH is intentionally excluded from the
// returned error to avoid leaking environment layout into logs; call
// DumpPATH() explicitly during debug sessions when binary-not-found issues
// need to be diagnosed.
func (e *ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		raw := sanitizer.Replace(strings.TrimSpace(string(out)))
		if len(raw) > maxOutputLen {
			raw = raw[:maxOutputLen] + "...(truncated)"
		}
		if raw != "" {
			return out, fmt.Errorf("exec %s: %w [output: %s]", name, err, raw)
		}
		return out, fmt.Errorf("exec %s: %w", name, err)
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
