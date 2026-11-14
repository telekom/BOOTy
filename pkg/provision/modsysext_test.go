//go:build linux

package provision

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestSignKernelModulesSysext(t *testing.T) {
	var mkfs []string
	old := hostCommandCombinedOutput
	hostCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		mkfs = append([]string{name}, args...)
		return nil, nil
	}
	t.Cleanup(func() { hostCommandCombinedOutput = old })
	o, cmd := newTestOrchestratorWithCommander(t,
		moduleSigningConfig(config.SigningPerHost, config.ModuleSigningConfig{Target: config.ModuleSigningTargetSysext}), &mockProvider{})
	root := o.config.rootDir
	mod := filepath.Join(root, "usr/lib/modules/6.8.0-1/updates/dkms/x.ko")
	for name, data := range map[string]string{mod: "\x7fELF", filepath.Join(root, "usr/lib/os-release"): "NAME=Ubuntu\nID=ubuntu\nVERSION_ID=\"24.04\"\n"} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	if err := o.signKernelModules(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(mod); string(got) != "\x7fELF" {
		t.Fatal("the module in the target root changed")
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("ran %v in the target; /usr must stay unchanged", cmd.calls)
	}
	if len(mkfs) == 0 || mkfs[0] != "mkfs.ext4" {
		t.Fatalf("mkfs = %v", mkfs)
	}
	ext := filepath.Join(root, "var/lib/extensions/booty-modules")
	for _, suffix := range []string{".raw", ".verity", ".roothash.p7s"} {
		if info, err := os.Stat(ext + suffix); err != nil || info.Size() == 0 {
			t.Fatalf("%s: %v", suffix, err)
		}
	}
	hash, _ := os.ReadFile(ext + ".roothash")
	if !regexp.MustCompile(`^[0-9a-f]{64}\n$`).Match(hash) || o.moduleSigning.RootHash+"\n" != string(hash) {
		t.Fatalf("roothash %q, report %q", hash, o.moduleSigning.RootHash)
	}
	// The staged tree (gone after the step) is checked by the mkfs
	// arguments: the directory came before the image.
	if dropIn, _ := os.ReadFile(filepath.Join(root, moduleSysextDropIn)); !bytes.Contains(dropIn, []byte("--image-policy=root=signed")) {
		t.Fatalf("drop-in %q", dropIn)
	}
	if link, err := os.Readlink(filepath.Join(root, moduleSysextWants)); err != nil || !strings.HasSuffix(link, "systemd-sysext.service") {
		t.Fatalf("systemd-sysext not enabled: %q %v", link, err)
	}
	if o.moduleSigning.Modules != 1 || o.moduleSigning.Target != "sysext" || len(o.moduleSigning.Cert) == 0 {
		t.Fatalf("report %+v", o.moduleSigning)
	}
}

func TestSignKernelModulesSysextNeedsMergedUsr(t *testing.T) {
	o, _ := newTestOrchestratorWithCommander(t,
		moduleSigningConfig(config.SigningPerHost, config.ModuleSigningConfig{Target: config.ModuleSigningTargetSysext}), &mockProvider{})
	if err := os.MkdirAll(filepath.Join(o.config.rootDir, "lib/modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := o.signKernelModules(context.Background()); err == nil || !strings.Contains(err.Error(), "merged /usr") {
		t.Fatalf("err = %v", err)
	}
}
