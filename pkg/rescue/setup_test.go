package rescue

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupSSHKeys_WritesKeys(t *testing.T) {
	oldRootSSHDir := rootSSHDir
	rootSSHDir = filepath.Join(t.TempDir(), ".ssh")
	t.Cleanup(func() { rootSSHDir = oldRootSSHDir })

	keys := []string{"ssh-rsa AAAA... user@host"}
	if err := setupSSHKeys(keys); err != nil {
		t.Fatalf("setupSSHKeys() = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(rootSSHDir, "authorized_keys"))
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if string(got) != "ssh-rsa AAAA... user@host\n" {
		t.Fatalf("authorized_keys = %q", string(got))
	}
}

func TestSetup_ShellMode_NoSSHKeys(t *testing.T) {
	// Constrain PATH so dropbear is never found — prevents starting real daemons.
	t.Setenv("PATH", t.TempDir())
	cfg := &Config{
		Mode: ModeShell,
	}
	err := Setup(context.Background(), cfg)
	if err != nil {
		t.Errorf("Setup(shell, no keys) = %v", err)
	}
}

func TestSetup_WaitMode(t *testing.T) {
	// Constrain PATH so dropbear is never found — prevents starting real daemons.
	t.Setenv("PATH", t.TempDir())
	cfg := &Config{
		Mode: ModeWait,
	}
	err := Setup(context.Background(), cfg)
	if err != nil {
		t.Errorf("Setup(wait) = %v", err)
	}
}
