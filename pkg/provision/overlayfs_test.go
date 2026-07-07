//go:build linux

package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestConfigureOverlayFSWritesOverlayrootLocalConfigAndDirs(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	cfg := &config.OverlayFSConfig{
		Enabled:    true,
		Mode:       config.OverlayFSModeDevice,
		Device:     "LABEL=BOOTY-OVERLAY",
		Directory:  "booty",
		UpperDir:   "/var/lib/booty/overlayfs/upper",
		WorkDir:    "/var/lib/booty/overlayfs/work",
		TimeoutSec: 30,
		Debug:      true,
	}

	if err := c.ConfigureOverlayFS(cfg, "ubuntu"); err != nil {
		t.Fatalf("ConfigureOverlayFS() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(c.rootDir, overlayRootLocalConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	want := `overlayroot="device:dev=LABEL=BOOTY-OVERLAY,timeout=30,swap=0,recurse=0,debug=1,dir=booty"`
	if !strings.Contains(content, want) {
		t.Fatalf("overlayroot config = %q, want %q", content, want)
	}
	for _, rel := range []string{
		"var/lib/booty/overlayfs/upper",
		"var/lib/booty/overlayfs/work",
	} {
		if info, err := os.Stat(filepath.Join(c.rootDir, rel)); err != nil || !info.IsDir() {
			t.Fatalf("%s should be a directory, info=%v err=%v", rel, info, err)
		}
	}
}

func TestConfigureOverlayFSTmpfsDefaults(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	cfg := &config.OverlayFSConfig{Enabled: true}

	if err := c.ConfigureOverlayFS(cfg, "Ubuntu"); err != nil {
		t.Fatalf("ConfigureOverlayFS() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(c.rootDir, overlayRootLocalConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `overlayroot="tmpfs:swap=0,recurse=0"`) {
		t.Fatalf("overlayroot config = %q", string(data))
	}
}

func TestConfigureOverlayFSSkipsDisabled(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())

	if err := c.ConfigureOverlayFS(&config.OverlayFSConfig{}, "ubuntu"); err != nil {
		t.Fatalf("ConfigureOverlayFS() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.rootDir, overlayRootLocalConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("disabled overlayFS should not write config, stat error: %v", err)
	}
}

func TestConfigureOverlayFSRejectsNonUbuntu(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())

	err := c.ConfigureOverlayFS(&config.OverlayFSConfig{Enabled: true}, "flatcar")
	if err == nil {
		t.Fatal("expected non-Ubuntu rejection")
	}
	if !strings.Contains(err.Error(), "osFamily=ubuntu") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderOverlayRootConfigRejectsDeviceWithoutDevice(t *testing.T) {
	_, err := renderOverlayRootConfig(&config.OverlayFSConfig{
		Enabled: true,
		Mode:    config.OverlayFSModeDevice,
	})
	if err == nil {
		t.Fatal("expected missing device error")
	}
	if !strings.Contains(err.Error(), "device is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
