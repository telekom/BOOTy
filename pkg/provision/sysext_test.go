//go:build linux

package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/telekom/BOOTy/pkg/config"
	imageutil "github.com/telekom/BOOTy/pkg/image"
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

func TestApplySysextsUpdatesExistingPreloadCatalog(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "updated node tuning sysext")
	targetDir := filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := sysextCatalog{
		APIVersion: "imagebuilding.tcaas.telekom.de/v1alpha1",
		Kind:       "SysextPreloadCatalog",
		Layers: []sysextCatalogLayer{
			{Name: "node-tuning", Version: "v1", FileName: "node-tuning.raw", Path: "/stale/node-tuning.raw", Digest: "sha256:stale"},
			{Name: "debug-tools", Version: "v2", FileName: "debug-tools.raw", Path: "/usr/lib/tcaas-sysext/preloaded/debug-tools.raw", Digest: "sha256:debug"},
		},
	}
	data, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "catalog.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:     "node-tuning",
			Version:  "v1",
			Source:   source,
			FileName: "node-tuning.raw",
			SHA256:   digest,
		}},
	}

	if err := c.ApplySysexts(context.Background(), &cfg); err != nil {
		t.Fatalf("ApplySysexts() error: %v", err)
	}

	var catalog sysextCatalog
	readJSON(t, filepath.Join(targetDir, "catalog.json"), &catalog)
	if len(catalog.Layers) != 2 {
		t.Fatalf("catalog layers = %d, want 2", len(catalog.Layers))
	}
	if got := catalog.Layers[0]; got.Path != "/usr/lib/tcaas-sysext/preloaded/node-tuning.raw" || got.Digest != "sha256:"+digest {
		t.Fatalf("updated catalog layer = %#v", got)
	}
	if got := catalog.Layers[1]; got.Name != "debug-tools" || got.Digest != "sha256:debug" {
		t.Fatalf("preserved catalog layer = %#v", got)
	}
}

func TestApplySysextsRejectsExistingCatalogSymlink(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "safe catalog")
	targetDir := filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-catalog.json")
	if err := os.Symlink(outside, filepath.Join(targetDir, "catalog.json")); err != nil {
		t.Fatal(err)
	}

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:   "node-tuning",
			Source: source,
			SHA256: digest,
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected existing catalog symlink rejection")
	}
	if !strings.Contains(err.Error(), "refusing symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(targetDir, "catalog.json")); err != nil {
		t.Fatalf("catalog lstat: %v", err)
	} else if _, err := os.Readlink(filepath.Join(targetDir, "catalog.json")); err != nil {
		t.Fatal("catalog.json symlink was unexpectedly replaced")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("catalog write followed symlink target")
	}
}

func TestApplySysextsRejectsOversizedCatalog(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "safe catalog")
	targetDir := filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "catalog.json"), []byte(strings.Repeat("x", maxSysextCatalogBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:   "node-tuning",
			Source: source,
			SHA256: digest,
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected oversized catalog rejection")
	}
	if !strings.Contains(err.Error(), "catalog exceeds") {
		t.Fatalf("expected size rejection, got %v", err)
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

func TestApplySysextsRejectsDuplicateTargetPathBeforeCopy(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	sourceA, digestA := writeSysextSource(t, "first")
	sourceB, digestB := writeSysextSource(t, "second")

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{
			{
				Name:     "node-tuning",
				Source:   sourceA,
				FileName: "shared.raw",
				SHA256:   digestA,
			},
			{
				Name:     "vsr",
				Source:   sourceB,
				FileName: "shared.raw",
				SHA256:   digestB,
			},
		},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected duplicate target rejection")
	}
	if !strings.Contains(err.Error(), `duplicate sysext target /usr/lib/tcaas-sysext/preloaded/shared.raw`) {
		t.Fatalf("expected duplicate target error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded/shared.raw")); !os.IsNotExist(err) {
		t.Fatalf("duplicate target validation must run before writing target, stat err=%v", err)
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

func TestApplySysextsRejectsOCIWithoutSysextMediaType(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	content := []byte("ordinary oci layer")
	ref := pushOCIWithMediaType(t, "test/not-a-sysext:v1", content, types.OCILayer)
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

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected ordinary OCI layer to be rejected for sysext preload")
	}
	if !strings.Contains(err.Error(), string(imageutil.SystemdSysextMediaType)) {
		t.Fatalf("error = %q, want sysext media type", err.Error())
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

func TestApplySysextsDoesNotFollowPredictableTempSymlink(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "safe temp")
	targetDir := filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.raw")
	if err := os.Symlink(outside, filepath.Join(targetDir, "layer.raw.tmp")); err != nil {
		t.Fatal(err)
	}

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:     "safe-temp",
			Source:   source,
			FileName: "layer.raw",
			SHA256:   digest,
		}},
	}

	if err := c.ApplySysexts(context.Background(), &cfg); err != nil {
		t.Fatalf("ApplySysexts() error: %v", err)
	}
	if got := readFile(t, filepath.Join(targetDir, "layer.raw")); got != "safe temp" {
		t.Fatalf("sysext content = %q", got)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("predictable temp symlink target was written")
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

func TestApplySysextsRejectsInvalidLayerModeAtApplyTime(t *testing.T) {
	c := newTestConfigurator(t, newMockCommander())
	source, digest := writeSysextSource(t, "invalid mode")

	cfg := config.SysextConfig{
		Enabled: true,
		Layers: []config.SysextLayerConfig{{
			Name:   "node-tuning",
			Mode:   "actve",
			Source: source,
			SHA256: digest,
		}},
	}

	err := c.ApplySysexts(context.Background(), &cfg)
	if err == nil {
		t.Fatal("expected invalid sysext mode error")
	}
	if !strings.Contains(err.Error(), `invalid sysext mode "actve"`) {
		t.Fatalf("expected invalid mode error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.rootDir, "usr/lib/tcaas-sysext/preloaded")); !os.IsNotExist(err) {
		t.Fatalf("invalid mode should not create preload directory")
	}
	if _, err := os.Stat(filepath.Join(c.rootDir, "var/lib/extensions")); !os.IsNotExist(err) {
		t.Fatalf("invalid mode should not create active directory")
	}
}

func TestWriteAndHashRejectsShortWrite(t *testing.T) {
	_, err := writeAndHash(context.Background(), strings.NewReader("short"), shortWriter{})
	if err == nil {
		t.Fatal("expected short write error")
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want %v", err, io.ErrShortWrite)
	}
}

func TestWriteAndHashRejectsZeroProgressReader(t *testing.T) {
	_, err := writeAndHash(context.Background(), zeroProgressReader{}, io.Discard)
	if err == nil {
		t.Fatal("expected zero-progress reader error")
	}
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("error = %v, want %v", err, io.ErrNoProgress)
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
	if sysextHTTPClient.Timeout <= 0 {
		t.Fatal("client Timeout must bound body reads")
	}
}

func TestOpenSysextSourceHTTPBodyTimeout(t *testing.T) {
	oldClient := sysextHTTPClient
	sysextHTTPClient = &http.Client{Timeout: 100 * time.Millisecond}
	t.Cleanup(func() { sysextHTTPClient = oldClient })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(time.Second)
		_, _ = w.Write([]byte("late"))
	}))
	t.Cleanup(srv.Close)

	rc, err := openSysextSource(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("openSysextSource() error: %v", err)
	}
	defer rc.Close()

	_, err = io.ReadAll(rc)
	if err == nil {
		t.Fatal("expected body read timeout")
	}
}

func TestOpenSysextSourceHTTPRetriesTransientStatusThenSucceeds(t *testing.T) {
	withFastSysextHTTPRetry(t)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("sysext"))
	}))
	t.Cleanup(srv.Close)

	rc, err := openSysextSource(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("openSysextSource() error: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(got) != "sysext" {
		t.Fatalf("body = %q, want sysext", got)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestOpenSysextSourceHTTPPermanentFailureDoesNotRetry(t *testing.T) {
	withFastSysextHTTPRetry(t)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "missing", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := openSysextSource(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected permanent HTTP status error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
	if !strings.Contains(err.Error(), "404 Not Found") {
		t.Fatalf("error = %q, want HTTP status text", err.Error())
	}
}

func TestOpenSysextSourceHTTPClosesRetryableStatusBody(t *testing.T) {
	withFastSysextHTTPRetry(t)

	var failedBodyClosed atomic.Bool
	var attempts atomic.Int32
	oldClient := sysextHTTPClient
	sysextHTTPClient = &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Status:     "503 Service Unavailable",
					Body:       &closeTrackingReadCloser{closed: &failedBodyClosed},
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("sysext")),
			}, nil
		}),
	}
	t.Cleanup(func() { sysextHTTPClient = oldClient })

	rc, err := openSysextSource(context.Background(), "https://example.invalid/layer.raw")
	if err != nil {
		t.Fatalf("openSysextSource() error: %v", err)
	}
	defer rc.Close()

	if !failedBodyClosed.Load() {
		t.Fatal("retryable failure response body was not closed")
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestOpenSysextSourceHTTPStatusErrorRedactsSensitiveURLParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("robot", "secret")
	u.Path = "/layer.raw"
	u.RawQuery = "token=abc"
	source := u.String()

	_, err = openSysextSource(context.Background(), source)
	if err == nil {
		t.Fatal("expected HTTP status error")
	}
	if strings.Contains(err.Error(), "robot") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc") {
		t.Fatalf("error leaked sensitive URL parts: %q", err.Error())
	}
	wantURL := srv.URL + "/layer.raw"
	if !strings.Contains(err.Error(), wantURL) {
		t.Fatalf("error = %q, want redacted URL %q", err.Error(), wantURL)
	}
	if !strings.Contains(err.Error(), "404 Not Found") {
		t.Fatalf("error = %q, want HTTP status text", err.Error())
	}
}

func TestOpenSysextSourceHTTPFetchErrorRedactsAndPreservesCause(t *testing.T) {
	withFastSysextHTTPRetry(t)

	cause := errors.New("sentinel transport")
	source := "https://robot:secret@example.invalid/layer.raw?token=abc#frag"
	oldClient := sysextHTTPClient
	sysextHTTPClient = &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Get", URL: source, Err: cause}
		}),
	}
	t.Cleanup(func() { sysextHTTPClient = oldClient })

	_, err := openSysextSource(context.Background(), source)
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(err, cause) = false; err=%v", err)
	}
	for _, sensitive := range []string{"robot", "secret", "token=abc", "#frag"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("error leaked %q: %q", sensitive, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "https://example.invalid/layer.raw") {
		t.Fatalf("error = %q, want redacted source context", err.Error())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type closeTrackingReadCloser struct {
	closed *atomic.Bool
}

func (c *closeTrackingReadCloser) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (c *closeTrackingReadCloser) Close() error {
	c.closed.Store(true)
	return nil
}

func withFastSysextHTTPRetry(t *testing.T) {
	t.Helper()
	oldPolicy := sysextHTTPRetryPolicy
	sysextHTTPRetryPolicy = RetryPolicy{MaxRetries: 3, Transient: false}
	t.Cleanup(func() { sysextHTTPRetryPolicy = oldPolicy })
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
	return pushOCIWithMediaType(t, repoTag, data, imageutil.SystemdSysextMediaType)
}

func pushOCIWithMediaType(t *testing.T, repoTag string, data []byte, mediaType types.MediaType) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)

	layer := stream.NewLayer(io.NopCloser(strings.NewReader(string(data))), stream.WithMediaType(mediaType))
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

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}

type zeroProgressReader struct{}

func (zeroProgressReader) Read([]byte) (int, error) {
	return 0, nil
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
