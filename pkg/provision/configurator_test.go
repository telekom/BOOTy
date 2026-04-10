//go:build linux

package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestEfiLoaderNames(t *testing.T) {
	tests := []struct {
		name     string
		arch     string
		wantShim string
		wantGrub string
	}{
		{"amd64", "amd64", "shimx64.efi", "grubx64.efi"},
		{"arm64", "arm64", "shimaa64.efi", "grubaa64.efi"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shim, grub, err := efiLoaderNames(tc.arch)
			if err != nil {
				t.Fatalf("efiLoaderNames(%q): %v", tc.arch, err)
			}
			if shim != tc.wantShim {
				t.Errorf("shim = %q, want %q", shim, tc.wantShim)
			}
			if grub != tc.wantGrub {
				t.Errorf("grub = %q, want %q", grub, tc.wantGrub)
			}
		})
	}
}

func TestEfiLoaderNamesUnsupported(t *testing.T) {
	_, _, err := efiLoaderNames("s390x")
	if err == nil {
		t.Fatal("expected error for unsupported architecture")
	}
	if !strings.Contains(err.Error(), "unsupported architecture") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEfiLoaderPath(t *testing.T) {
	root := t.TempDir()
	efiDir := filepath.Join(root, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No shim but grub present -> falls back to grub.
	if err := os.WriteFile(filepath.Join(efiDir, "grubx64.efi"), []byte("grub"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader, err := efiLoaderPath(root, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	wantGrub := "\\EFI\\ubuntu\\grubx64.efi"
	if loader != wantGrub {
		t.Errorf("got %q, want grub fallback %q", loader, wantGrub)
	}

	// Create shim -> should prefer shim.
	if err := os.WriteFile(filepath.Join(efiDir, "shimx64.efi"), []byte("shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader, err = efiLoaderPath(root, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	wantShim := "\\EFI\\ubuntu\\shimx64.efi"
	if loader != wantShim {
		t.Errorf("got %q, want shim %q", loader, wantShim)
	}

	// ARM64 without shim but with grub -> grub fallback.
	if err := os.WriteFile(filepath.Join(efiDir, "grubaa64.efi"), []byte("grub-arm64"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader, err = efiLoaderPath(root, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	wantArm := "\\EFI\\ubuntu\\grubaa64.efi"
	if loader != wantArm {
		t.Errorf("got %q, want arm64 grub fallback %q", loader, wantArm)
	}

	// ARM64 with shim -> should prefer shimaa64.
	if err := os.WriteFile(filepath.Join(efiDir, "shimaa64.efi"), []byte("shim-arm64"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader, err = efiLoaderPath(root, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	wantArmShim := "\\EFI\\ubuntu\\shimaa64.efi"
	if loader != wantArmShim {
		t.Errorf("got %q, want arm64 shim %q", loader, wantArmShim)
	}
}

func TestEfiLoaderPath_MissingLoaders(t *testing.T) {
	root := t.TempDir()
	efiDir := filepath.Join(root, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := efiLoaderPath(root, "amd64")
	if err == nil {
		t.Fatal("expected error when both shim and grub are missing")
	}
	if !strings.Contains(err.Error(), "no EFI loader found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRedactCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "password equals",
			input: "setup --password=s3cr3t --user=admin",
			want:  "setup --password=[REDACTED] --user=admin",
		},
		{
			name:  "token colon",
			input: "curl -H token:myBearerXYZ http://example.com",
			want:  "curl -H token:[REDACTED] http://example.com",
		},
		{
			name:  "secret uppercase",
			input: "configure SECRET=abc123",
			want:  "configure SECRET=[REDACTED]",
		},
		{
			name:  "key equals",
			input: "deploy key=opensesame region=us-east-1",
			want:  "deploy key=[REDACTED] region=us-east-1",
		},
		{
			name:  "credential equals",
			input: "connect credential=user:pass@host",
			want:  "connect credential=[REDACTED]",
		},
		{
			name:  "auth equals",
			input: "login auth=Bearer_token123",
			want:  "login auth=[REDACTED]",
		},
		{
			name:  "no sensitive data",
			input: "apt-get install -y curl",
			want:  "apt-get install -y curl",
		},
		{
			name:  "multiple sensitive keys",
			input: "setup password=abc token=xyz region=eu",
			want:  "setup password=[REDACTED] token=[REDACTED] region=eu",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "spaces around equals",
			input: "run password = secret123 --verbose",
			want:  "run password =[REDACTED] --verbose",
		},
		{
			name:  "double-dash password space",
			input: "--password secret --verbose",
			want:  "--password [REDACTED] --verbose",
		},
		{
			name:  "double-dash token space",
			input: "--token mytoken",
			want:  "--token [REDACTED]",
		},
		{
			name:  "single-dash password space",
			input: "-password s3cr3t",
			want:  "-password [REDACTED]",
		},
		{
			name:  "double-quoted value with spaces",
			input: `--password "very secret" --verbose`,
			want:  `--password [REDACTED] --verbose`,
		},
		{
			name:  "single-quoted value with spaces",
			input: "--password 'secret value'",
			want:  "--password [REDACTED]",
		},
		{
			name:  "tab-delimited flag value",
			input: "--password\tsecret",
			want:  "--password [REDACTED]",
		},
		{
			name:  "assignment with double-quoted spaced value",
			input: `password="secret with space"`,
			want:  `password=[REDACTED]`,
		},
		{
			name:  "flag value containing colon",
			input: "--password abc:def --verbose",
			want:  "--password [REDACTED] --verbose",
		},
		{
			name:  "flag value containing equals",
			input: "--token foo=bar",
			want:  "--token [REDACTED]",
		},
		{
			name:  "flag quoted value containing colon",
			input: `--password "abc:def"`,
			want:  `--password [REDACTED]`,
		},
		{
			name:  "no redaction for substring match in key",
			input: "monkey=banana",
			want:  "monkey=banana",
		},
		{
			name:  "no redaction for monkey prefix",
			input: "cmd --monkey=banana",
			want:  "cmd --monkey=banana",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactCommand(tc.input)
			if got != tc.want {
				t.Errorf("redactCommand(%q)\n got  %q\n want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestConfigureDNSEmptyResolvers(t *testing.T) {
	t.Helper()
	c := &Configurator{rootDir: t.TempDir()}
	cfg := &config.MachineConfig{DNSResolvers: ""}
	if err := c.ConfigureDNS(cfg); err != nil {
		t.Fatalf("expected nil for empty resolvers, got: %v", err)
	}
}

func TestConfigureDNSSuccess(t *testing.T) {
	root := t.TempDir()
	etcDir := filepath.Join(root, "etc")
	if err := os.MkdirAll(etcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Configurator{rootDir: root}
	cfg := &config.MachineConfig{DNSResolvers: "8.8.8.8, 1.1.1.1"}
	if err := c.ConfigureDNS(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(etcDir, "resolv.conf"))
	if err != nil {
		t.Fatalf("cannot read resolv.conf: %v", err)
	}
	if !strings.Contains(string(data), "nameserver 8.8.8.8") {
		t.Errorf("missing nameserver 8.8.8.8 in %s", data)
	}
	if !strings.Contains(string(data), "nameserver 1.1.1.1") {
		t.Errorf("missing nameserver 1.1.1.1 in %s", data)
	}
}

func TestConfigureDNSMissingEtcDir(t *testing.T) {
	root := t.TempDir()
	// Don't create /etc — ConfigureDNS should skip gracefully.
	c := &Configurator{rootDir: root}
	cfg := &config.MachineConfig{DNSResolvers: "8.8.8.8"}
	if err := c.ConfigureDNS(cfg); err != nil {
		t.Fatalf("expected nil when etc/ doesn't exist, got: %v", err)
	}
}

func TestCopyTreeCancelledBeforeStart(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := copyTree(ctx, src, dst)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestCopyFileCancelledBeforeStart(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	srcFile := filepath.Join(src, "file.txt")
	if err := os.WriteFile(srcFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := copyFile(ctx, srcFile, filepath.Join(dst, "file.txt"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
