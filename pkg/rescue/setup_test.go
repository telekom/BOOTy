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

// An authorized_keys file that a deploy archive put into the ramdisk must
// not grant a login when the trusted configuration has no valid key.
func TestSetupSSHKeys_RemovesStaleKeysWithoutConfiguredKeys(t *testing.T) {
	for _, configured := range []string{"", "not-a-key;ssh-ed25519 AAAAC3NzaC1lZDI1NTE5"} {
		path := filepath.Join(rootSSHDir, "authorized_keys")
		if err := os.MkdirAll(rootSSHDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(testKeyA+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		keys, _ := ParseSSHKeys(configured)
		if err := setupSSHKeys(keys); err != nil {
			t.Fatalf("setupSSHKeys(%q) = %v", configured, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("configured %q: authorized_keys still present (stat err %v)", configured, err)
		}
	}
}

func TestSetupSSHKeys_RemovalFailureAbortsSSH(t *testing.T) {
	// A directory in place of the file cannot be removed with os.Remove
	// when it is not empty.
	path := filepath.Join(rootSSHDir, "authorized_keys")
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	if err := setupSSHKeys(nil); err == nil {
		t.Fatal("setupSSHKeys(nil) = nil, want an error")
	}
	t.Setenv("PATH", t.TempDir())
	if err := Setup(context.Background(), &Config{Mode: ModeShell}); err == nil {
		t.Fatal("Setup() = nil, want the removal error")
	}
}
