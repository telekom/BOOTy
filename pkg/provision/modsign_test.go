//go:build linux

package provision

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func moduleSigningConfig(ms config.ModuleSigningConfig) *config.MachineConfig {
	cfg := &config.MachineConfig{Hostname: "host-1"}
	cfg.Provision.SecureBoot.ModuleSigning = ms
	return cfg
}

func TestSignKernelModulesDisabledIsNoop(t *testing.T) {
	o, cmd := newTestOrchestratorWithCommander(t, moduleSigningConfig(config.ModuleSigningConfig{}), &mockProvider{})
	if err := o.signKernelModules(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.enrollModuleSigningKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cmd.calls) != 0 || o.moduleSigning != nil {
		t.Fatalf("disabled module signing ran %v", cmd.calls)
	}
}

func TestSignKernelModules(t *testing.T) {
	o, cmd := newTestOrchestratorWithCommander(t, moduleSigningConfig(config.ModuleSigningConfig{Enabled: true}), &mockProvider{})
	root := o.config.rootDir
	mod := filepath.Join(root, "lib/modules/6.8.0-1/updates/dkms/x.ko")
	if err := os.MkdirAll(filepath.Dir(mod), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mod, []byte("\x7fELF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.signKernelModules(context.Background()); err != nil {
		t.Fatal(err)
	}
	der, err := os.ReadFile(filepath.Join(root, config.DefaultModuleSigningCertDir, moduleCertFile))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil || cert.Subject.CommonName != "BOOTy module signing host-1" {
		t.Fatalf("certificate %v: %v", cert, err)
	}
	if signed, _ := os.ReadFile(mod); len(signed) <= 4 {
		t.Fatal("module not signed")
	}
	for _, want := range [][]string{{"depmod -a 6.8.0-1"}, {initramfsUpdateCmd}} {
		if !hasProvisionCall(cmd.calls, "chroot", root, "/bin/bash", "-c", want[0]) {
			t.Fatalf("missing chroot %q in %v", want[0], cmd.calls)
		}
	}
	if o.moduleSigning == nil || o.moduleSigning.Modules != 1 || o.moduleSigning.Enrollment != "efi-helper" {
		t.Fatalf("report %+v", o.moduleSigning)
	}
}

func TestSignKernelModulesFailsClosed(t *testing.T) {
	for _, allow := range []bool{false, true} {
		o, cmd := newTestOrchestratorWithCommander(t,
			moduleSigningConfig(config.ModuleSigningConfig{Enabled: true, AllowFailure: allow}), &mockProvider{})
		mod := filepath.Join(o.config.rootDir, "lib/modules/6.8.0-1/updates/x.ko")
		if err := os.MkdirAll(filepath.Dir(mod), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mod, []byte("\x7fELF"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd.setResult("chroot "+o.config.rootDir, nil, errors.New("depmod failed"))
		err := o.signKernelModules(context.Background())
		if (err == nil) != allow {
			t.Fatalf("allowFailure=%v: err=%v", allow, err)
		}
	}
}

// The enrollment never falls back to a certificate that the image ships at
// the certificate path (a failed signing with MODULE_SIGNING_ALLOW_FAILURE).
func TestEnrollModuleSigningKeyNeedsCertOfThisRun(t *testing.T) {
	o, _ := newTestOrchestratorWithCommander(t,
		moduleSigningConfig(config.ModuleSigningConfig{Enabled: true, Enrollment: config.ModuleSigningEnrollExternal}), &mockProvider{})
	planted := filepath.Join(o.config.rootDir, config.DefaultModuleSigningCertDir, moduleCertFile)
	if err := os.MkdirAll(filepath.Dir(planted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, []byte("attacker certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.enrollModuleSigningKey(context.Background()); err == nil {
		t.Fatal("enrollment without a certificate of this run succeeded")
	}
}

func TestMOKHelperEntries(t *testing.T) {
	out := "BootCurrent: 0001\nBootOrder: 0001,0000\nBoot0000* ubuntu\tHD(1,GPT)\n" +
		"Boot0003  booty-mok-enroll\tHD(1,GPT)/\\EFI\\BOOTy\\mok-enroll.efi\n" +
		"Boot000A* booty-mok-enroll-x\tHD\n"
	if got := mokHelperEntries(out); !slices.Equal(got, []string{"0003"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestNeedsFirmwareReboot(t *testing.T) {
	for _, tc := range []struct {
		sb   config.SecureBootConfig
		want bool
	}{
		{config.SecureBootConfig{}, false},
		{config.SecureBootConfig{ReEnable: true}, true},
		{config.SecureBootConfig{ModuleSigning: config.ModuleSigningConfig{Enabled: true}}, true},
		{config.SecureBootConfig{ModuleSigning: config.ModuleSigningConfig{Enabled: true, Enrollment: "mokutil"}}, true},
		{config.SecureBootConfig{ModuleSigning: config.ModuleSigningConfig{Enabled: true, Enrollment: "external"}}, false},
	} {
		if got := tc.sb.NeedsFirmwareReboot(); got != tc.want {
			t.Errorf("%+v: got %v", tc.sb, got)
		}
	}
}
