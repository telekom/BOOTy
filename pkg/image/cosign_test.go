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
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
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
	attachCosignLayers(t, ref, cosignLayer(payload, cosignSimpleSigningMediaType, sig))
}

func cosignLayer(payload []byte, mediaType types.MediaType, sig string) mutate.Addendum {
	return mutate.Addendum{
		Layer:       static.NewLayer(payload, mediaType),
		Annotations: map[string]string{cosignSignatureAnnotation: sig},
	}
}

// attachCosignLayers stores a signature image with layers under the cosign
// signature tag of ref.
func attachCosignLayers(t *testing.T, ref name.Digest, layers ...mutate.Addendum) {
	t.Helper()
	img, err := mutate.Append(empty.Image, layers...)
	if err != nil {
		t.Fatal(err)
	}
	tag := ref.Context().Tag(strings.Replace(ref.DigestStr(), ":", "-", 1) + ".sig")
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
}

// signECDSAPayload signs payload with key over the hash h, as a KMS key
// with a P-384 or P-521 curve does.
func signECDSAPayload(t *testing.T, key *ecdsa.PrivateKey, payload []byte, h crypto.Hash) string {
	t.Helper()
	d := h.New()
	d.Write(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, d.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
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
	// The registry corrupts the blob whose digest is in broken, so reading
	// it fails its digest check.
	reg := registry.New()
	var broken atomic.Value
	broken.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, _ := broken.Load().(string)
		if d == "" || r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/blobs/"+d) {
			reg.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		body[0] ^= 0xff
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	}))
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

	// A signed payload of another type than a cosign image signature.
	wrongType := pushTestImage(t, host+"/os/wrong-type")
	wrongTypePayload := []byte(strings.Replace(string(cosignPayload(wrongType, wrongType.DigestStr())),
		cosignSignatureType, "atomic container signature", 1))
	attachCosignSignature(t, wrongType, wrongTypePayload, signCosignPayload(t, signer, wrongTypePayload))

	// Two signatures; the first layer cannot be read.
	brokenFirst := pushTestImage(t, host+"/os/broken-first")
	brokenPayload := append(cosignPayload(brokenFirst, brokenFirst.DigestStr()), ' ')
	validPayload := cosignPayload(brokenFirst, brokenFirst.DigestStr())
	attachCosignLayers(t, brokenFirst,
		cosignLayer(brokenPayload, cosignSimpleSigningMediaType, signCosignPayload(t, signer, brokenPayload)),
		cosignLayer(validPayload, cosignSimpleSigningMediaType, signCosignPayload(t, signer, validPayload)))
	brokenLayer, err := static.NewLayer(brokenPayload, cosignSimpleSigningMediaType).Digest()
	if err != nil {
		t.Fatal(err)
	}
	broken.Store(brokenLayer.String())
	// Only the broken layer: its read error is reported.
	brokenOnly := pushTestImage(t, host+"/os/broken-only")
	attachCosignLayers(t, brokenOnly,
		cosignLayer(brokenPayload, cosignSimpleSigningMediaType, signCosignPayload(t, signer, brokenPayload)))

	// A layer of another media type is skipped, even with a valid signature.
	otherLayer := pushTestImage(t, host+"/os/other-layer")
	otherLayerPayload := cosignPayload(otherLayer, otherLayer.DigestStr())
	otherLayerSig := signCosignPayload(t, signer, otherLayerPayload)
	attachCosignLayers(t, otherLayer,
		cosignLayer(otherLayerPayload, "application/octet-stream", otherLayerSig),
		cosignLayer(otherLayerPayload, cosignSimpleSigningMediaType, otherLayerSig))
	otherLayerOnly := pushTestImage(t, host+"/os/other-layer-only")
	otherLayerOnlyPayload := cosignPayload(otherLayerOnly, otherLayerOnly.DigestStr())
	attachCosignLayers(t, otherLayerOnly, cosignLayer(otherLayerOnlyPayload, "application/octet-stream",
		signCosignPayload(t, signer, otherLayerOnlyPayload)))

	// ECDSA over each curve and hash: cosign signs P-256 file keys with
	// SHA-256, KMS keys with the hash of their curve.
	curves := map[string]*ecdsa.PrivateKey{}
	for _, c := range []struct {
		name  string
		curve elliptic.Curve
	}{{"p384", elliptic.P384()}, {"p521", elliptic.P521()}} {
		k, err := ecdsa.GenerateKey(c.curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		curves[c.name] = k
	}
	ecdsaSigned := map[string]name.Digest{}
	for _, c := range []struct {
		name, curve string
		hash        crypto.Hash
	}{
		{"p384-sha256", "p384", crypto.SHA256},
		{"p384-sha384", "p384", crypto.SHA384},
		{"p384-sha512", "p384", crypto.SHA512},
		{"p521-sha256", "p521", crypto.SHA256},
		{"p521-sha512", "p521", crypto.SHA512},
	} {
		ref := pushTestImage(t, host+"/os/"+c.name)
		payload := cosignPayload(ref, ref.DigestStr())
		attachCosignSignature(t, ref, payload, signECDSAPayload(t, curves[c.curve], payload, c.hash))
		ecdsaSigned[c.name] = ref
	}

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
		{name: "p384 sha256", ref: ecdsaSigned["p384-sha256"].String(), keys: []crypto.PublicKey{&curves["p384"].PublicKey}},
		{name: "p384 sha384", ref: ecdsaSigned["p384-sha384"].String(), keys: []crypto.PublicKey{&curves["p384"].PublicKey}},
		{name: "p384 sha512 refused", ref: ecdsaSigned["p384-sha512"].String(), keys: []crypto.PublicKey{&curves["p384"].PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "p521 sha256", ref: ecdsaSigned["p521-sha256"].String(), keys: []crypto.PublicKey{&curves["p521"].PublicKey}},
		{name: "p521 sha512", ref: ecdsaSigned["p521-sha512"].String(), keys: []crypto.PublicKey{&curves["p521"].PublicKey}},
		{name: "p384 signature, p521 key", ref: ecdsaSigned["p384-sha384"].String(), keys: []crypto.PublicKey{&curves["p521"].PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "wrong key", ref: signed.String(), keys: []crypto.PublicKey{&other.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "signature names another digest", ref: wrongDigest.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "tampered payload", ref: tampered.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "wrong critical type", ref: wrongType.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "none of the 1 signatures"},
		{name: "first layer broken, second valid", ref: brokenFirst.String(), keys: []crypto.PublicKey{&signer.PublicKey}},
		{name: "only layer broken", ref: brokenOnly.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "error verifying sha256"},
		{name: "non-simplesigning layer skipped", ref: otherLayer.String(), keys: []crypto.PublicKey{&signer.PublicKey}},
		{name: "only a non-simplesigning layer", ref: otherLayerOnly.String(), keys: []crypto.PublicKey{&signer.PublicKey}, wantErr: "none of the 0 signatures"},
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
			if errors.Is(err, ErrCosignFetch) {
				t.Fatalf("VerifyCosignSignature() = %v, want a permanent error", err)
			}
		})
	}
}

func TestCosignFetchTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"no signature", &transport.Error{StatusCode: http.StatusNotFound}, false},
		{"unauthorized", &transport.Error{StatusCode: http.StatusUnauthorized}, false},
		{"server error", &transport.Error{StatusCode: http.StatusBadGateway}, true},
		{"not implemented", &transport.Error{StatusCode: http.StatusNotImplemented}, true},
		{"rate limit", &transport.Error{StatusCode: http.StatusTooManyRequests}, true},
		{"connection refused", &url.Error{Op: "Get", URL: "https://r.example", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		{"timeout", fmt.Errorf("get: %w", context.DeadlineExceeded), true},
		{"truncated", io.ErrUnexpectedEOF, true},
		{"canceled", fmt.Errorf("get: %w", context.Canceled), false},
		{"digest mismatch", errors.New("error verifying sha256 checksum"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cosignFetchTransient(tc.err); got != tc.want {
				t.Fatalf("cosignFetchTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
			if got := errors.Is(cosignFetchError(tc.err, "r.example/os"), ErrCosignFetch); got != tc.want {
				t.Fatalf("cosignFetchError(%v) is ErrCosignFetch = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// A registry that fails with a server error is a transient failure.
func TestVerifyCosignSignatureRegistryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer srv.Close()
	ref := strings.TrimPrefix(srv.URL, "http://") + "/os/image@sha256:" + strings.Repeat("1", 64)
	err := VerifyCosignSignature(context.Background(), ref, []crypto.PublicKey{&newECDSAKey(t).PublicKey})
	if !errors.Is(err, ErrCosignVerification) || !errors.Is(err, ErrCosignFetch) {
		t.Fatalf("VerifyCosignSignature() = %v, want ErrCosignVerification and ErrCosignFetch", err)
	}
}

func publicKeyPEM(t *testing.T, pub crypto.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func writeKeyFile(t *testing.T, dir, file string, data ...[]byte) string {
	t.Helper()
	p := filepath.Join(dir, file)
	if err := os.WriteFile(p, bytes.Join(data, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writePublicKeyPEM(t *testing.T, dir, file string, pub crypto.PublicKey) string {
	t.Helper()
	return writeKeyFile(t, dir, file, publicKeyPEM(t, pub))
}

func TestLoadCosignPublicKeys(t *testing.T) {
	dir := t.TempDir()
	ecPath := writePublicKeyPEM(t, dir, "ec.pub", &newECDSAKey(t).PublicKey)
	smallRSA, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // weak on purpose: must be refused
	if err != nil {
		t.Fatal(err)
	}
	smallRSAPath := writePublicKeyPEM(t, dir, "rsa1024.pub", &smallRSA.PublicKey)
	garbage := writeKeyFile(t, dir, "garbage.pub", []byte("not a key"))
	p224, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p224Path := writePublicKeyPEM(t, dir, "p224.pub", &p224.PublicKey)
	ecPEM := publicKeyPEM(t, &newECDSAKey(t).PublicKey)
	rotated := writeKeyFile(t, dir, "rotated.pub", ecPEM, []byte("\n"), publicKeyPEM(t, &newECDSAKey(t).PublicKey))
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := writeKeyFile(t, dir, "pkcs1.pub",
		pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)}))
	cert := writeKeyFile(t, dir, "cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("der")}))
	keyAndCert := writeKeyFile(t, dir, "key-cert.pem", ecPEM,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("der")}))
	emptyFile := writeKeyFile(t, dir, "empty.pub")
	blank := writeKeyFile(t, dir, "blank.pub", []byte(" \n\t\n"))
	trailing := writeKeyFile(t, dir, "trailing.pub", ecPEM, []byte("trailing text\n"))
	leading := writeKeyFile(t, dir, "leading.pub", []byte("comment\n"), ecPEM)
	truncated := writeKeyFile(t, dir, "truncated.pub", ecPEM[:len(ecPEM)-10])

	tests := []struct {
		name     string
		paths    []string
		wantKeys int
		wantErr  string
	}{
		{name: "ecdsa key", paths: []string{ecPath}, wantKeys: 1},
		{name: "two keys in one file", paths: []string{rotated}, wantKeys: 2},
		{name: "two files", paths: []string{ecPath, rotated}, wantKeys: 3},
		{name: "none", wantErr: "no cosign public keys"},
		{name: "missing file", paths: []string{filepath.Join(dir, "missing.pub")}, wantErr: "read cosign public key"},
		{name: "not PEM", paths: []string{ecPath, garbage}, wantErr: "data outside PEM blocks"},
		{name: "weak RSA", paths: []string{smallRSAPath}, wantErr: "need at least 2048"},
		{name: "P-224 curve", paths: []string{p224Path}, wantErr: "unsupported ECDSA curve P-224"},
		{name: "PKCS#1 RSA PUBLIC KEY", paths: []string{pkcs1}, wantErr: `PEM block "RSA PUBLIC KEY" is not a PUBLIC KEY`},
		{name: "certificate", paths: []string{cert}, wantErr: `PEM block "CERTIFICATE" is not a PUBLIC KEY`},
		{name: "key and certificate", paths: []string{keyAndCert}, wantErr: `PEM block "CERTIFICATE" is not a PUBLIC KEY`},
		{name: "empty file", paths: []string{emptyFile}, wantErr: "no PEM PUBLIC KEY block"},
		{name: "whitespace only", paths: []string{blank}, wantErr: "no PEM PUBLIC KEY block"},
		{name: "trailing data", paths: []string{trailing}, wantErr: "data outside PEM blocks"},
		{name: "leading data", paths: []string{leading}, wantErr: "data outside PEM blocks"},
		{name: "truncated block", paths: []string{truncated}, wantErr: "malformed PEM block"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := LoadCosignPublicKeys(tc.paths)
			if tc.wantErr == "" {
				if err != nil || len(keys) != tc.wantKeys {
					t.Fatalf("LoadCosignPublicKeys() = %d keys, %v, want %d keys", len(keys), err, tc.wantKeys)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadCosignPublicKeys() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
