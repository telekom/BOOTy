//go:build linux

package provision

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/secureboot"
)

func moduleSigningConfig(signing string, ms config.ModuleSigningConfig) *config.MachineConfig {
	cfg := &config.MachineConfig{Hostname: "host-1"}
	cfg.Provision.SecureBoot.Signing = signing
	cfg.Provision.SecureBoot.ModuleSigning = ms
	return cfg
}

func TestSignKernelModulesDisabledIsNoop(t *testing.T) {
	o, cmd := newTestOrchestratorWithCommander(t, moduleSigningConfig("", config.ModuleSigningConfig{}), &mockProvider{})
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
	o, cmd := newTestOrchestratorWithCommander(t, moduleSigningConfig(config.SigningPerHost, config.ModuleSigningConfig{}), &mockProvider{})
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
			moduleSigningConfig(config.SigningPerHost, config.ModuleSigningConfig{AllowFailure: allow}), &mockProvider{})
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
		moduleSigningConfig(config.SigningPerHost, config.ModuleSigningConfig{Enrollment: config.ModuleSigningEnrollExternal}), &mockProvider{})
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

// Signing ci holds no key: it signs nothing, reads the CI certificate from
// the target and reports it for enrollment.
func TestSignKernelModulesCI(t *testing.T) {
	cfg := moduleSigningConfig(config.SigningCI, config.ModuleSigningConfig{Enrollment: config.ModuleSigningEnrollExternal})
	cfg.Provision.SecureBoot.MOKCertPath = "/usr/share/ci/mok.der"
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	root := o.config.rootDir
	signed, _ := secureboot.SignModuleTree(t.TempDir(), "CI key")
	mod := filepath.Join(root, "lib/modules/6.8.0-1/updates/x.ko")
	for name, data := range map[string][]byte{mod: []byte("\x7fELF ci-signed"), filepath.Join(root, "usr/share/ci/mok.der"): signed.CertDER} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.signKernelModules(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(mod); string(got) != "\x7fELF ci-signed" || len(cmd.calls) != 0 {
		t.Fatalf("signing ci changed the module or ran %v", cmd.calls)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultModuleSigningCertDir, moduleCertFile)); err == nil {
		t.Fatal("signing ci installed a per-host certificate")
	}
	if ms := o.moduleSigning; ms == nil || ms.Signing != config.SigningCI || ms.Modules != 0 || !bytes.Equal(ms.Cert, signed.CertDER) {
		t.Fatalf("report %+v", o.moduleSigning)
	}
	if err := o.enrollModuleSigningKey(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A FIFO must not block the read.
	fifo := filepath.Join(root, "usr/share/ci/mok.der")
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.signKernelModules(context.Background()); err == nil {
		t.Fatal("a FIFO was accepted as the CI certificate")
	}
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}

	// Without the module-signing-only EKU shim would trust the CI key for
	// boot images too.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CI boot key"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr/share/ci/mok.der"), der, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.signKernelModules(context.Background()); err == nil || o.moduleSigning != nil {
		t.Fatalf("a CI certificate without the module-signing-only EKU was accepted: %v", err)
	}

	// Not a certificate: fails closed.
	if err := os.WriteFile(filepath.Join(root, "usr/share/ci/mok.der"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.signKernelModules(context.Background()); err == nil {
		t.Fatal("a junk CI certificate was accepted")
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
		{config.SecureBootConfig{Signing: config.SigningOff, ModuleSigning: config.ModuleSigningConfig{Enrollment: "mokutil"}}, false},
		{config.SecureBootConfig{Signing: config.SigningCI}, true},
		{config.SecureBootConfig{Signing: config.SigningPerHost, ModuleSigning: config.ModuleSigningConfig{Enrollment: config.ModuleSigningEnrollEFIHelper}}, true},
		{config.SecureBootConfig{Signing: config.SigningPerHost, ModuleSigning: config.ModuleSigningConfig{Enrollment: "mokutil"}}, true},
		{config.SecureBootConfig{Signing: config.SigningPerHost, ModuleSigning: config.ModuleSigningConfig{Enrollment: "external"}}, true},
	} {
		if got := tc.sb.NeedsFirmwareReboot(); got != tc.want {
			t.Errorf("%+v: got %v", tc.sb, got)
		}
	}
}
