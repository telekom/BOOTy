package image

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/telekom/BOOTy/pkg/image/sigstoretest"
)

const (
	testIdentity = "https://ci.example/group/os-image//.ci/build.yml@refs/heads/main"
	testIssuer   = "https://ci.example"
)

func writeTrustedRoot(t *testing.T, s *sigstoretest.Sigstore) string {
	t.Helper()
	data, err := s.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadTestKeyless(t *testing.T, s *sigstoretest.Sigstore) *CosignKeyless {
	t.Helper()
	p, err := LoadCosignKeyless(writeTrustedRoot(t, s), `https://ci\.example/group/os-image//\.ci/build\.yml@refs/heads/main`, testIssuer)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyCosignKeyless(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	ss, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	other, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	policy := loadTestKeyless(t, ss)
	longAgo := time.Now().Add(-3 * time.Hour)
	otherDigest := "sha256:" + strings.Repeat("0", 64)

	tests := []struct {
		name   string
		legacy bool                        // a cosign 2 .sig tag signature instead of a bundle referrer
		signer *sigstoretest.Sigstore      // default ss
		edit   func(*sigstoretest.Options) // changes the good signature
		// signDigest is the digest the signature names; default the image.
		signDigest string
		unsigned   bool
		wantErr    string
	}{
		{name: "bundle"},
		{name: "legacy sig tag", legacy: true},
		// The certificate has long expired; the log recorded the signature
		// while it was valid.
		{name: "expired certificate signed in time", edit: func(o *sigstoretest.Options) { o.SignedAt = longAgo }},
		{name: "legacy expired certificate signed in time", legacy: true, edit: func(o *sigstoretest.Options) { o.SignedAt = longAgo }},
		{name: "wrong identity", wantErr: "does not match the identity regexp",
			edit: func(o *sigstoretest.Options) { o.Identity = testIdentity + "-fork" }},
		{name: "identity only as a suffix", wantErr: "does not match the identity regexp",
			edit: func(o *sigstoretest.Options) { o.Identity = "https://evil.example/?" + testIdentity }},
		{name: "legacy wrong identity", legacy: true, wantErr: "does not match the identity regexp",
			edit: func(o *sigstoretest.Options) { o.Identity = "someone@example.com" }},
		{name: "wrong issuer", wantErr: `OIDC issuer "https://other.example" is not`,
			edit: func(o *sigstoretest.Options) { o.Issuer = "https://other.example" }},
		{name: "other trust root", signer: other, wantErr: "is not in the trusted root"},
		{name: "tampered digest", signDigest: otherDigest, wantErr: "statement subject does not name digest"},
		{name: "legacy payload names another digest", legacy: true, signDigest: otherDigest, wantErr: "payload does not name digest"},
		{name: "other predicate", wantErr: "is not a cosign signature",
			edit: func(o *sigstoretest.Options) { o.Predicate = "https://example.com/sbom" }},
		{name: "missing SCT", wantErr: "certificate has no embedded SCT", edit: func(o *sigstoretest.Options) { o.NoSCT = true }},
		{name: "missing inclusion proof", wantErr: "inclusion proof missing", edit: func(o *sigstoretest.Options) { o.NoInclusionProof = true }},
		{name: "invalid inclusion proof", wantErr: "computed root hash does not match", edit: func(o *sigstoretest.Options) { o.BadInclusionProof = true }},
		{name: "invalid SET", legacy: true, wantErr: "signed entry timestamp does not verify", edit: func(o *sigstoretest.Options) { o.BadSET = true }},
		{name: "integrated after the certificate expired", wantErr: "certificate has expired",
			edit: func(o *sigstoretest.Options) { o.SignedAt = longAgo; o.IntegratedAt = longAgo.Add(time.Hour) }},
		{name: "unsigned", unsigned: true, wantErr: "no Sigstore bundle referrer"},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := pushTestImage(t, fmt.Sprintf("%s/os/keyless-%d", host, i))
			o := sigstoretest.Options{Identity: testIdentity, Issuer: testIssuer}
			if tc.edit != nil {
				tc.edit(&o)
			}
			signer, digest := ss, ref.DigestStr()
			if tc.signer != nil {
				signer = tc.signer
			}
			if tc.signDigest != "" {
				digest = tc.signDigest
			}
			switch {
			case tc.unsigned:
			case tc.legacy:
				payload := sigstoretest.SimpleSigningPayload(ref.Context().Name(), digest)
				ann, err := signer.Legacy(payload, o)
				if err != nil {
					t.Fatal(err)
				}
				if err := sigstoretest.PushLegacy(ref, payload, ann); err != nil {
					t.Fatal(err)
				}
			default:
				b, err := signer.Bundle(digest, o)
				if err != nil {
					t.Fatal(err)
				}
				if err := sigstoretest.PushBundle(ref, b); err != nil {
					t.Fatal(err)
				}
			}
			err := VerifyCosignKeyless(context.Background(), ref.String(), policy)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("VerifyCosignKeyless() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrCosignVerification) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("VerifyCosignKeyless() = %v, want ErrCosignVerification containing %q", err, tc.wantErr)
			}
			if errors.Is(err, ErrCosignFetch) {
				t.Fatalf("VerifyCosignKeyless() = %v, want a permanent error", err)
			}
		})
	}

	t.Run("tag not digest", func(t *testing.T) {
		err := VerifyCosignKeyless(context.Background(), host+"/os/unsigned:v1", policy)
		if !errors.Is(err, ErrCosignVerification) || !strings.Contains(err.Error(), "must be pinned by digest") {
			t.Fatalf("VerifyCosignKeyless() = %v", err)
		}
	})
}

func TestLoadCosignKeyless(t *testing.T) {
	ss, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	trustedRoot := writeTrustedRoot(t, ss)
	garbage := filepath.Join(t.TempDir(), "garbage.json")
	if err := os.WriteFile(garbage, []byte(`{"mediaType":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, root, identity, issuer, wantErr string
	}{
		{name: "ok", root: trustedRoot, identity: ".*", issuer: testIssuer},
		{name: "no root", identity: ".*", issuer: testIssuer, wantErr: "needs the trusted root"},
		{name: "no identity", root: trustedRoot, issuer: testIssuer, wantErr: "needs the trusted root"},
		{name: "no issuer", root: trustedRoot, identity: ".*", wantErr: "needs the trusted root"},
		{name: "missing root", root: filepath.Join(t.TempDir(), "missing.json"), identity: ".*", issuer: testIssuer, wantErr: "load Sigstore trusted root"},
		{name: "bad root", root: garbage, identity: ".*", issuer: testIssuer, wantErr: "load Sigstore trusted root"},
		{name: "bad regexp", root: trustedRoot, identity: "(", issuer: testIssuer, wantErr: "identity regexp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadCosignKeyless(tc.root, tc.identity, tc.issuer)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadCosignKeyless() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
