package image

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
)

// pushTestImage pushes a random image to repo and returns its digest reference.
func pushTestImage(t *testing.T, repo string) name.Digest {
	t.Helper()
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(repo + ":v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return tag.Context().Digest(d.String())
}

// cosignPayload is the simple-signing payload `cosign sign` writes.
func cosignPayload(ref name.Digest, digest string) []byte {
	return fmt.Appendf(nil,
		`{"critical":{"identity":{"docker-reference":%q},"image":{"docker-manifest-digest":%q},"type":"cosign container image signature"},"optional":null}`,
		ref.Context().Name(), digest)
}

func signCosignPayload(t *testing.T, key crypto.Signer, payload []byte) string {
	t.Helper()
	var (
		sig []byte
		err error
	)
	if _, ok := key.(ed25519.PrivateKey); ok {
		sig, err = key.Sign(rand.Reader, payload, crypto.Hash(0))
	} else {
		h := sha256.Sum256(payload)
		sig, err = key.Sign(rand.Reader, h[:], crypto.SHA256)
	}
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// attachCosignSignature stores payload and sig under the cosign signature
// tag of ref, like `cosign attach signature`.
func attachCosignSignature(t *testing.T, ref name.Digest, payload []byte, sig string) {
	t.Helper()
	layer := static.NewLayer(payload, cosignSimpleSigningMediaType)
	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer:       layer,
		Annotations: map[string]string{cosignSignatureAnnotation: sig},
	})
	if err != nil {
		t.Fatal(err)
	}
	tag := ref.Context().Tag(strings.Replace(ref.DigestStr(), ":", "-", 1) + ".sig")
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
}

func newECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestVerifyCosignSignature(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	signer := newECDSAKey(t)
	other := newECDSAKey(t)
	edPub, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	signed := pushTestImage(t, host+"/os/signed")
	attachCosignSignature(t, signed, cosignPayload(signed, signed.DigestStr()),
		signCosignPayload(t, signer, cosignPayload(signed, signed.DigestStr())))

	edSigned := pushTestImage(t, host+"/os/ed25519")
	attachCosignSignature(t, edSigned, cosignPayload(edSigned, edSigned.DigestStr()),
		signCosignPayload(t, edKey, cosignPayload(edSigned, edSigned.DigestStr())))

	rsaSigned := pushTestImage(t, host+"/os/rsa")
	attachCosignSignature(t, rsaSigned, cosignPayload(rsaSigned, rsaSigned.DigestStr()),
		signCosignPayload(t, rsaKey, cosignPayload(rsaSigned, rsaSigned.DigestStr())))

	// The signature names another digest: a valid signature of another
	// image copied to this one.
	otherDigest := pushTestImage(t, host+"/os/other").DigestStr()
	wrongDigest := pushTestImage(t, host+"/os/wrong-digest")
	attachCosignSignature(t, wrongDigest, cosignPayload(wrongDigest, otherDigest),
		signCosignPayload(t, signer, cosignPayload(wrongDigest, otherDigest)))

	// The payload was changed after signing.
	tampered := pushTestImage(t, host+"/os/tampered")
	tamperedSig := signCosignPayload(t, signer, cosignPayload(tampered, otherDigest))
	attachCosignSignature(t, tampered, cosignPayload(tampered, tampered.DigestStr()), tamperedSig)

	unsigned := pushTestImage(t, host+"/os/unsigned")

	tests := []struct {
		name    string
		ref     string
		keys    []crypto.PublicKey
		wantErr string
	}{
		{name: "valid signature", ref: "oci://" + signed.String(), keys: []crypto.PublicKey{&signer.PublicKey}},
		{name: "second key of two", ref: signed.String(), keys: []crypto.PublicKey{&other.PublicKey, &signer.PublicKey}},
		{name: "ed25519 key", ref: edSigned.String(), keys: []crypto.PublicKey{edPub}},
		{name: "rsa key", ref: rsaSigned.String(), keys: []crypto.PublicKey{&rsaKey.PublicKey}},
		{name: "wrong key", ref: signed.String(), keys: []crypto.PublicKey{&other.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "signature names another digest", ref: wrongDigest.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "tampered payload", ref: tampered.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "missing signature", ref: unsigned.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "no signature found"},
		{name: "tag not digest", ref: host + "/os/signed:v1", keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "must be pinned by digest"},
		{name: "no keys", ref: signed.String(), wantErr: "no public keys configured"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyCosignSignature(context.Background(), tc.ref, tc.keys)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("VerifyCosignSignature() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrCosignVerification) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("VerifyCosignSignature() = %v, want ErrCosignVerification containing %q", err, tc.wantErr)
			}
		})
	}
}

func writePublicKeyPEM(t *testing.T, dir, file string, pub crypto.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, file)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCosignPublicKeys(t *testing.T) {
	dir := t.TempDir()
	ecPath := writePublicKeyPEM(t, dir, "ec.pub", &newECDSAKey(t).PublicKey)
	smallRSA, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // weak on purpose: must be refused
	if err != nil {
		t.Fatal(err)
	}
	smallRSAPath := writePublicKeyPEM(t, dir, "rsa1024.pub", &smallRSA.PublicKey)
	garbage := filepath.Join(dir, "garbage.pub")
	if err := os.WriteFile(garbage, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		paths   []string
		wantErr string
	}{
		{name: "ecdsa key", paths: []string{ecPath}},
		{name: "none", wantErr: "no cosign public keys"},
		{name: "missing file", paths: []string{filepath.Join(dir, "missing.pub")}, wantErr: "read cosign public key"},
		{name: "not PEM", paths: []string{ecPath, garbage}, wantErr: "no PEM PUBLIC KEY block"},
		{name: "weak RSA", paths: []string{smallRSAPath}, wantErr: "need at least 2048"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := LoadCosignPublicKeys(tc.paths)
			if tc.wantErr == "" {
				if err != nil || len(keys) != len(tc.paths) {
					t.Fatalf("LoadCosignPublicKeys() = %d keys, %v", len(keys), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadCosignPublicKeys() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestOCIBlobTamperedUnderSignedDigest serves other bytes under the layer
// digest of a signed image. A reader that stops early (A/B and root
// streaming stop after the last partition) sees no error; the drain that
// withDownloadChecksum sets up for oci:// sources reads to the end, where
// go-containerregistry compares the blob with its digest.
func TestOCIBlobTamperedUnderSignedDigest(t *testing.T) {
	reg := registry.New()
	var tamper bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tamper && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/") {
			rec := httptest.NewRecorder()
			reg.ServeHTTP(rec, r)
			body := rec.Body.Bytes()
			if len(body) > 0 {
				body[len(body)-1] ^= 0xff
			}
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(body)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	defer srv.Close()

	payload := bytes.Repeat([]byte("signed image "), 1000)
	img, err := mutate.Append(empty.Image, mutate.Addendum{Layer: static.NewLayer(payload, "application/vnd.example.disk+raw")})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(strings.TrimPrefix(srv.URL, "http://") + "/os/tampered-blob:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	url := "oci://" + tag.Context().Digest(d.String()).String()
	tamper = true

	for _, drain := range []bool{false, true} {
		rc, err := FetchOCIImageBlob(context.Background(), TrimOCIScheme(url))
		if err != nil {
			t.Fatal(err)
		}
		opt := StreamOpts{}
		if drain {
			if opt, err = withDownloadChecksum(opt, url); err != nil {
				t.Fatal(err)
			}
		}
		src := opt.download.source(rc)
		if _, err := io.ReadFull(src, make([]byte, 100)); err != nil {
			t.Fatalf("read the first partition: %v", err)
		}
		err = verifyChecksum(nil, opt)
		_ = rc.Close()
		if drain && (err == nil || !strings.Contains(err.Error(), "error verifying sha256")) {
			t.Fatalf("verifyChecksum() = %v for a tampered oci:// blob, want a digest error", err)
		}
		if !drain && err != nil {
			t.Fatalf("verifyChecksum() without a drain = %v, want nil (the control case)", err)
		}
	}
}
