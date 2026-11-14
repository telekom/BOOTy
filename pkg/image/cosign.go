package image

import (
	"bytes"
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
	"net"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
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

var (
	// ErrCosignVerification marks a reference without a signature that
	// verifies against the configured keys.
	ErrCosignVerification = errors.New("cosign signature verification failed")
	// ErrCosignFetch marks a network or registry error (timeout, 5xx,
	// connection error) while fetching the signature, which a retry may
	// fix. The error still wraps ErrCosignVerification.
	ErrCosignFetch = errors.New("fetching cosign signature failed")
)

// LoadCosignPublicKeys reads the PEM public keys (ECDSA, RSA or Ed25519, as
// written by `cosign generate-key-pair` or `cosign public-key`) in paths. A
// file may hold several PUBLIC KEY blocks, so keys can be rotated by
// concatenating them.
func LoadCosignPublicKeys(paths []string) ([]crypto.PublicKey, error) {
	if len(paths) == 0 {
		return nil, errors.New("no cosign public keys configured")
	}
	keys := make([]crypto.PublicKey, 0, len(paths))
	for _, p := range paths {
		data, err := readCosignFile(p)
		if err != nil {
			return nil, fmt.Errorf("read cosign public key: %w", err)
		}
		fileKeys, err := parseCosignPublicKeys(data)
		if err != nil {
			return nil, fmt.Errorf("cosign public key %s: %w", p, err)
		}
		keys = append(keys, fileKeys...)
	}
	return keys, nil
}

// parseCosignPublicKeys parses every PEM block of data. Any data outside
// the blocks and any block that is not a PUBLIC KEY fail the file.
func parseCosignPublicKeys(data []byte) ([]crypto.PublicKey, error) {
	var keys []crypto.PublicKey
	for rest := bytes.TrimSpace(data); len(rest) > 0; rest = bytes.TrimSpace(rest) {
		// pem.Decode skips text before a block; refuse it instead.
		if !bytes.HasPrefix(rest, []byte("-----BEGIN ")) {
			return nil, errors.New("data outside PEM blocks")
		}
		// pem.Decode also skips a malformed block and returns the next
		// one; refuse a decode that consumed more than one BEGIN line.
		block, next := pem.Decode(rest)
		if block == nil || bytes.Count(rest[:len(rest)-len(next)], []byte("-----BEGIN ")) != 1 {
			return nil, errors.New("malformed PEM block")
		}
		rest = next
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("PEM block %q is not a PUBLIC KEY", block.Type)
		}
		key, err := parseCosignPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("no PEM PUBLIC KEY block")
	}
	return keys, nil
}

func parseCosignPublicKey(der []byte) (crypto.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
			return key, nil
		}
		return nil, fmt.Errorf("unsupported ECDSA curve %s", k.Curve.Params().Name)
	case ed25519.PublicKey:
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
	digestRef, err := cosignDigestRef(reference)
	if err != nil {
		return err
	}
	digest := digestRef.DigestStr()
	sigImg, manifest, err := fetchCosignSigTag(digestRef, []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx)})
	if err != nil {
		return err
	}
	// A layer that cannot be read does not hide a later valid one; its
	// error is reported only when no layer verifies.
	checked := 0
	var layerErrs []error
	for i := range manifest.Layers {
		desc := &manifest.Layers[i]
		sig := desc.Annotations[cosignSignatureAnnotation]
		if desc.MediaType != cosignSimpleSigningMediaType || sig == "" || desc.Size > maxCosignPayloadSize {
			continue
		}
		checked++
		layer, err := sigImg.LayerByDigest(desc.Digest)
		if err != nil {
			layerErrs = append(layerErrs, fmt.Errorf("signature layer %s: %w", desc.Digest, cosignFetchError(err, reference)))
			continue
		}
		payload, err := readCosignPayload(layer.Compressed)
		if err != nil {
			layerErrs = append(layerErrs, fmt.Errorf("signature layer %s: %w", desc.Digest, cosignFetchError(err, reference)))
			continue
		}
		if cosignSignatureValid(keys, payload, sig, digest) {
			return nil
		}
	}
	if len(layerErrs) > 0 {
		return fmt.Errorf("%w: none of the %d signatures of %s verifies against the configured keys for digest %s: %w",
			ErrCosignVerification, checked, redacted, digest, errors.Join(layerErrs...))
	}
	return fmt.Errorf("%w: none of the %d signatures of %s verifies against the configured keys for digest %s",
		ErrCosignVerification, checked, redacted, digest)
}

// cosignDigestRef parses reference (without oci://), which must be pinned
// by digest.
func cosignDigestRef(reference string) (name.Digest, error) {
	redacted := RedactOCIRef(reference)
	ref, err := name.ParseReference(reference)
	if err != nil {
		return name.Digest{}, fmt.Errorf("parse OCI reference %q: %w", redacted, wrapRedactedOCIRefError(err, reference))
	}
	digestRef, ok := ref.(name.Digest)
	if !ok {
		return name.Digest{}, fmt.Errorf("%w: %s must be pinned by digest (@sha256:...)", ErrCosignVerification, redacted)
	}
	return digestRef, nil
}

// fetchCosignSigTag fetches the signature image under the sha256-<hex>.sig
// tag of digestRef and its manifest.
func fetchCosignSigTag(digestRef name.Digest, opts []remote.Option) (v1.Image, *v1.Manifest, error) {
	reference := digestRef.String()
	redacted := RedactOCIRef(reference)
	sigTag := digestRef.Context().Tag(strings.Replace(digestRef.DigestStr(), ":", "-", 1) + ".sig")
	sigImg, err := remote.Image(sigTag, opts...)
	if err != nil {
		if cosignFetchTransient(err) {
			return nil, nil, fmt.Errorf("%w: %w: %s: %w", ErrCosignVerification, ErrCosignFetch, redacted, wrapRedactedOCIRefError(err, reference))
		}
		return nil, nil, fmt.Errorf("%w: no signature found for %s: %w", ErrCosignVerification, redacted, wrapRedactedOCIRefError(err, reference))
	}
	manifest, err := sigImg.Manifest()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read signature manifest of %s: %w", ErrCosignVerification, redacted, cosignFetchError(err, reference))
	}
	return sigImg, manifest, nil
}

// cosignFetchError redacts err and marks it with ErrCosignFetch when a
// retry may fix it.
func cosignFetchError(err error, reference string) error {
	redacted := wrapRedactedOCIRefError(err, reference)
	if cosignFetchTransient(err) {
		return fmt.Errorf("%w: %w", ErrCosignFetch, redacted)
	}
	return redacted
}

// cosignFetchTransient reports whether err is a timeout, a connection
// error, a connection closed early, a truncated read or a temporary registry error (5xx, 408, 429).
// A missing signature (404) or a refused request is not.
func cosignFetchTransient(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.Temporary() || terr.StatusCode >= http.StatusInternalServerError
	}
	var nerr net.Error
	var opErr *net.OpError
	// io.EOF: the connection closed before the response headers.
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &opErr) || (errors.As(err, &nerr) && nerr.Timeout())
}

// readCosignPayload reads a payload blob to its end, so go-containerregistry
// checks it against its layer digest.
func readCosignPayload(open func() (io.ReadCloser, error)) ([]byte, error) {
	return readLimited(open, maxCosignPayloadSize)
}

// readLimited reads a blob of at most limit bytes to its end.
func readLimited(open func() (io.ReadCloser, error), limit int64) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("payload larger than %d bytes", limit)
	}
	return data, nil
}

// cosignSignatureValid reports whether sig (base64) is a signature of
// payload by one of keys and payload is a cosign signature of digest.
func cosignSignatureValid(keys []crypto.PublicKey, payload []byte, sig, digest string) bool {
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	if !cosignPayloadNamesDigest(payload, digest) {
		return false
	}
	for _, key := range keys {
		if verifyCosignKey(key, payload, raw) {
			return true
		}
	}
	return false
}

// cosignPayloadNamesDigest reports whether payload is a simple-signing
// cosign signature of digest.
func cosignPayloadNamesDigest(payload []byte, digest string) bool {
	var p struct {
		Critical struct {
			Image struct {
				DockerManifestDigest string `json:"docker-manifest-digest"`
			} `json:"image"`
			Type string `json:"type"`
		} `json:"critical"`
	}
	return json.Unmarshal(payload, &p) == nil &&
		p.Critical.Type == cosignSignatureType && p.Critical.Image.DockerManifestDigest == digest
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
