//go:build linux

package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
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
	if _, err := os.Stat(filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded")); !os.IsNotExist(err) {
		t.Fatalf("preload directory should not exist for active-only layers")
	}
}

func TestApplySysextsPullsOCILayer(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	content := []byte("oci sysext")
	ref := pushSysextOCI(t, "test/sysext:v1", content)
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:    "node-tuning",
			Version: "v1",
			Source:  "oci://" + ref,
			SHA256:  digest,
		}},
	}

	if err := c.ApplySysexts(context.Background(), &cfg); err != nil {
		t.Fatalf("ApplySysexts() error: %v", err)
	}

	target := filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded/node-tuning.raw")
	if got := readFile(t, target); got != string(content) {
		t.Fatalf("OCI sysext content = %q", got)
	}

	var catalog sysextCatalog
	readJSON(t, filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded/catalog.json"), &catalog)
	if len(catalog.Layers) != 1 {
		t.Fatalf("catalog layers = %d, want 1", len(catalog.Layers))
	}
	if catalog.Layers[0].FileName != "node-tuning.raw" {
		t.Fatalf("catalog fileName = %q", catalog.Layers[0].FileName)
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

func TestApplySysextsRejectsSymlinkEscape(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "escape")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(c.rootDir, "usr")); err != nil {
		t.Fatal(err)
	}

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:     "escape",
			Source:   source,
			FileName: "escape.raw",
			SHA256:   digest,
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected symlink escape error")
	}
	if !strings.Contains(err.Error(), "target escapes provisioned root") {
		t.Fatalf("expected target escape error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "lib")); !os.IsNotExist(err) {
		t.Fatalf("sysext provisioning created content outside root")
	}
}

func TestApplySysextsRejectsNonRegularLocalSource(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	sourceDir := t.TempDir()

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:   "directory",
			Source: sourceDir,
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected non-regular source error")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected regular file error, got %v", err)
	}
}

func TestSysextHTTPClientBoundsHeaderWait(t *testing.T) {
	transport, ok := sysextHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("sysextHTTPClient transport = %T", sysextHTTPClient.Transport)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("ResponseHeaderTimeout must be bounded")
	}
	if transport.TLSHandshakeTimeout <= 0 {
		t.Fatal("TLSHandshakeTimeout must be bounded")
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

func pushSysextOCI(t *testing.T, repoTag string, data []byte) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)

	layer := stream.NewLayer(io.NopCloser(strings.NewReader(string(data))))
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/%s", strings.TrimPrefix(srv.URL, "http://"), repoTag))
	if err != nil {
		t.Fatalf("parse OCI ref: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := remote.Write(ref, img, remote.WithContext(ctx)); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}
	return ref.String()
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
