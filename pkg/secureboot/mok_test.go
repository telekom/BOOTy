//go:build linux

package secureboot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func fakeMokutil(t *testing.T, exitCode int, output string) func() {
	t.Helper()
	orig := mokutilCommand
	mokutilCommand = func(ctx context.Context, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "GO_HELPER=1",
			"HELPER_EXIT="+strconv.Itoa(exitCode), "HELPER_OUT="+output)
		return cmd
	}
	return func() { mokutilCommand = orig }
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER") != "1" {
		return
	}
	os.Stdout.WriteString(os.Getenv("HELPER_OUT"))
	code, _ := strconv.Atoi(os.Getenv("HELPER_EXIT"))
	os.Exit(code)
}

func writeCert(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mok.der")
	if err := os.WriteFile(p, []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnroll_MissingCertPath(t *testing.T) {
	if err := NewMOKEnroller("", "pw").Enroll(context.Background()); err == nil {
		t.Fatal("expected error for empty cert path")
	}
}

func TestEnroll_CertNotFound(t *testing.T) {
	if err := NewMOKEnroller("/nope/x.der", "pw").Enroll(context.Background()); err == nil {
		t.Fatal("expected error for missing cert file")
	}
}

func TestEnroll_Success(t *testing.T) {
	defer fakeMokutil(t, 0, "")()
	if err := NewMOKEnroller(writeCert(t), "pw").Enroll(context.Background()); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestEnroll_MokutilFails(t *testing.T) {
	defer fakeMokutil(t, 1, "import error")()
	if err := NewMOKEnroller(writeCert(t), "pw").Enroll(context.Background()); err == nil {
		t.Fatal("expected error when mokutil fails")
	}
}

func TestIsEnrolled_True(t *testing.T) {
	defer fakeMokutil(t, 0, "is enrolled")()
	ok, err := NewMOKEnroller(writeCert(t), "").IsEnrolled(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected enrolled, got ok=%v err=%v", ok, err)
	}
}

func TestIsEnrolled_NotEnrolled(t *testing.T) {
	defer fakeMokutil(t, 1, "is not enrolled")()
	ok, err := NewMOKEnroller(writeCert(t), "").IsEnrolled(context.Background())
	if err != nil || ok {
		t.Fatalf("expected not-enrolled false/nil, got ok=%v err=%v", ok, err)
	}
}

func TestIsEnrolled_Error(t *testing.T) {
	defer fakeMokutil(t, 1, "permission denied")()
	if _, err := NewMOKEnroller(writeCert(t), "").IsEnrolled(context.Background()); err == nil {
		t.Fatal("expected error on unexpected mokutil failure")
	}
}
