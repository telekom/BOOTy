//go:build e2e && linux

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/provision"
)

func TestOverlayFSVarsToProvisionArtifactE2E(t *testing.T) {
	input := `OS_FAMILY="Ubuntu"
OVERLAYFS_ENABLED="true"
OVERLAYFS_MODE="device"
OVERLAYFS_DEVICE="LABEL=BOOTY-OVERLAY"
OVERLAYFS_DIRECTORY="booty"
OVERLAYFS_UPPER_DIR="/var/lib/booty/.../upper"
OVERLAYFS_WORK_DIR="/var/lib/booty/.../work"
OVERLAYFS_TIMEOUT_SEC="10"
`
	cfg, err := caprf.ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseVars() error: %v", err)
	}

	root := t.TempDir()
	c := provision.NewConfigurator(disk.NewManager(newMockCommander()))
	c.SetRootDir(root)
	if err := c.ConfigureOverlayFS(&cfg.Provision.OverlayFS, cfg.OSFamily); err != nil {
		t.Fatalf("ConfigureOverlayFS() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "etc", "overlayroot.local.conf"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`overlayroot_cfgdisk="disabled"`,
		`overlayroot="device:dev=LABEL=BOOTY-OVERLAY,timeout=10,swap=0,recurse=0,dir=booty"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("overlayroot config missing %q: %q", want, content)
		}
	}

	for _, rel := range []string{
		"var/lib/booty/.../upper",
		"var/lib/booty/.../work",
	} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("expected overlayFS directory %s: %v", rel, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected overlayFS directory %s, got %v", rel, info.Mode())
		}
	}
}
