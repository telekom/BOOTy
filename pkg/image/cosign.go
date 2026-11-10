package image

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Cosign signature verification against offline public keys.
//
// BOOTy reads the signatures that `cosign sign --key` and `cosign attach
// signature` store in the repository of the image under the tag
// sha256-<hex>.sig: an image with one simple-signing payload layer per
// signature and the base64 signature in the layer annotation
// dev.cosignproject.cosign/signature. A signature is accepted when it
// verifies against one of the configured keys and its payload names the
// digest of the reference. The keys are the trust root: transparency log
// entries, keyless certificates and the Sigstore bundle format (OCI
// referrers) are not read.
const (
	cosignSimpleSigningMediaType = "application/vnd.dev.cosign.simplesigning.v1+json"
	cosignSignatureAnnotation    = "dev.cosignproject.cosign/signature"
	cosignSignatureType          = "cosign container image signature"
	// A simple-signing payload is a few hundred bytes; the limit keeps a
	// hostile registry from filling the ramdisk.
	maxCosignPayloadSize = 64 << 10
	minCosignRSAKeyBits  = 2048
)

// ErrCosignVerification marks a reference without a signature that verifies
// against the configured keys.
var ErrCosignVerification = errors.New("cosign signature verification failed")

// LoadCosignPublicKeys reads one PEM public key (ECDSA, RSA or Ed25519, as
// written by `cosign generate-key-pair` or `cosign public-key`) per path.
func LoadCosignPublicKeys(paths []string) ([]crypto.PublicKey, error) {
	if len(paths) == 0 {
		return nil, errors.New("no cosign public keys configured")
	}
	keys := make([]crypto.PublicKey, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // configured key path in the ramdisk
		if err != nil {
			return nil, fmt.Errorf("read cosign public key: %w", err)
		}
		key, err := parseCosignPublicKey(data)
		if err != nil {
			return nil, fmt.Errorf("cosign public key %s: %w", p, err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func parseCosignPublicKey(data []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("no PEM PUBLIC KEY block")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	switch k := key.(type) {
	case *ecdsa.PublicKey, ed25519.PublicKey:
		return key, nil
	case *rsa.PublicKey:
		if k.N.BitLen() < minCosignRSAKeyBits {
			return nil, fmt.Errorf("RSA key has %d bits, need at least %d", k.N.BitLen(), minCosignRSAKeyBits)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported public key type %T", key)
	}
}

// VerifyCosignSignature checks that the image at reference (with or without
// oci://) carries a cosign signature by one of keys. The reference must be
// pinned by digest: the signature binds the digest, and the later pull by the
// same digest is checked against it, so the bytes written are the bytes
// signed.
func VerifyCosignSignature(ctx context.Context, reference string, keys []crypto.PublicKey) error {
	reference = TrimOCIScheme(strings.TrimSpace(reference))
	redacted := RedactOCIRef(reference)
	if len(keys) == 0 {
		return fmt.Errorf("%w: %s: no public keys configured", ErrCosignVerification, redacted)
	}
	ref, err := name.ParseReference(reference)
	if err != nil {
		return fmt.Errorf("parse OCI reference %q: %w", redacted, wrapRedactedOCIRefError(err, reference))
	}
	digestRef, ok := ref.(name.Digest)
	if !ok {
		return fmt.Errorf("%w: %s must be pinned by digest (@sha256:...)", ErrCosignVerification, redacted)
	}
	digest := digestRef.DigestStr()
	sigTag := digestRef.Context().Tag(strings.Replace(digest, ":", "-", 1) + ".sig")
	sigImg, err := remote.Image(sigTag, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("%w: no signature found for %s: %w", ErrCosignVerification, redacted, wrapRedactedOCIRefError(err, reference))
	}
	manifest, err := sigImg.Manifest()
	if err != nil {
		return fmt.Errorf("%w: read signature manifest of %s: %w", ErrCosignVerification, redacted, wrapRedactedOCIRefError(err, reference))
	}
	checked := 0
	for i := range manifest.Layers {
		desc := &manifest.Layers[i]
		sig := desc.Annotations[cosignSignatureAnnotation]
		if desc.MediaType != cosignSimpleSigningMediaType || sig == "" || desc.Size > maxCosignPayloadSize {
			continue
		}
		checked++
		layer, err := sigImg.LayerByDigest(desc.Digest)
		if err != nil {
			return fmt.Errorf("%w: signature payload of %s: %w", ErrCosignVerification, redacted, wrapRedactedOCIRefError(err, reference))
		}
		payload, err := readCosignPayload(layer.Compressed)
		if err != nil {
			return fmt.Errorf("%w: signature payload of %s: %w", ErrCosignVerification, redacted, wrapRedactedOCIRefError(err, reference))
		}
		if cosignSignatureValid(keys, payload, sig, digest) {
			return nil
		}
	}
	return fmt.Errorf("%w: none of the %d signatures of %s verifies against the configured keys for digest %s",
		ErrCosignVerification, checked, redacted, digest)
}

// readCosignPayload reads a payload blob to its end, so go-containerregistry
// checks it against its layer digest.
func readCosignPayload(open func() (io.ReadCloser, error)) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	payload, err := io.ReadAll(io.LimitReader(rc, maxCosignPayloadSize+1))
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	if len(payload) > maxCosignPayloadSize {
		return nil, fmt.Errorf("payload larger than %d bytes", maxCosignPayloadSize)
	}
	return payload, nil
}

// cosignSignatureValid reports whether sig (base64) is a signature of
// payload by one of keys and payload is a cosign signature of digest.
func cosignSignatureValid(keys []crypto.PublicKey, payload []byte, sig, digest string) bool {
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	var p struct {
		Critical struct {
			Image struct {
				DockerManifestDigest string `json:"docker-manifest-digest"`
			} `json:"image"`
			Type string `json:"type"`
		} `json:"critical"`
	}
	if err := json.Unmarshal(payload, &p); err != nil ||
		p.Critical.Type != cosignSignatureType || p.Critical.Image.DockerManifestDigest != digest {
		return false
	}
	for _, key := range keys {
		if verifyCosignKey(key, payload, raw) {
			return true
		}
	}
	return false
}

// verifyCosignKey verifies like cosign: ECDSA over SHA-256 (plus the hash
// that matches a P-384 or P-521 curve, which KMS keys sign with), RSA
// PKCS#1 v1.5 over SHA-256, Ed25519 over the payload.
func verifyCosignKey(key crypto.PublicKey, payload, sig []byte) bool {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		h := sha256.Sum256(payload)
		if ecdsa.VerifyASN1(k, h[:], sig) {
			return true
		}
		switch k.Curve {
		case elliptic.P384():
			h := sha512.Sum384(payload)
			return ecdsa.VerifyASN1(k, h[:], sig)
		case elliptic.P521():
			h := sha512.Sum512(payload)
			return ecdsa.VerifyASN1(k, h[:], sig)
		}
	case *rsa.PublicKey:
		h := sha256.Sum256(payload)
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(k, payload, sig)
	}
	return false
}
