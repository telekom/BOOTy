package image

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Keyless cosign verification against an offline Sigstore trusted root
// (sigstore.go). BOOTy reads two layouts:
//
//   - a Sigstore bundle (v0.1 to v0.3) attached to the image as an OCI
//     referrer, as cosign 3 does by default: a DSSE envelope with an in-toto
//     statement of predicate type CosignSignPredicateType whose subject is
//     the image digest, or a message signature (`cosign sign-blob` style)
//     whose message digest is the image manifest digest;
//   - the sha256-<hex>.sig tag that cosign 2 and `cosign sign
//     --new-bundle-format=false` write: a simple-signing payload with the
//     certificate, the Rekor entry (with its SET) and an optional RFC 3161
//     timestamp in layer annotations.
const (
	// CosignSignPredicateType is the in-toto predicate type of `cosign
	// sign` in the bundle format.
	CosignSignPredicateType     = "https://sigstore.dev/cosign/sign/v1"
	sigstoreBundleMediaTypeBase = "application/vnd.dev.sigstore.bundle"
	ociEmptyConfigMediaType     = "application/vnd.oci.empty.v1+json"
	cosignCertificateAnnotation = "dev.sigstore.cosign/certificate"
	cosignRekorBundleAnnotation = "dev.sigstore.cosign/bundle"
	cosignRFC3161Annotation     = "dev.sigstore.cosign/rfc3161timestamp"
	inTotoStatementV1           = "https://in-toto.io/Statement/v1"
	inTotoPayloadType           = "application/vnd.in-toto+json"
	// A bundle is a few KiB; the limits keep a hostile registry from
	// filling the ramdisk or making BOOTy walk many referrers. Only
	// referrers that may hold a bundle count, so SBOMs and scan reports
	// attached to the image cannot hide the signature.
	maxSigstoreBundleSize = 1 << 20
	maxCosignReferrers    = 64
	maxBundlesPerReferrer = 4
)

// CosignKeyless is a keyless verification policy: an offline trusted root
// and the certificate identity that must have signed.
type CosignKeyless struct {
	root     *sigstoreRoot
	identity sigstoreIdentity
}

// LoadCosignKeyless reads the Sigstore trusted_root.json at trustedRootPath
// and builds a policy that accepts certificates whose subject alternative
// name matches identityRegexp in full and whose OIDC issuer extension is
// oidcIssuer. All three are required.
func LoadCosignKeyless(trustedRootPath, identityRegexp, oidcIssuer string) (*CosignKeyless, error) {
	if trustedRootPath == "" || identityRegexp == "" || oidcIssuer == "" {
		return nil, errors.New("keyless cosign verification needs the trusted root, the identity regexp and the OIDC issuer")
	}
	san, err := regexp.Compile("^(?:" + identityRegexp + ")$")
	if err != nil {
		return nil, fmt.Errorf("cosign identity regexp: %w", err)
	}
	root, err := loadSigstoreRoot(trustedRootPath)
	if err != nil {
		return nil, fmt.Errorf("load Sigstore trusted root %s: %w", trustedRootPath, err)
	}
	return &CosignKeyless{root: root, identity: sigstoreIdentity{san: san, issuer: oidcIssuer}}, nil
}

// VerifyCosignKeyless checks that the image at reference (with or without
// oci://), pinned by sha256 digest, carries a keyless cosign signature that
// satisfies policy: a Sigstore bundle referrer or a .sig tag signature.
func VerifyCosignKeyless(ctx context.Context, reference string, policy *CosignKeyless) error {
	reference = TrimOCIScheme(strings.TrimSpace(reference))
	redacted := RedactOCIRef(reference)
	if policy == nil {
		return fmt.Errorf("%w: %s: no keyless policy configured", ErrCosignVerification, redacted)
	}
	digestRef, err := cosignDigestRef(reference)
	if err != nil {
		return err
	}
	digest := digestRef.DigestStr()
	if hexDigest, ok := strings.CutPrefix(digest, "sha256:"); !ok || len(hexDigest) != sha256.Size*2 {
		return fmt.Errorf("%w: %s: keyless verification needs a sha256 digest", ErrCosignVerification, redacted)
	}
	opts := []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx)}
	checked, errReferrers := policy.verifyReferrers(digestRef, opts)
	if errReferrers == nil {
		return nil
	}
	n, errSigTag := policy.verifySigTag(digestRef, opts)
	if errSigTag == nil {
		return nil
	}
	return fmt.Errorf("%w: none of the %d keyless signatures of %s verifies for digest %s: %w",
		ErrCosignVerification, checked+n, redacted, digest, errors.Join(errReferrers, errSigTag))
}

// verifyReferrers checks the Sigstore bundles attached to the digest as OCI
// referrers and returns the number of bundles checked.
func (p *CosignKeyless) verifyReferrers(digestRef name.Digest, opts []remote.Option) (int, error) {
	reference := digestRef.String()
	index, err := remote.Referrers(digestRef, opts...)
	if err != nil {
		return 0, fmt.Errorf("referrers: %w", cosignFetchError(err, reference))
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return 0, fmt.Errorf("referrers: %w", cosignFetchError(err, reference))
	}
	checked, candidates := 0, 0
	var errs []error
	for i := range manifest.Manifests {
		desc := &manifest.Manifests[i]
		// cosign sets artifactType in the manifest; a registry that takes it
		// from the config reports the empty config type.
		if at := desc.ArtifactType; at != "" && at != ociEmptyConfigMediaType && !strings.HasPrefix(at, sigstoreBundleMediaTypeBase) {
			continue
		}
		if candidates == maxCosignReferrers {
			errs = append(errs, fmt.Errorf("more than %d bundle referrers, the rest is not checked", maxCosignReferrers))
			break
		}
		candidates++
		img, err := remote.Image(digestRef.Context().Digest(desc.Digest.String()), opts...)
		if err != nil {
			errs = append(errs, fmt.Errorf("referrer %s: %w", desc.Digest, cosignFetchError(err, reference)))
			continue
		}
		m, err := img.Manifest()
		if err != nil {
			errs = append(errs, fmt.Errorf("referrer %s: %w", desc.Digest, cosignFetchError(err, reference)))
			continue
		}
		bundles := 0
		for j := range m.Layers {
			layer := &m.Layers[j]
			if !strings.HasPrefix(string(layer.MediaType), sigstoreBundleMediaTypeBase) {
				continue
			}
			if bundles == maxBundlesPerReferrer {
				errs = append(errs, fmt.Errorf("referrer %s: more than %d bundles, the rest is not checked", desc.Digest, maxBundlesPerReferrer))
				break
			}
			bundles++
			checked++
			if err := p.verifyBundleLayer(img, layer, digestRef); err != nil {
				errs = append(errs, fmt.Errorf("bundle %s: %w", layer.Digest, err))
				continue
			}
			return checked, nil
		}
	}
	if len(errs) == 0 {
		return 0, errors.New("no Sigstore bundle referrer")
	}
	return checked, errors.Join(errs...)
}

func (p *CosignKeyless) verifyBundleLayer(img v1.Image, desc *v1.Descriptor, digestRef name.Digest) error {
	if desc.Size > maxSigstoreBundleSize {
		return fmt.Errorf("larger than %d bytes", maxSigstoreBundleSize)
	}
	layer, err := img.LayerByDigest(desc.Digest)
	if err != nil {
		return cosignFetchError(err, digestRef.String())
	}
	data, err := readLimited(layer.Compressed, maxSigstoreBundleSize)
	if err != nil {
		return cosignFetchError(err, digestRef.String())
	}
	var b sigstoreBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("parse bundle: %w", err)
	}
	hexDigest := strings.TrimPrefix(digestRef.DigestStr(), "sha256:")
	if b.MessageSignature != nil {
		// A message signature over the manifest: its digest is the image digest.
		digest, err := hex.DecodeString(hexDigest)
		if err != nil {
			return fmt.Errorf("image digest: %w", err)
		}
		_, err = verifySigstoreBundle(p.root, p.identity, &b, digest)
		return err
	}
	if b.DSSEEnvelope == nil || b.DSSEEnvelope.PayloadType != inTotoPayloadType {
		return errors.New("bundle carries neither an in-toto DSSE envelope nor a message signature")
	}
	payload, err := verifySigstoreBundle(p.root, p.identity, &b, nil)
	if err != nil {
		return err
	}
	return checkCosignStatement(payload, hexDigest)
}

// checkCosignStatement checks that the verified in-toto statement is a
// `cosign sign` statement about the image digest.
func checkCosignStatement(payload []byte, hexDigest string) error {
	var st struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return fmt.Errorf("parse in-toto statement: %w", err)
	}
	if st.Type != inTotoStatementV1 || st.PredicateType != CosignSignPredicateType {
		return fmt.Errorf("statement %s with predicate type %q is not a cosign signature (%s)", st.Type, st.PredicateType, CosignSignPredicateType)
	}
	for _, s := range st.Subject {
		if strings.EqualFold(s.Digest["sha256"], hexDigest) {
			return nil
		}
	}
	return fmt.Errorf("statement subject does not name digest sha256:%s", hexDigest)
}

// verifySigTag checks the keyless simple-signing signatures under the
// sha256-<hex>.sig tag and returns the number of signatures checked.
func (p *CosignKeyless) verifySigTag(digestRef name.Digest, opts []remote.Option) (int, error) {
	sigImg, manifest, err := fetchCosignSigTag(digestRef, opts)
	if err != nil {
		return 0, err
	}
	checked := 0
	var errs []error
	for i := range manifest.Layers {
		desc := &manifest.Layers[i]
		if desc.MediaType != cosignSimpleSigningMediaType || desc.Annotations[cosignCertificateAnnotation] == "" ||
			desc.Size > maxCosignPayloadSize {
			continue
		}
		checked++
		layer, err := sigImg.LayerByDigest(desc.Digest)
		if err != nil {
			errs = append(errs, fmt.Errorf("signature layer %s: %w", desc.Digest, cosignFetchError(err, digestRef.String())))
			continue
		}
		payload, err := readCosignPayload(layer.Compressed)
		if err != nil {
			errs = append(errs, fmt.Errorf("signature layer %s: %w", desc.Digest, cosignFetchError(err, digestRef.String())))
			continue
		}
		if err := p.verifyLegacySignature(desc.Annotations, payload, digestRef.DigestStr()); err != nil {
			errs = append(errs, fmt.Errorf("signature layer %s: %w", desc.Digest, err))
			continue
		}
		return checked, nil
	}
	if len(errs) == 0 {
		return 0, errors.New("no keyless signature under the .sig tag")
	}
	return checked, errors.Join(errs...)
}

// verifyLegacySignature verifies a .sig tag signature as a v0.1 bundle:
// message signature over the payload, the certificate, the Rekor entry
// with its SET from the dev.sigstore.cosign/bundle annotation and the RFC
// 3161 timestamp of dev.sigstore.cosign/rfc3161timestamp, if any.
func (p *CosignKeyless) verifyLegacySignature(annotations map[string]string, payload []byte, digest string) error {
	if !cosignPayloadNamesDigest(payload, digest) {
		return fmt.Errorf("payload does not name digest %s", digest)
	}
	sig, err := base64.StdEncoding.DecodeString(annotations[cosignSignatureAnnotation])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	block, _ := pem.Decode([]byte(annotations[cosignCertificateAnnotation]))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("certificate annotation is not a PEM CERTIFICATE")
	}
	var rb struct {
		SignedEntryTimestamp []byte `json:"SignedEntryTimestamp"`
		Payload              struct {
			Body           []byte `json:"body"`
			IntegratedTime int64  `json:"integratedTime"`
			LogIndex       int64  `json:"logIndex"`
			LogID          string `json:"logID"`
		} `json:"Payload"`
	}
	if err := json.Unmarshal([]byte(annotations[cosignRekorBundleAnnotation]), &rb); err != nil {
		return fmt.Errorf("parse Rekor bundle annotation: %w", err)
	}
	var e sigstoreTlogEntry
	if e.LogID.KeyID, err = hex.DecodeString(rb.Payload.LogID); err != nil {
		return fmt.Errorf("parse Rekor log ID: %w", err)
	}
	var kind struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(rb.Payload.Body, &kind); err != nil {
		return fmt.Errorf("parse Rekor entry body: %w", err)
	}
	e.LogIndex, e.IntegratedTime, e.CanonicalizedBody = rb.Payload.LogIndex, rb.Payload.IntegratedTime, rb.Payload.Body
	e.KindVersion.Kind, e.KindVersion.Version = kind.Kind, kind.APIVersion
	e.InclusionPromise = &struct {
		SignedEntryTimestamp []byte `json:"signedEntryTimestamp"`
	}{rb.SignedEntryTimestamp}

	var b sigstoreBundle
	b.MediaType = sigstoreBundleMediaTypeBase + "+json;version=0.1"
	b.VerificationMaterial.Certificate = &struct {
		RawBytes []byte `json:"rawBytes"`
	}{block.Bytes}
	b.VerificationMaterial.TlogEntries = []sigstoreTlogEntry{e}
	if ann := annotations[cosignRFC3161Annotation]; ann != "" {
		var ts struct {
			SignedRFC3161Timestamp []byte `json:"SignedRFC3161Timestamp"`
		}
		if err := json.Unmarshal([]byte(ann), &ts); err != nil {
			return fmt.Errorf("parse RFC 3161 timestamp annotation: %w", err)
		}
		tsd := &b.VerificationMaterial.TimestampVerificationData
		tsd.RFC3161Timestamps = append(tsd.RFC3161Timestamps, struct {
			SignedTimestamp []byte `json:"signedTimestamp"`
		}{ts.SignedRFC3161Timestamp})
	}
	h := sha256.Sum256(payload)
	b.MessageSignature = &sigstoreMessageSignature{Signature: sig}
	b.MessageSignature.MessageDigest.Algorithm = "SHA2_256"
	b.MessageSignature.MessageDigest.Digest = h[:]
	_, err = verifySigstoreBundleMessage(p.root, p.identity, &b, h[:], payload)
	return err
}
