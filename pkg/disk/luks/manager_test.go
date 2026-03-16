//go:build linux

package luks

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
)

type fakeCommander struct {
	runFn          func(context.Context, string, ...string) ([]byte, error)
	runWithInputFn func(context.Context, string, string, ...string) ([]byte, error)
}

func (f *fakeCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if f.runFn != nil {
		return f.runFn(ctx, name, args...)
	}
	return nil, nil
}

func (f *fakeCommander) RunWithInput(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	if f.runWithInputFn != nil {
		return f.runWithInputFn(ctx, input, name, args...)
	}
	return nil, nil
}

func TestFormatValidatesInputs(t *testing.T) {
	mgr := NewWithCommander(nil, &fakeCommander{})

	if err := mgr.Format(context.Background(), nil, &Config{Passphrase: "x"}); err == nil {
		t.Fatal("expected error for nil target")
	}
	if err := mgr.Format(context.Background(), &Target{Device: "/dev/sda3"}, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	if err := mgr.Format(context.Background(), &Target{}, &Config{Passphrase: "x"}); err == nil {
		t.Fatal("expected error for empty target device")
	}
	if err := mgr.Format(context.Background(), &Target{Device: "/dev/sda3"}, &Config{}); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestOpenValidatesInputs(t *testing.T) {
	mgr := NewWithCommander(nil, &fakeCommander{})

	if err := mgr.Open(context.Background(), nil, "x"); err == nil {
		t.Fatal("expected error for nil target")
	}
	if err := mgr.Open(context.Background(), &Target{Device: "/dev/sda3"}, "x"); err == nil {
		t.Fatal("expected error for missing mapped name")
	}
	if err := mgr.Open(context.Background(), &Target{Device: "/dev/sda3", MappedName: "root_crypt"}, ""); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestIsLUKSWithError(t *testing.T) {
	t.Run("device required", func(t *testing.T) {
		mgr := NewWithCommander(nil, &fakeCommander{})
		if _, err := mgr.IsLUKSWithError(context.Background(), ""); err == nil {
			t.Fatal("expected error for empty device")
		}
	})

	t.Run("exit status one means not luks", func(t *testing.T) {
		cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 1")
		exitErr := cmd.Run()
		if exitErr == nil {
			t.Fatal("expected non-zero exit error")
		}

		mgr := NewWithCommander(nil, &fakeCommander{runFn: func(context.Context, string, ...string) ([]byte, error) {
			return nil, fmt.Errorf("exec cryptsetup: %w", exitErr)
		}})

		ok, err := mgr.IsLUKSWithError(context.Background(), "/dev/sda3")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if ok {
			t.Fatal("expected false for non-luks device")
		}
	})

	t.Run("other command error", func(t *testing.T) {
		mgr := NewWithCommander(nil, &fakeCommander{runFn: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("boom"), fmt.Errorf("exec cryptsetup: %w", context.DeadlineExceeded)
		}})

		if _, err := mgr.IsLUKSWithError(context.Background(), "/dev/sda3"); err == nil {
			t.Fatal("expected wrapped error")
		}
	})
}
