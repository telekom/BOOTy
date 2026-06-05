//go:build linux

package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestApplySysextsPreloadsLayerAndCatalog(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "kubernetes sysext")

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:    "kubernetes",
			Version: "v1.34.3",
			Source:  source,
			SHA256:  "sha256:" + digest,
		}},
	}

	if err := c.ApplySysexts(context.Background(), &cfg); err != nil {
		t.Fatalf("ApplySysexts() error: %v", err)
	}

	target := filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded", filepath.Base(source))
	if got := readFile(t, target); got != "kubernetes sysext" {
		t.Fatalf("preloaded sysext content = %q", got)
	}

	var catalog sysextCatalog
	readJSON(t, filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded/catalog.json"), &catalog)
	if catalog.APIVersion != "imagebuilding.tcaas.telekom.de/v1alpha1" {
		t.Fatalf("catalog apiVersion = %q", catalog.APIVersion)
	}
	if len(catalog.Layers) != 1 {
		t.Fatalf("catalog layers = %d, want 1", len(catalog.Layers))
	}
	layer := catalog.Layers[0]
	if layer.Name != "kubernetes" || layer.Version != "v1.34.3" {
		t.Fatalf("catalog layer = %#v", layer)
	}
	if layer.Path != "/usr/lib/tcaas-sysext/preloaded/"+filepath.Base(source) {
		t.Fatalf("catalog path = %q", layer.Path)
	}
	if layer.Digest != "sha256:"+digest {
		t.Fatalf("catalog digest = %q", layer.Digest)
	}
}

func TestApplySysextsActiveModeDoesNotWriteCatalog(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "debug sysext")

	cfg := config.SysextConfig{
		Enabled:     true,
		DefaultMode: "active",
		Layers: []config.SysextLayerConfig{{
			Name:     "debug-tools",
			Source:   source,
			FileName: "debug-tools.raw",
			SHA256:   digest,
		}},
	}

	if err := c.ApplySysexts(context.Background(), &cfg); err != nil {
		t.Fatalf("ApplySysexts() error: %v", err)
	}

	target := filepath.Join(c.rootDir, "var/lib/extensions/debug-tools.raw")
	if got := readFile(t, target); got != "debug sysext" {
		t.Fatalf("active sysext content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded/catalog.json")); !os.IsNotExist(err) {
		t.Fatalf("preload catalog should not exist for active-only layers")
	}
}

func TestApplySysextsDigestMismatch(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, _ := writeSysextSource(t, "bad digest")

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:   "bad",
			Source: source,
			SHA256: strings.Repeat("0", 64),
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
}

func TestApplySysextsRejectsUnsafeFileName(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "unsafe")

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:     "unsafe",
			Source:   source,
			FileName: "../unsafe.raw",
			SHA256:   digest,
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected unsafe filename error")
	}
	if !strings.Contains(err.Error(), "unsafe fileName") {
		t.Fatalf("expected unsafe filename error, got %v", err)
	}
}

func writeSysextSource(t *testing.T, content string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layer.raw")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
