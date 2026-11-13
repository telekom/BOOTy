//go:build linux

package provision

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/image/sigstoretest"
)

func writeTestCosignKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyCosignSignatures(t *testing.T) {
	keyPath := writeTestCosignKey(t)
	const (
		imageRef  = "oci://registry.example/os@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		sysextRef = "oci://registry.example/ext@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	)
	sysextLayers := []config.SysextLayerConfig{
		{Name: "local", Source: "/deploy/ext.raw", SHA256: strings.Repeat("3", 64)},
		{Name: "ext", Source: " " + sysextRef + " "},
	}
	tests := []struct {
		name       string
		imageURL   string
		checksum   string
		imageKeys  []string
		sysextKeys []string
		sysextOff  bool
		sysextTag  bool
		reject     string // ref whose verification fails
		wantErr    string
		wantRefs   []string
	}{
		{name: "off by default", imageURL: imageRef},
		{name: "image and sysext", imageURL: imageRef, imageKeys: []string{keyPath}, sysextKeys: []string{keyPath},
			wantRefs: []string{imageRef, sysextRef}},
		{name: "unsigned image refused", imageURL: imageRef, imageKeys: []string{keyPath}, reject: imageRef,
			wantErr: "verifying image signature", wantRefs: []string{imageRef}},
		{name: "unsigned sysext refused", imageURL: imageRef, sysextKeys: []string{keyPath}, reject: sysextRef,
			wantErr: `verifying sysext "ext" signature`, wantRefs: []string{sysextRef}},
		{name: "sysexts disabled", imageURL: imageRef, sysextKeys: []string{keyPath}, sysextOff: true},
		{name: "missing key file", imageURL: imageRef, imageKeys: []string{keyPath + ".missing"},
			wantErr: "read cosign public key"},
		{name: "http image with checksum", imageURL: "https://images.example/os.raw", checksum: strings.Repeat("a", 64),
			imageKeys: []string{keyPath}},
		{name: "http image without checksum", imageURL: "https://images.example/os.raw", imageKeys: []string{keyPath},
			wantErr: "no IMAGE_CHECKSUM"},
		{name: "image tag refused before any fetch", imageURL: "oci://registry.example/os:v1", imageKeys: []string{keyPath},
			wantErr: "must be pinned by digest"},
		{name: "sysext tag refused before any fetch", imageURL: imageRef, imageKeys: []string{keyPath}, sysextKeys: []string{keyPath},
			sysextTag: true, wantErr: `verifying sysext "ext" signature`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var refs []string
			previous := verifyCosignSignature
			t.Cleanup(func() { verifyCosignSignature = previous })
			verifyCosignSignature = func(_ context.Context, ref string, keys []crypto.PublicKey) error {
				refs = append(refs, ref)
				if len(keys) != 1 {
					t.Fatalf("keys = %d, want 1", len(keys))
				}
				if ref == tc.reject {
					return errors.New("no signature found")
				}
				return nil
			}
			cfg := &config.MachineConfig{}
			cfg.Provision.Image.Checksum = tc.checksum
			cfg.Provision.Image.CosignPublicKeys = tc.imageKeys
			cfg.Provision.Sysext.Enabled = !tc.sysextOff
			cfg.Provision.Sysext.Layers = slices.Clone(sysextLayers)
			if tc.sysextTag {
				cfg.Provision.Sysext.Layers[1].Source = "oci://registry.example/ext:v1"
			}
			cfg.Provision.Sysext.CosignPublicKeys = tc.sysextKeys
			o := newTestOrchestrator(t, cfg, &mockProvider{})

			err := o.verifyCosignSignatures(context.Background(), tc.imageURL)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("verifyCosignSignatures() = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("verifyCosignSignatures() = %v, want %q", err, tc.wantErr)
			}
			if !slices.Equal(refs, tc.wantRefs) {
				t.Fatalf("verified refs = %q, want %q", refs, tc.wantRefs)
			}
		})
	}
}

func TestDryRunCosignKeys(t *testing.T) {
	keyPath := writeTestCosignKey(t)
	const (
		imageRef  = "oci://registry.example/os@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		sysextRef = "oci://registry.example/ext@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	)
	tests := []struct {
		name       string
		imageURLs  []string
		checksum   string
		imageKeys  []string
		sysextKeys []string
		sysextSrc  string
		want       DryRunStatus
	}{
		{name: "not configured", imageURLs: []string{"oci://registry.example/os:v1"}, want: DryRunPass},
		{name: "image and sysext keys", imageURLs: []string{imageRef}, imageKeys: []string{keyPath}, sysextKeys: []string{keyPath}, want: DryRunPass},
		{name: "missing sysext key", imageURLs: []string{imageRef}, imageKeys: []string{keyPath}, sysextKeys: []string{keyPath + ".missing"}, want: DryRunFail},
		{name: "image tag", imageURLs: []string{"oci://registry.example/os:v1"}, imageKeys: []string{keyPath}, want: DryRunFail},
		{name: "second image source is a tag", imageURLs: []string{imageRef, " oci://registry.example/os:v1 "}, imageKeys: []string{keyPath}, want: DryRunFail},
		{name: "image tag without image keys", imageURLs: []string{"oci://registry.example/os:v1"}, sysextKeys: []string{keyPath}, want: DryRunPass},
		{name: "sysext tag", imageURLs: []string{imageRef}, sysextKeys: []string{keyPath}, sysextSrc: "oci://registry.example/ext:v1", want: DryRunFail},
		{name: "http image without checksum", imageURLs: []string{"https://images.example/os.raw"}, imageKeys: []string{keyPath}, want: DryRunFail},
		{name: "http image with checksum", imageURLs: []string{"https://images.example/os.raw"}, checksum: strings.Repeat("a", 64), imageKeys: []string{keyPath}, want: DryRunPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Image.URLs = tc.imageURLs
			cfg.Provision.Image.Checksum = tc.checksum
			cfg.Provision.Image.CosignPublicKeys = tc.imageKeys
			cfg.Provision.Sysext.Enabled = true
			src := sysextRef
			if tc.sysextSrc != "" {
				src = tc.sysextSrc
			}
			cfg.Provision.Sysext.Layers = []config.SysextLayerConfig{{Name: "ext", Source: src}}
			cfg.Provision.Sysext.CosignPublicKeys = tc.sysextKeys
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			if got := o.dryRunCosignKeys(context.Background()); got.Status != tc.want {
				t.Fatalf("dryRunCosignKeys() = %s (%s), want %s", got.Status, got.Message, tc.want)
			}
		})
	}
}

func TestReportCosignSignature(t *testing.T) {
	tests := []struct {
		name     string
		imageURL string
		want     ReportSignatureResult
	}{
		{name: "oci image", imageURL: "oci://registry.example/os@sha256:" + strings.Repeat("1", 64),
			want: ReportSignatureResult{Method: "cosign", Result: ReportStateVerified}},
		{name: "http image", imageURL: "https://images.example/os.raw",
			want: ReportSignatureResult{Result: ReportStateNotConfigured}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Image.CosignPublicKeys = []string{"/deploy/cosign.pub"}
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			o.bestImageURL = tc.imageURL
			if got := o.buildReport(context.Background()).Verification.Signature; got != tc.want {
				t.Fatalf("signature = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestVerifyCosignKeylessPolicy covers the keyless options and their
// combination with keys: either kind of signature is accepted, a missing
// keyless option fails before any fetch.
func TestVerifyCosignKeylessPolicy(t *testing.T) {
	keyPath := writeTestCosignKey(t)
	ss, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	rootJSON, err := ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, rootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	const imageRef = "oci://registry.example/os@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	tests := []struct {
		name                     string
		keys                     bool
		root, identity, issuer   string
		keyOK, keylessOK         bool
		wantErr                  string
		wantKeyCalls, wantKLCall int
	}{
		{name: "keyless", root: rootPath, identity: ".*", issuer: "https://ci.example", keylessOK: true, wantKLCall: 1},
		{name: "keyless refused", root: rootPath, identity: ".*", issuer: "https://ci.example", wantErr: "keyless refused", wantKLCall: 1},
		{name: "keys first", keys: true, root: rootPath, identity: ".*", issuer: "https://ci.example", keyOK: true, wantKeyCalls: 1},
		{name: "keyless after keys fail", keys: true, root: rootPath, identity: ".*", issuer: "https://ci.example", keylessOK: true,
			wantKeyCalls: 1, wantKLCall: 1},
		{name: "both fail", keys: true, root: rootPath, identity: ".*", issuer: "https://ci.example",
			wantErr: "key refused\nkeyless refused", wantKeyCalls: 1, wantKLCall: 1},
		{name: "issuer missing", root: rootPath, identity: ".*", wantErr: "IMAGE_COSIGN_OIDC_ISSUER"},
		{name: "trusted root missing", identity: ".*", issuer: "https://ci.example", wantErr: "IMAGE_COSIGN_TRUSTED_ROOT"},
		{name: "unreadable trusted root", root: rootPath + ".missing", identity: ".*", issuer: "https://ci.example", wantErr: "load Sigstore trusted root"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var keyCalls, keylessCalls int
			prevKey, prevKeyless := verifyCosignSignature, verifyCosignKeyless
			t.Cleanup(func() { verifyCosignSignature, verifyCosignKeyless = prevKey, prevKeyless })
			verifyCosignSignature = func(context.Context, string, []crypto.PublicKey) error {
				keyCalls++
				if tc.keyOK {
					return nil
				}
				return errors.New("key refused")
			}
			verifyCosignKeyless = func(_ context.Context, _ string, p *image.CosignKeyless) error {
				keylessCalls++
				if p == nil {
					t.Fatal("no keyless policy")
				}
				if tc.keylessOK {
					return nil
				}
				return errors.New("keyless refused")
			}
			cfg := &config.MachineConfig{}
			if tc.keys {
				cfg.Provision.Image.CosignPublicKeys = []string{keyPath}
			}
			cfg.Provision.Image.CosignTrustedRoot = tc.root
			cfg.Provision.Image.CosignIdentityRegexp = tc.identity
			cfg.Provision.Image.CosignOIDCIssuer = tc.issuer
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			err := o.verifyCosignSignatures(context.Background(), imageRef)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("verifyCosignSignatures() = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("verifyCosignSignatures() = %v, want %q", err, tc.wantErr)
			}
			if keyCalls != tc.wantKeyCalls || keylessCalls != tc.wantKLCall {
				t.Fatalf("key calls %d keyless calls %d, want %d and %d", keyCalls, keylessCalls, tc.wantKeyCalls, tc.wantKLCall)
			}
		})
	}
}
