package rescue

import (
	"context"
	"os"
	"testing"
)

func TestSetupSSHKeys_WritesKeys(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: running as root would write to real /root/.ssh/authorized_keys")
	}
	// Not root — expect permission error writing to /root/.ssh.
	keys := []string{"ssh-rsa AAAA... user@host"}
	err := setupSSHKeys(keys)
	if err == nil {
		t.Error("expected permission error when not running as root")
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
