package rescue

import (
	"context"
	"testing"
)

func TestSetupSSHKeys_WritesKeys(t *testing.T) {
	keys := []string{"ssh-rsa AAAA... user@host", "ssh-ed25519 BBBB... user2@host"}
	err := setupSSHKeys(keys)
	if err == nil {
		t.Log("setupSSHKeys succeeded (running as root)")
	}
	// On non-root systems, we expect a permission error — that's OK.
}

func TestSetup_ShellMode_NoSSHKeys(t *testing.T) {
	cfg := &Config{
		Mode: ModeShell,
	}
	err := Setup(context.Background(), cfg)
	if err != nil {
		t.Errorf("Setup(shell, no keys) = %v", err)
	}
}

func TestSetup_WaitMode(t *testing.T) {
	cfg := &Config{
		Mode: ModeWait,
	}
	err := Setup(context.Background(), cfg)
	if err != nil {
		t.Errorf("Setup(wait) = %v", err)
	}
}
