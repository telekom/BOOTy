package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	return string(data)
}

func TestBootyVersionCommandOutput(t *testing.T) {
	orig := Release
	defer func() { Release = orig }()

	Release.Version = "v1.2.3"
	Release.Build = "abc123"

	out := captureStdout(t, func() {
		bootyVersion.Run(bootyVersion, nil)
	})

	if !strings.Contains(out, "BOOTy Release Information") {
		t.Fatalf("missing release header in output: %q", out)
	}
	if !strings.Contains(out, "Version:  v1.2.3") {
		t.Fatalf("missing version in output: %q", out)
	}
	if !strings.Contains(out, "Build:    abc123") {
		t.Fatalf("missing build in output: %q", out)
	}
}

func TestBootySubcommands(t *testing.T) {
	want := []string{"version", "provision", "deprovision", "standby", "check", "validate"}
	cmds := bootyCmd.Commands()
	names := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		names[c.Name()] = true
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestConfigFlagRegistered(t *testing.T) {
	f := bootyCmd.PersistentFlags().Lookup("config")
	if f == nil {
		t.Fatal("--config persistent flag not registered")
	}
	if f.Shorthand != "c" {
		t.Errorf("config shorthand = %q, want \"c\"", f.Shorthand)
	}
}

func TestValidateRequiresConfig(t *testing.T) {
	old := configPath
	defer func() { configPath = old }()
	configPath = ""

	err := validateCmd.RunE(validateCmd, nil)
	if err == nil {
		t.Fatal("expected error when --config is empty")
	}
	if !strings.Contains(err.Error(), "--config") {
		t.Fatalf("error should mention --config: %v", err)
	}
}

func TestValidateWithValidConfig(t *testing.T) {
	old := configPath
	defer func() { configPath = old }()

	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hostname: test-node\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	configPath = f.Name()
	err = validateCmd.RunE(validateCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWithInvalidConfig(t *testing.T) {
	old := configPath
	defer func() { configPath = old }()

	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hostname: test\nmode: invalid-bogus\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	configPath = f.Name()
	err = validateCmd.RunE(validateCmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestValidateStrictRejectsUnknownFields(t *testing.T) {
	old := configPath
	oldStrict := validateStrict
	defer func() {
		configPath = old
		validateStrict = oldStrict
	}()

	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hostname: test-node\nunknownFieldXYZ: bad\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	configPath = f.Name()
	validateStrict = true
	err = validateCmd.RunE(validateCmd, nil)
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode")
	}
	if !strings.Contains(err.Error(), "unknownFieldXYZ") {
		t.Fatalf("expected unknown field name in error: %v", err)
	}
}

func TestValidateStrictAcceptsKnownFields(t *testing.T) {
	old := configPath
	oldStrict := validateStrict
	defer func() {
		configPath = old
		validateStrict = oldStrict
	}()

	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hostname: test-node\nmode: provision\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	configPath = f.Name()
	validateStrict = true
	if err := validateCmd.RunE(validateCmd, nil); err != nil {
		t.Fatalf("unexpected error in strict mode with valid fields: %v", err)
	}
}

func TestProvisionRequiresConfig(t *testing.T) {
	old := configPath
	defer func() { configPath = old }()
	configPath = ""

	err := provisionCmd.RunE(provisionCmd, nil)
	if err == nil {
		t.Fatal("expected error when --config is empty")
	}
}

func TestDeprovisionSoftFlag(t *testing.T) {
	f := deprovisionCmd.Flags().Lookup("soft")
	if f == nil {
		t.Fatal("--soft flag not registered on deprovision")
	}
}
