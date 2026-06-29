package executil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestExecCommanderSuccess(t *testing.T) {
	cmd := &ExecCommander{}
	out, err := cmd.Run(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}

func TestExecCommanderFailure(t *testing.T) {
	cmd := &ExecCommander{}
	_, err := cmd.Run(context.Background(), "false")
	if err == nil {
		t.Fatal("expected error from false")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "exec false") {
		t.Fatalf("error should mention command name, got: %s", errStr)
	}
	if strings.Contains(errStr, "[PATH:") {
		t.Fatalf("error must not include [PATH: ...] annotation, got: %s", errStr)
	}
}

func TestExecCommanderNotFound(t *testing.T) {
	cmd := &ExecCommander{}
	_, err := cmd.Run(context.Background(), "nonexistent-binary-xyz")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if strings.Contains(err.Error(), "[PATH:") {
		t.Fatalf("error must not include [PATH: ...] annotation, got: %s", err.Error())
	}
}

func TestExecCommanderOutputInError(t *testing.T) {
	cmd := &ExecCommander{}
	_, err := cmd.Run(context.Background(), "sh", "-c", "echo some-output; exit 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "some-output") {
		t.Fatalf("error should include command output, got: %s", err.Error())
	}
}

func TestExecCommanderSanitizesNewlines(t *testing.T) {
	cmd := &ExecCommander{}
	_, err := cmd.Run(context.Background(), "sh", "-c", "printf 'line1\nline2\nline3'; exit 1")
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	if strings.Contains(errStr, "\n") {
		t.Fatalf("error should not contain newlines, got: %s", errStr)
	}
	if !strings.Contains(errStr, "line1 line2 line3") {
		t.Fatalf("newlines should be replaced with spaces, got: %s", errStr)
	}
}

func TestCommandContextRegistersManagedChildUntilWait(t *testing.T) {
	cmd := CommandContext(context.Background(), "sh", "-c", "sleep 60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := cmd.Process.Pid
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	if !IsManagedChild(pid) {
		t.Fatalf("pid %d is not registered as managed after Start", pid)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("Wait() error = nil, want killed process error")
	}
	waited = true
	if IsManagedChild(pid) {
		t.Fatalf("pid %d is still registered as managed after Wait", pid)
	}
}

func TestCommandContextOutputCapturesExitErrorStderr(t *testing.T) {
	cmd := CommandContext(context.Background(), "sh", "-c", "printf stdout; printf stderr >&2; exit 7")
	out, err := cmd.Output()
	if err == nil {
		t.Fatal("Output() error = nil, want exit error")
	}
	if string(out) != "stdout" {
		t.Fatalf("Output() stdout = %q, want stdout", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Output() error = %T, want wrapped *exec.ExitError", err)
	}
	if string(exitErr.Stderr) != "stderr" {
		t.Fatalf("ExitError.Stderr = %q, want stderr", exitErr.Stderr)
	}
}

func TestDumpPATH(t *testing.T) {
	old := os.Getenv("PATH")
	defer func() { _ = os.Setenv("PATH", old) }()
	_ = os.Setenv("PATH", "/usr/bin:/nonexistent-dir-xyz")
	DumpPATH()
}
