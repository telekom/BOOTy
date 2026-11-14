package image

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
		{name: "SCT with a broken signature", wantErr: "no SCT verifies", edit: func(o *sigstoretest.Options) { o.BadSCT = true }},
		{name: "missing inclusion proof", wantErr: "inclusion proof missing", edit: func(o *sigstoretest.Options) { o.NoInclusionProof = true }},
		{name: "invalid inclusion proof", wantErr: "computed root hash does not match", edit: func(o *sigstoretest.Options) { o.BadInclusionProof = true }},
		{name: "invalid SET", legacy: true, wantErr: "signed entry timestamp does not verify", edit: func(o *sigstoretest.Options) { o.BadSET = true }},
		{name: "integrated after the certificate expired", wantErr: "certificate has expired",
			edit: func(o *sigstoretest.Options) { o.SignedAt = longAgo; o.IntegratedAt = longAgo.Add(time.Hour) }},
		{name: "unsigned", unsigned: true, wantErr: "no Sigstore bundle referrer"},

		// Bundle versions and content.
		{name: "bundle v0.1 with a chain and SET only", edit: func(o *sigstoretest.Options) {
			o.MediaType, o.CertificateChain, o.NoInclusionProof = sigstoretest.BundleMediaTypeV01, true, true
		}},
		{name: "bundle v0.2 with a chain", edit: func(o *sigstoretest.Options) {
			o.MediaType, o.CertificateChain = sigstoretest.BundleMediaTypeV02, true
		}},
		{name: "bundle v0.3 old media type", edit: func(o *sigstoretest.Options) { o.MediaType = sigstoretest.BundleMediaTypeV03 }},
		{name: "bundle v0.3 with a chain", wantErr: "must carry a certificate, not a certificate chain",
			edit: func(o *sigstoretest.Options) { o.CertificateChain = true }},
		{name: "bundle v0.2 without inclusion proof", wantErr: "inclusion proof missing",
			edit: func(o *sigstoretest.Options) { o.MediaType, o.NoInclusionProof = sigstoretest.BundleMediaTypeV02, true }},
		// A valid message signature over the manifest (`cosign sign-blob`)
		// is no image signature, as in cosign (#933 review).
		{name: "message signature of the manifest", wantErr: "a message signature does not sign an image",
			edit: func(o *sigstoretest.Options) { o.MessageSignature = true }},
		{name: "message signature v0.1 with rekor v2", wantErr: "a message signature does not sign an image",
			edit: func(o *sigstoretest.Options) {
				o.MessageSignature, o.MediaType, o.CertificateChain, o.RekorV2, o.Timestamp = true, sigstoretest.BundleMediaTypeV01, true, true, true
			}},

		// RFC 3161 timestamps and Rekor v2.
		{name: "rekor v1 and timestamp", edit: func(o *sigstoretest.Options) { o.Timestamp = true }},
		{name: "rekor v2 and timestamp", edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp = true, true }},
		{name: "rekor v2 expired certificate stamped in time", edit: func(o *sigstoretest.Options) {
			o.RekorV2, o.Timestamp, o.SignedAt = true, true, longAgo
		}},
		{name: "timestamp without embedded certificate", edit: func(o *sigstoretest.Options) {
			o.RekorV2, o.Timestamp, o.TimestampWithoutCert = true, true, true
		}},
		{name: "rekor v2 without timestamp", wantErr: "needs a verified RFC 3161 timestamp",
			edit: func(o *sigstoretest.Options) { o.RekorV2 = true }},
		{name: "rekor v2 bad checkpoint signature", wantErr: "signature by the log key does not verify",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.BadCheckpoint = true, true, true }},
		{name: "rekor v2 checkpoint by another key", wantErr: "signature by the log key does not verify",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.WrongLogKey = true, true, true }},
		{name: "rekor v2 inclusion proof mismatch", wantErr: "computed root hash does not match",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.BadProofHash = true, true, true }},
		{name: "rekor v2 without inclusion proof", wantErr: "inclusion proof missing",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.NoInclusionProof = true, true, true }},
		{name: "rekor v2 entry of another signature", signDigest: otherDigest, wantErr: "statement subject does not name digest",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp = true, true }},
		{name: "timestamp with signingCertificate v1", edit: func(o *sigstoretest.Options) {
			o.RekorV2, o.Timestamp, o.TimestampESS = true, true, "v1"
		}},
		{name: "timestamp without signingCertificate", wantErr: "no signingCertificate or signingCertificateV2",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.TimestampESS = true, true, "none" }},
		{name: "timestamp signingCertificate of another certificate", wantErr: "does not name the signer certificate",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.TimestampESS = true, true, "wrong" }},
		{name: "TSA not in the trusted root", wantErr: "no timestamp authority of the trusted root signed it",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.UntrustedTSA = true, true, true }},
		{name: "message imprint mismatch", wantErr: "message imprint is not the hash of the signature",
			edit: func(o *sigstoretest.Options) { o.RekorV2, o.Timestamp, o.BadTimestampImprint = true, true, true }},
		{name: "timestamp after the certificate expired", wantErr: "certificate has expired",
			edit: func(o *sigstoretest.Options) {
				o.RekorV2, o.Timestamp, o.SignedAt, o.TimestampAt = true, true, longAgo, longAgo.Add(time.Hour)
			}},
		// Every verified time counts: a SET in time does not excuse a late
		// timestamp.
		{name: "rekor v1 in time, timestamp late", wantErr: "certificate has expired",
			edit: func(o *sigstoretest.Options) {
				o.Timestamp, o.SignedAt, o.TimestampAt = true, longAgo, longAgo.Add(time.Hour)
			}},
		// An untrusted timestamp is ignored when the SET gives a time.
		{name: "rekor v1 and untrusted timestamp", edit: func(o *sigstoretest.Options) { o.Timestamp, o.UntrustedTSA = true, true }},
		{name: "legacy with timestamp", legacy: true, edit: func(o *sigstoretest.Options) { o.Timestamp = true }},
		{name: "legacy timestamp late", legacy: true, wantErr: "certificate has expired",
			edit: func(o *sigstoretest.Options) {
				o.Timestamp, o.SignedAt, o.TimestampAt = true, longAgo, longAgo.Add(time.Hour)
			}},

		// Certificate checks of the #933 review.
		{name: "P-384 key signing with SHA-256", wantErr: "signature does not verify against the certificate",
			edit: func(o *sigstoretest.Options) { o.LeafP384 = true }},
		{name: "no digitalSignature key usage", wantErr: "lacks the digitalSignature key usage",
			edit: func(o *sigstoretest.Options) { o.NoDigitalSignature = true }},
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

	// More referrers than the cap that are no bundles (SBOMs, scan
	// reports) must not hide the bundle.
	t.Run("many other referrers", func(t *testing.T) {
		ref := pushTestImage(t, host+"/os/keyless-referrers")
		for i := range maxCosignReferrers + 6 {
			if err := sigstoretest.PushReferrer(ref, "application/spdx+json", fmt.Appendf(nil, `{"n":%d}`, i), nil); err != nil {
				t.Fatal(err)
			}
		}
		b, err := ss.Bundle(ref.DigestStr(), sigstoretest.Options{Identity: testIdentity, Issuer: testIssuer})
		if err != nil {
			t.Fatal(err)
		}
		if err := sigstoretest.PushBundle(ref, b); err != nil {
			t.Fatal(err)
		}
		if err := VerifyCosignKeyless(context.Background(), ref.String(), policy); err != nil {
			t.Fatalf("VerifyCosignKeyless() = %v", err)
		}
	})

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

// The trust material is pinned at start (#933 review): replacing the
// trusted root or a key file afterwards, or creating one that was missing,
// changes nothing.
func TestPinCosignFiles(t *testing.T) {
	t.Cleanup(unpinCosignFiles)
	ss, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rootPath, keyPath, missing := filepath.Join(dir, "trusted_root.json"), filepath.Join(dir, "cosign.pub"), filepath.Join(dir, "later.pub")
	good, err := ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	key := pemPublicKey(t)
	for p, data := range map[string][]byte{rootPath: good, keyPath: key} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	PinCosignFiles(rootPath, keyPath, missing)

	evil, err := attacker.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range map[string][]byte{rootPath: evil, keyPath: pemPublicKey(t), missing: pemPublicKey(t)} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := LoadCosignKeyless(rootPath, `https://ci\.example/group/os-image//\.ci/build\.yml@refs/heads/main`, testIssuer)
	if err != nil {
		t.Fatal(err)
	}
	for signer, want := range map[*sigstoretest.Sigstore]bool{ss: true, attacker: false} {
		data, err := signer.Bundle("sha256:"+strings.Repeat("ab", 32), sigstoretest.Options{Identity: testIdentity, Issuer: testIssuer})
		if err != nil {
			t.Fatal(err)
		}
		var b sigstoreBundle
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatal(err)
		}
		if _, err := verifySigstoreBundle(policy.root, policy.identity, &b, nil); (err == nil) != want {
			t.Fatalf("bundle of the %s Sigstore: verify = %v", map[bool]string{true: "pinned", false: "replacing"}[want], err)
		}
	}
	keys, err := LoadCosignPublicKeys([]string{keyPath})
	if err != nil {
		t.Fatal(err)
	}
	pinnedKeys, err := parseCosignPublicKeys(key)
	if err != nil || len(keys) != 1 || !keys[0].(interface{ Equal(crypto.PublicKey) bool }).Equal(pinnedKeys[0]) {
		t.Fatalf("LoadCosignPublicKeys() = %v, %v; want the pinned key", keys, err)
	}
	if _, err := LoadCosignPublicKeys([]string{missing}); err == nil {
		t.Fatal("a key file missing at pin time was loaded")
	}
	// Once pinned, a path that was not pinned is not read from disk.
	other := filepath.Join(dir, "other.pub")
	if err := os.WriteFile(other, pemPublicKey(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCosignPublicKeys([]string{other}); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("LoadCosignPublicKeys(unpinned) = %v, want a refusal", err)
	}
}

func pemPublicKey(t *testing.T) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}
