package rescue

import (
	"context"
	"os"
	"testing"
)

func TestSetupSSHKeys_WritesKeys(t *testing.T) {
	if os.Getuid() != 0 {
		// Not root — expect permission error writing to /root/.ssh.
		keys := []string{"ssh-rsa AAAA... user@host"}
		err := setupSSHKeys(keys)
		if err == nil {
			t.Error("expected permission error when not running as root")
		}
		return
	}
	// Running as root — function should succeed.
	keys := []string{"ssh-rsa AAAA... user@host", "ssh-ed25519 BBBB... user2@host"}
	if err := setupSSHKeys(keys); err != nil {
		t.Errorf("setupSSHKeys() = %v", err)
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
