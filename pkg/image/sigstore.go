package image

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// An offline verifier for keyless Sigstore signatures: a Fulcio certificate
// with an embedded SCT, recorded in a Rekor transparency log, checked
// against a trusted_root.json. It covers what cosign writes for container
// images and nothing more, with the standard library only, so the ramdisk
// does not grow by the ~5.7 MB that sigstore-go adds (see
// docs/library-adoption.md). Supported:
//
//   - bundle media types v0.1 to v0.3 (certificate chain up to v0.2, single
//     certificate from v0.3 on), content DSSE envelope (one signature) or
//     message signature;
//   - Rekor v1 entries of kind dsse 0.0.1, intoto 0.0.2 and hashedrekord
//     0.0.1: a signed entry timestamp (SET, required for bundle v0.1) and an
//     inclusion proof with a signed checkpoint (required from v0.2 on);
//   - Rekor v2 (rekor-tiles) entries of kind hashedrekord 0.0.2, also for
//     DSSE (digest of the pre-authentication encoding): the entry body is
//     rebuilt from the bundle and proven by the inclusion proof and the
//     checkpoint, a signed note of the log key under the log's origin;
//   - RFC 3161 timestamps of a timestamp authority of the trusted root
//     (rfc3161.go);
//   - ECDSA, RSA PKCS#1 v1.5 and Ed25519 log and authority keys.
//
// Every transparency log entry must verify. The certificate is checked at
// every verified time: the integrated time of a Rekor v1 entry with a SET
// and the time of each RFC 3161 timestamp that verifies (one that does not
// is ignored, as in sigstore-go). At least one time is required, so a
// bundle with only Rekor v2 entries needs a timestamp; this is sigstore-go's
// WithObserverTimestamps(1) with a transparency log threshold of 1.
//
// Not supported, so refused: Rekor v2 dsse 0.0.2 entries (sigstore-go does
// not verify them either), public-key bundles.

const (
	sigstoreTrustedRootMediaType = "application/vnd.dev.sigstore.trustedroot+json;version=0.1"
	// A trusted root holds a few certificates and keys.
	maxSigstoreTrustedRootSize = 1 << 20
	// The limits of sigstore-go: more entries or timestamps are refused.
	maxSigstoreTlogEntries = 32
	maxSigstoreTimestamps  = 32
	rekorV2Kind            = "hashedrekord 0.0.2"
)

var (
	oidFulcioIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidFulcioIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidSCTList        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
	oidSAN            = asn1.ObjectIdentifier{2, 5, 29, 17}
	bundleVersionRe   = regexp.MustCompile(`^application/vnd\.dev\.sigstore\.bundle(?:\.v(0\.[1-3])\+json|\+json;version=(0\.[1-3]))$`)
)

// sigstoreRoot is the parsed trusted root.
type sigstoreRoot struct {
	cas  []sigstoreCA
	tsas []sigstoreCA // timestamp authorities
	// Logs by hex log ID. A log can be listed more than once, e.g. with
	// several validity windows; a key valid at the checked time is used.
	tlogs  map[string][]*sigstoreLogKey
	ctlogs map[string][]*sigstoreLogKey
}

type sigstoreCA struct {
	roots, intermediates []*x509.Certificate
	leaf                 *x509.Certificate // timestamp authorities: the signing certificate, if listed
	start, end           time.Time
}

func (ca *sigstoreCA) validAt(t time.Time) bool {
	return !t.Before(ca.start) && (ca.end.IsZero() || !t.After(ca.end))
}

type sigstoreLogKey struct {
	key        crypto.PublicKey
	origin     string // host name of the base URL: the checkpoint origin of a Rekor v2 log
	host       string // host and port of the base URL: the origin of a Rekor v1 log
	start, end time.Time
}

type validFor struct {
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end"`
}

type sigstoreLogJSON struct {
	BaseURL   string `json:"baseUrl"`
	PublicKey struct {
		RawBytes []byte   `json:"rawBytes"`
		ValidFor validFor `json:"validFor"`
	} `json:"publicKey"`
	LogID struct {
		KeyID []byte `json:"keyId"`
	} `json:"logId"`
}

type sigstoreCAJSON struct {
	CertChain struct {
		Certificates []struct {
			RawBytes []byte `json:"rawBytes"`
		} `json:"certificates"`
	} `json:"certChain"`
	ValidFor validFor `json:"validFor"`
}

// loadSigstoreRoot reads a trusted_root.json (protobuf-specs TrustedRoot,
// JSON form).
func loadSigstoreRoot(path string) (*sigstoreRoot, error) {
	data, err := readCosignFile(path)
	if err != nil {
		return nil, fmt.Errorf("read trusted root: %w", err)
	}
	if len(data) > maxSigstoreTrustedRootSize {
		return nil, fmt.Errorf("larger than %d bytes", maxSigstoreTrustedRootSize)
	}
	return parseSigstoreRoot(data)
}

func parseSigstoreRoot(data []byte) (*sigstoreRoot, error) {
	var raw struct {
		MediaType string            `json:"mediaType"`
		Tlogs     []sigstoreLogJSON `json:"tlogs"`
		CTLogs    []sigstoreLogJSON `json:"ctlogs"`
		CAs       []sigstoreCAJSON  `json:"certificateAuthorities"`
		TSAs      []sigstoreCAJSON  `json:"timestampAuthorities"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse trusted root: %w", err)
	}
	if raw.MediaType != sigstoreTrustedRootMediaType {
		return nil, fmt.Errorf("trusted root media type %q is not %q", raw.MediaType, sigstoreTrustedRootMediaType)
	}
	r := &sigstoreRoot{}
	var err error
	if r.tlogs, err = parseSigstoreLogs(raw.Tlogs); err != nil {
		return nil, fmt.Errorf("tlogs: %w", err)
	}
	if r.ctlogs, err = parseSigstoreLogs(raw.CTLogs); err != nil {
		return nil, fmt.Errorf("ctlogs: %w", err)
	}
	if r.cas, err = parseSigstoreCAs(raw.CAs, false); err != nil {
		return nil, fmt.Errorf("certificate authorities: %w", err)
	}
	if r.tsas, err = parseSigstoreCAs(raw.TSAs, true); err != nil {
		return nil, fmt.Errorf("timestamp authorities: %w", err)
	}
	if len(r.cas) == 0 || len(r.tlogs) == 0 || len(r.ctlogs) == 0 {
		return nil, errors.New("trusted root needs certificate authorities, tlogs and ctlogs")
	}
	return r, nil
}

// parseSigstoreCAs parses certificate chains ordered from the leaf or the
// intermediates to the root. For a timestamp authority a first certificate
// that is no CA is the signing certificate.
func parseSigstoreCAs(cas []sigstoreCAJSON, tsa bool) ([]sigstoreCA, error) {
	out := make([]sigstoreCA, 0, len(cas))
	for i, ca := range cas {
		certs := ca.CertChain.Certificates
		if len(certs) == 0 {
			return nil, fmt.Errorf("authority %d has no certificates", i)
		}
		c := sigstoreCA{start: ca.ValidFor.Start}
		if ca.ValidFor.End != nil {
			c.end = *ca.ValidFor.End
		}
		for j, raw := range certs {
			cert, err := x509.ParseCertificate(raw.RawBytes)
			if err != nil {
				return nil, fmt.Errorf("authority %d: %w", i, err)
			}
			switch {
			case j == len(certs)-1:
				c.roots = append(c.roots, cert)
			case j == 0 && tsa && !cert.IsCA:
				c.leaf = cert
			default:
				c.intermediates = append(c.intermediates, cert)
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func parseSigstoreLogs(logs []sigstoreLogJSON) (map[string][]*sigstoreLogKey, error) {
	out := make(map[string][]*sigstoreLogKey, len(logs))
	for i, l := range logs {
		key, err := x509.ParsePKIXPublicKey(l.PublicKey.RawBytes)
		if err != nil {
			// PKCS1_RSA_PKCS1V5, still used by the staging CT log.
			rsaKey, errPKCS1 := x509.ParsePKCS1PublicKey(l.PublicKey.RawBytes)
			if errPKCS1 != nil {
				return nil, fmt.Errorf("log %d: %w", i, err)
			}
			key = rsaKey
		}
		if len(l.LogID.KeyID) == 0 {
			return nil, fmt.Errorf("log %d has no log ID", i)
		}
		if l.PublicKey.ValidFor.Start.IsZero() {
			return nil, fmt.Errorf("log %d has no validity start", i)
		}
		k := &sigstoreLogKey{key: key, start: l.PublicKey.ValidFor.Start}
		if u, err := url.Parse(l.BaseURL); err == nil {
			k.origin, k.host = u.Hostname(), u.Host
		}
		if l.PublicKey.ValidFor.End != nil {
			k.end = *l.PublicKey.ValidFor.End
		}
		id := hex.EncodeToString(l.LogID.KeyID)
		out[id] = append(out[id], k)
	}
	return out, nil
}

func (k *sigstoreLogKey) validAt(t time.Time) bool {
	return !t.Before(k.start) && (k.end.IsZero() || !t.After(k.end))
}

// sigstoreBundle is the JSON form of a protobuf-specs Bundle, as far as
// BOOTy reads it.
type sigstoreBundle struct {
	MediaType            string `json:"mediaType"`
	VerificationMaterial struct {
		Certificate *struct {
			RawBytes []byte `json:"rawBytes"`
		} `json:"certificate"`
		X509CertificateChain *struct {
			Certificates []struct {
				RawBytes []byte `json:"rawBytes"`
			} `json:"certificates"`
		} `json:"x509CertificateChain"`
		TlogEntries               []sigstoreTlogEntry `json:"tlogEntries"`
		TimestampVerificationData struct {
			RFC3161Timestamps []struct {
				SignedTimestamp []byte `json:"signedTimestamp"`
			} `json:"rfc3161Timestamps"`
		} `json:"timestampVerificationData"`
	} `json:"verificationMaterial"`
	DSSEEnvelope *struct {
		Payload     []byte `json:"payload"`
		PayloadType string `json:"payloadType"`
		Signatures  []struct {
			Sig []byte `json:"sig"`
		} `json:"signatures"`
	} `json:"dsseEnvelope"`
	MessageSignature *sigstoreMessageSignature `json:"messageSignature"`
}

type sigstoreMessageSignature struct {
	MessageDigest struct {
		Algorithm string `json:"algorithm"`
		Digest    []byte `json:"digest"`
	} `json:"messageDigest"`
	Signature []byte `json:"signature"`
}

type sigstoreTlogEntry struct {
	LogIndex int64 `json:"logIndex,string"`
	LogID    struct {
		KeyID []byte `json:"keyId"`
	} `json:"logId"`
	KindVersion struct {
		Kind    string `json:"kind"`
		Version string `json:"version"`
	} `json:"kindVersion"`
	IntegratedTime   int64 `json:"integratedTime,string"`
	InclusionPromise *struct {
		SignedEntryTimestamp []byte `json:"signedEntryTimestamp"`
	} `json:"inclusionPromise"`
	InclusionProof *struct {
		LogIndex   int64    `json:"logIndex,string"`
		RootHash   []byte   `json:"rootHash"`
		TreeSize   int64    `json:"treeSize,string"`
		Hashes     [][]byte `json:"hashes"`
		Checkpoint struct {
			Envelope string `json:"envelope"`
		} `json:"checkpoint"`
	} `json:"inclusionProof"`
	CanonicalizedBody []byte `json:"canonicalizedBody"`
}

// sigstoreIdentity is the certificate identity a signature must have.
type sigstoreIdentity struct {
	san    *regexp.Regexp // matched against the whole SAN
	issuer string         // exact
}

// verifySigstoreBundle verifies b against root and id. A DSSE envelope is
// verified with digest nil and its payload returned; a message signature
// must sign the SHA-256 digest (of the artifact, or of an image manifest).
func verifySigstoreBundle(root *sigstoreRoot, id sigstoreIdentity, b *sigstoreBundle, digest []byte) ([]byte, error) {
	return verifySigstoreBundleMessage(root, id, b, digest, nil)
}

// verifySigstoreBundleMessage is verifySigstoreBundle with the signed
// message itself (the .sig tag payload), which the signature is checked
// over; an Ed25519 key signs the message rather than its digest.
func verifySigstoreBundleMessage(root *sigstoreRoot, id sigstoreIdentity, b *sigstoreBundle, digest, message []byte) ([]byte, error) {
	m := bundleVersionRe.FindStringSubmatch(b.MediaType)
	if m == nil {
		return nil, fmt.Errorf("unsupported bundle media type %q", b.MediaType)
	}
	version := m[1] + m[2]
	leaf, err := bundleLeaf(b, version)
	if err != nil {
		return nil, err
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, errors.New("signing certificate lacks the digitalSignature key usage")
	}
	c, err := bundleContent(b, digest)
	if err != nil {
		return nil, err
	}
	c.message = message
	if !c.verify(leaf.PublicKey) {
		return nil, errors.New("signature does not verify against the certificate")
	}
	if n := len(b.VerificationMaterial.TimestampVerificationData.RFC3161Timestamps); n > maxSigstoreTimestamps {
		return nil, fmt.Errorf("%d timestamps, more than %d", n, maxSigstoreTimestamps)
	}
	tsTimes, tsErr := verifySigstoreTimestamps(root, b, c.sig)
	times, err := verifySigstoreTlog(root, b.VerificationMaterial.TlogEntries, leaf, c, version, tsTimes)
	if err != nil {
		if tsErr != nil {
			err = fmt.Errorf("%w; %w", err, tsErr)
		}
		return nil, err
	}
	times = append(times, tsTimes...)
	if len(times) == 0 {
		err := errors.New("no verified signing time: the bundle needs a Rekor v1 signed entry timestamp or an RFC 3161 timestamp of a trusted timestamp authority")
		if tsErr != nil {
			err = fmt.Errorf("%w: %w", err, tsErr)
		}
		return nil, err
	}
	var chain []*x509.Certificate
	for _, t := range times {
		if chain, err = verifySigstoreChain(root, leaf, t); err != nil {
			return nil, err
		}
	}
	if err := verifySigstoreSCT(root, leaf, chain[1]); err != nil {
		return nil, err
	}
	if err := id.verify(leaf); err != nil {
		return nil, err
	}
	return c.payload, nil
}

// signedContent is what a bundle signs.
type signedContent struct {
	dsse    bool
	payload []byte // DSSE payload
	pae     []byte // DSSE pre-authentication encoding, the signed bytes
	digest  []byte // message signature: the signed SHA-256 digest
	message []byte // message signature: the message, when known
	sig     []byte
}

// verify checks the signature over the PAE or the message with the hash of
// the key's algorithm only (ECDSA P-256/SHA-256, P-384/SHA-384,
// P-521/SHA-512, RSA/SHA-256, Ed25519), or over the SHA-256 message digest
// for a P-256 or RSA key.
func (c *signedContent) verify(key crypto.PublicKey) bool {
	if c.dsse {
		return verifySigstoreSig(key, c.pae, c.sig)
	}
	if c.message != nil {
		return verifySigstoreSig(key, c.message, c.sig)
	}
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		return k.Curve == elliptic.P256() && ecdsa.VerifyASN1(k, c.digest, c.sig)
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, c.digest, c.sig) == nil
	}
	return false
}

// loggedHash is the SHA-256 hash a Rekor v1 entry records: of the DSSE
// payload, or the message digest.
func (c *signedContent) loggedHash() []byte {
	if c.dsse {
		h := sha256.Sum256(c.payload)
		return h[:]
	}
	return c.digest
}

func bundleContent(b *sigstoreBundle, digest []byte) (*signedContent, error) {
	switch {
	case b.DSSEEnvelope != nil && b.MessageSignature == nil && digest == nil:
		env := b.DSSEEnvelope
		if len(env.Signatures) != 1 {
			return nil, fmt.Errorf("DSSE envelope has %d signatures, want 1", len(env.Signatures))
		}
		pae := fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(env.PayloadType), env.PayloadType, len(env.Payload), env.Payload)
		return &signedContent{dsse: true, payload: env.Payload, pae: pae, sig: env.Signatures[0].Sig}, nil
	case b.MessageSignature != nil && b.DSSEEnvelope == nil && digest != nil:
		ms := b.MessageSignature
		if ms.MessageDigest.Algorithm != "SHA2_256" || !bytes.Equal(ms.MessageDigest.Digest, digest) {
			return nil, errors.New("message digest does not match the artifact")
		}
		return &signedContent{digest: digest, sig: ms.Signature}, nil
	default:
		return nil, errors.New("bundle content does not match the expected signature kind")
	}
}

func bundleLeaf(b *sigstoreBundle, version string) (*x509.Certificate, error) {
	vm := &b.VerificationMaterial
	var der []byte
	switch {
	case vm.Certificate != nil && vm.X509CertificateChain != nil:
		return nil, errors.New("bundle has both a certificate and a certificate chain")
	case vm.Certificate != nil:
		der = vm.Certificate.RawBytes
	case vm.X509CertificateChain != nil && version == "0.3":
		// As sigstore-go: a v0.3 bundle carries the certificate alone.
		return nil, errors.New("a bundle v0.3 must carry a certificate, not a certificate chain")
	case vm.X509CertificateChain != nil && len(vm.X509CertificateChain.Certificates) > 0:
		// The rest of the chain must come from the trusted root.
		der = vm.X509CertificateChain.Certificates[0].RawBytes
	default:
		return nil, errors.New("bundle has no signing certificate")
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse signing certificate: %w", err)
	}
	return leaf, nil
}

// verifySigstoreTimestamps returns the times of the RFC 3161 timestamps of
// the bundle over sig that verify; the error explains the others.
func verifySigstoreTimestamps(root *sigstoreRoot, b *sigstoreBundle, sig []byte) ([]time.Time, error) {
	var times []time.Time
	var errs []error
	for i, ts := range b.VerificationMaterial.TimestampVerificationData.RFC3161Timestamps {
		t, err := root.verifyRFC3161(ts.SignedTimestamp, sig)
		if err != nil {
			errs = append(errs, fmt.Errorf("RFC 3161 timestamp %d: %w", i, err))
			continue
		}
		times = append(times, t)
	}
	return times, errors.Join(errs...)
}

// verifySigstoreTlog checks that every entry is of a log of the trusted root
// and records the signature, the certificate and the signed content. It
// returns the integrated times of the Rekor v1 entries with a SET. The key
// of each entry must be valid at every verified time: these and tsTimes.
func verifySigstoreTlog(root *sigstoreRoot, entries []sigstoreTlogEntry, leaf *x509.Certificate, c *signedContent, version string, tsTimes []time.Time) ([]time.Time, error) {
	if len(entries) == 0 {
		return nil, errors.New("bundle has no transparency log entry")
	}
	if len(entries) > maxSigstoreTlogEntries {
		return nil, fmt.Errorf("%d transparency log entries, more than %d", len(entries), maxSigstoreTlogEntries)
	}
	seen := make(map[string]bool, len(entries))
	var times []time.Time
	keys := make([][]*sigstoreLogKey, len(entries)) // the keys each entry verifies with
	for i := range entries {
		e := &entries[i]
		k := fmt.Sprintf("%x/%d", e.LogID.KeyID, e.LogIndex)
		if seen[k] {
			return nil, fmt.Errorf("duplicate tlog entry %d", e.LogIndex)
		}
		seen[k] = true
		logID := hex.EncodeToString(e.LogID.KeyID)
		candidates := root.tlogs[logID]
		if len(candidates) == 0 {
			return nil, fmt.Errorf("tlog entry %d: log %s is not in the trusted root", e.LogIndex, logID)
		}
		var errs []error
		var t time.Time
		for _, key := range candidates {
			ti, err := verifySigstoreTlogEntry(e, key, logID, leaf, c, version, tsTimes)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			keys[i], t = append(keys[i], key), ti
		}
		if len(keys[i]) == 0 {
			return nil, fmt.Errorf("tlog entry %d: %w", e.LogIndex, errors.Join(errs...))
		}
		if !t.IsZero() {
			times = append(times, t)
		}
	}
	all := slices.Concat(times, tsTimes)
	for i := range entries {
		if !slices.ContainsFunc(keys[i], func(k *sigstoreLogKey) bool { return k.validAtAll(all) }) {
			return nil, fmt.Errorf("tlog entry %d: log key not valid at every signing time", entries[i].LogIndex)
		}
	}
	return times, nil
}

// verifySigstoreTlogEntry checks one entry with one key of its log; the key
// validity is checked by the caller.
func verifySigstoreTlogEntry(e *sigstoreTlogEntry, key *sigstoreLogKey, logID string, leaf *x509.Certificate, c *signedContent, version string, tsTimes []time.Time) (time.Time, error) {
	if e.KindVersion.Kind+" "+e.KindVersion.Version == rekorV2Kind {
		return time.Time{}, verifyRekorV2Entry(e, key, leaf, c, tsTimes)
	}
	hasSET := e.InclusionPromise != nil && len(e.InclusionPromise.SignedEntryTimestamp) > 0
	switch {
	case !hasSET && version == "0.1":
		return time.Time{}, errors.New("no signed entry timestamp (required for bundle v0.1)")
	case e.InclusionProof == nil && version != "0.1":
		return time.Time{}, errors.New("inclusion proof missing (required from bundle v0.2 on)")
	case !hasSET && e.InclusionProof == nil:
		return time.Time{}, errors.New("neither a signed entry timestamp nor an inclusion proof")
	}
	var integrated time.Time
	if hasSET {
		var err error
		if integrated, err = verifySET(e, key, logID); err != nil {
			return time.Time{}, err
		}
	}
	if e.InclusionProof != nil {
		leafHash := sha256.Sum256(append([]byte{0}, e.CanonicalizedBody...))
		if err := verifyInclusionProof(e, key, leafHash[:], false); err != nil {
			return time.Time{}, fmt.Errorf("inclusion proof: %w", err)
		}
	}
	if err := checkRekorBody(e, leaf, c); err != nil {
		return time.Time{}, err
	}
	return integrated, nil
}

// verifySET checks the signed entry timestamp of a Rekor v1 entry and
// returns its integrated time.
func verifySET(e *sigstoreTlogEntry, key *sigstoreLogKey, logID string) (time.Time, error) {
	integrated := time.Unix(e.IntegratedTime, 0)
	if !key.validAt(integrated) {
		return time.Time{}, errors.New("log key not valid at the integrated time")
	}
	// RFC 8785 canonical JSON of the Rekor payload: the keys sorted, and
	// base64 and hex strings that need no escaping.
	set := fmt.Appendf(nil, `{"body":%q,"integratedTime":%d,"logID":%q,"logIndex":%d}`,
		base64.StdEncoding.EncodeToString(e.CanonicalizedBody), e.IntegratedTime, logID, e.LogIndex)
	if !verifySigstoreSig(key.key, set, e.InclusionPromise.SignedEntryTimestamp) {
		return time.Time{}, errors.New("signed entry timestamp does not verify")
	}
	return integrated, nil
}

func (k *sigstoreLogKey) validAtAll(times []time.Time) bool {
	return !slices.ContainsFunc(times, func(t time.Time) bool { return !k.validAt(t) })
}

// verifyRekorV2Entry checks a Rekor v2 hashedrekord 0.0.2 entry, which has
// no SET and no integrated time: the body rebuilt from the bundle must be
// the logged one and be included in the tree of the signed checkpoint.
func verifyRekorV2Entry(e *sigstoreTlogEntry, key *sigstoreLogKey, leaf *x509.Certificate, c *signedContent, tsTimes []time.Time) error {
	if len(tsTimes) == 0 {
		return errors.New("a Rekor v2 entry carries no time: it needs a verified RFC 3161 timestamp")
	}
	if e.InclusionProof == nil {
		return errors.New("inclusion proof missing (required for Rekor v2)")
	}
	body, err := rekorV2Body(leaf, c)
	if err != nil {
		return err
	}
	if !bytes.Equal(body, e.CanonicalizedBody) {
		return errors.New("entry does not record the signature, the certificate and the digest")
	}
	leafHash := sha256.Sum256(append([]byte{0}, body...))
	if err := verifyInclusionProof(e, key, leafHash[:], true); err != nil {
		return fmt.Errorf("inclusion proof: %w", err)
	}
	return nil
}

// rekorV2Body returns the RFC 8785 canonical JSON of the hashedrekord 0.0.2
// entry (protojson of rekor-tiles' Entry) that records c and leaf. For DSSE
// the digest is of the PAE, with the hash of the key's algorithm.
func rekorV2Body(leaf *x509.Certificate, c *signedContent) ([]byte, error) {
	details, hashName, h, err := rekorKeyDetails(leaf.PublicKey)
	if err != nil {
		return nil, err
	}
	digest := c.digest
	if c.dsse {
		hh := h.New()
		hh.Write(c.pae)
		digest = hh.Sum(nil)
	}
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Appendf(nil, `{"apiVersion":"0.0.2","kind":"hashedrekord","spec":{"hashedRekordV002":{"data":{"algorithm":%q,"digest":%q},`+
		`"signature":{"content":%q,"verifier":{"keyDetails":%q,"x509Certificate":{"rawBytes":%q}}}}}}`,
		hashName, b64(digest), b64(c.sig), details, b64(leaf.Raw)), nil
}

// rekorKeyDetails returns the protobuf-specs PublicKeyDetails, the hash
// algorithm name and the hash of a signing key, as sigstore's default
// algorithm registry.
func rekorKeyDetails(key crypto.PublicKey) (details, hashName string, h crypto.Hash, err error) {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return "PKIX_ECDSA_P256_SHA_256", "SHA2_256", crypto.SHA256, nil
		case elliptic.P384():
			return "PKIX_ECDSA_P384_SHA_384", "SHA2_384", crypto.SHA384, nil
		case elliptic.P521():
			return "PKIX_ECDSA_P521_SHA_512", "SHA2_512", crypto.SHA512, nil
		}
	case *rsa.PublicKey:
		switch n := k.N.BitLen(); n {
		case 2048, 3072, 4096:
			return fmt.Sprintf("PKIX_RSA_PKCS1V15_%d_SHA256", n), "SHA2_256", crypto.SHA256, nil
		}
	}
	return "", "", 0, fmt.Errorf("no Rekor v2 entry for a %T signing key", key)
}

// checkRekorBody checks that the Rekor v1 entry body records the signature,
// the certificate and the hash of the signed content, with an entry kind
// for that content.
func checkRekorBody(e *sigstoreTlogEntry, leaf *x509.Certificate, c *signedContent) error {
	kv := e.KindVersion.Kind + " " + e.KindVersion.Version
	var body struct {
		Kind       string      `json:"kind"`
		APIVersion string      `json:"apiVersion"`
		Spec       rekorV1Spec `json:"spec"`
	}
	if err := json.Unmarshal(e.CanonicalizedBody, &body); err != nil {
		return fmt.Errorf("parse entry body: %w", err)
	}
	if body.Kind+" "+body.APIVersion != kv {
		return fmt.Errorf("entry body is %s %s, entry says %s", body.Kind, body.APIVersion, kv)
	}
	if err := checkRekorKind(kv, c.dsse); err != nil {
		return err
	}
	hash, entrySig, entryCert, err := rekorBodyFields(kv, &body.Spec)
	if err != nil {
		return err
	}
	if hash.Algorithm != "sha256" || hash.Value != hex.EncodeToString(c.loggedHash()) {
		return errors.New("entry does not record the hash of the signed payload")
	}
	if !bytes.Equal(entrySig, c.sig) {
		return errors.New("entry does not record the signature")
	}
	block, _ := pem.Decode(entryCert)
	if block == nil || !bytes.Equal(block.Bytes, leaf.Raw) {
		return errors.New("entry does not record the signing certificate")
	}
	return nil
}

type rekorHash struct{ Algorithm, Value string }

// rekorV1Spec is the spec of the Rekor v1 entry kinds BOOTy reads.
type rekorV1Spec struct {
	// hashedrekord 0.0.1
	Data struct {
		Hash rekorHash `json:"hash"`
	} `json:"data"`
	Signature struct {
		Content   []byte `json:"content"`
		PublicKey struct {
			Content []byte `json:"content"`
		} `json:"publicKey"`
	} `json:"signature"`
	// dsse 0.0.1
	PayloadHash rekorHash `json:"payloadHash"`
	Signatures  []struct {
		Signature []byte `json:"signature"`
		Verifier  []byte `json:"verifier"`
	} `json:"signatures"`
	// intoto 0.0.2
	Content struct {
		PayloadHash rekorHash `json:"payloadHash"`
		Envelope    struct {
			Signatures []struct {
				Sig       []byte `json:"sig"`
				PublicKey []byte `json:"publicKey"`
			} `json:"signatures"`
		} `json:"envelope"`
	} `json:"content"`
}

// rekorBodyFields returns the payload hash, signature and PEM certificate
// that an entry of kind kv records.
func rekorBodyFields(kv string, s *rekorV1Spec) (hash rekorHash, entrySig, entryCert []byte, err error) {
	switch kv {
	case "hashedrekord 0.0.1":
		return s.Data.Hash, s.Signature.Content, s.Signature.PublicKey.Content, nil
	case "dsse 0.0.1":
		if len(s.Signatures) != 1 {
			return hash, nil, nil, fmt.Errorf("dsse entry has %d signatures, want 1", len(s.Signatures))
		}
		return s.PayloadHash, s.Signatures[0].Signature, s.Signatures[0].Verifier, nil
	case "intoto 0.0.2":
		sigs := s.Content.Envelope.Signatures
		if len(sigs) != 1 {
			return hash, nil, nil, fmt.Errorf("intoto entry has %d signatures, want 1", len(sigs))
		}
		// The intoto entry encodes the signature in base64 twice.
		decoded, err := base64.StdEncoding.DecodeString(string(sigs[0].Sig))
		if err != nil {
			return hash, nil, nil, fmt.Errorf("intoto signature: %w", err)
		}
		return s.Content.PayloadHash, decoded, sigs[0].PublicKey, nil
	}
	return hash, nil, nil, fmt.Errorf("unsupported entry kind %s", kv)
}

// checkRekorKind ties the Rekor v1 entry kind to the content: hashedrekord
// for a message signature, dsse or intoto for a DSSE envelope.
func checkRekorKind(kv string, dsse bool) error {
	if (kv == "hashedrekord 0.0.1") == dsse {
		return fmt.Errorf("a %s entry does not record a %s", kv, map[bool]string{true: "DSSE envelope", false: "message signature"}[dsse])
	}
	return nil
}

// verifyInclusionProof checks the RFC 6962 inclusion proof of the entry
// leaf hash and the checkpoint that the log signed over the root hash. A
// Rekor v2 checkpoint must name the log origin.
func verifyInclusionProof(e *sigstoreTlogEntry, key *sigstoreLogKey, leafHash []byte, v2 bool) error {
	p := e.InclusionProof
	if v2 && p.LogIndex != e.LogIndex {
		return errors.New("proof log index is not the entry log index")
	}
	rootHash, err := merkleRootFromProof(p.LogIndex, p.TreeSize, leafHash, p.Hashes)
	if err != nil {
		return err
	}
	if !bytes.Equal(rootHash, p.RootHash) {
		return errors.New("computed root hash does not match")
	}
	size, cpRoot, err := verifyCheckpoint(p.Checkpoint.Envelope, key, v2)
	if err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	if size != p.TreeSize || !bytes.Equal(cpRoot, p.RootHash) {
		return errors.New("checkpoint does not match the proof")
	}
	return nil
}

// merkleRootFromProof computes the root hash of a tree of size from the
// leaf hash at index and its audit path (RFC 9162, section 2.1.3.2).
func merkleRootFromProof(index, size int64, leaf []byte, proof [][]byte) ([]byte, error) {
	if index < 0 || size <= 0 || index >= size {
		return nil, fmt.Errorf("index %d outside tree size %d", index, size)
	}
	fn, sn := index, size-1
	r := leaf
	node := func(l, r []byte) []byte {
		h := sha256.New()
		h.Write([]byte{1})
		h.Write(l)
		h.Write(r)
		return h.Sum(nil)
	}
	for _, p := range proof {
		if sn == 0 {
			return nil, errors.New("proof too long")
		}
		if fn&1 == 1 || fn == sn {
			r = node(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = node(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 {
		return nil, errors.New("proof too short")
	}
	return r, nil
}

// verifyCheckpoint verifies a signed note checkpoint ("origin\nsize\nroot
// base64\n...\n\n— name base64(keyhint+signature)\n") with key and returns
// its tree size and root hash. Signature lines of other keys (witness
// cosignatures) are skipped; as golang.org/x/mod/sumdb/note, one of the log
// key must be there and all of them must verify. The log origin is the
// signer name and the first line: for Rekor v2 the host name of the base
// URL, which it needs; for Rekor v1 the host and port, extended with
// " - <tree ID>".
func verifyCheckpoint(note string, log *sigstoreLogKey, v2 bool) (size int64, rootHash []byte, err error) {
	key, origin := log.key, log.origin
	if !v2 {
		origin = log.host
	}
	if origin == "" {
		// Without the origin the checkpoint could be of any log with the key.
		return 0, nil, errors.New("the log has no base URL in the trusted root, so no checkpoint origin")
	}
	text, sigs, ok := strings.Cut(note, "\n\n")
	if !ok {
		return 0, nil, errors.New("not a signed note")
	}
	text += "\n"
	lines := strings.Split(text, "\n")
	if len(lines) < 4 {
		return 0, nil, errors.New("too few lines")
	}
	if lines[0] != origin && (v2 || !strings.HasPrefix(lines[0], origin+" - ")) {
		return 0, nil, fmt.Errorf("origin %q is not the log %q", lines[0], origin)
	}
	if err := verifyNoteSignatures(text, sigs, key, origin); err != nil {
		return 0, nil, err
	}
	size, err = strconv.ParseInt(lines[1], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("tree size: %w", err)
	}
	rootHash, err = base64.StdEncoding.DecodeString(lines[2])
	if err != nil || len(rootHash) != sha256.Size {
		return 0, nil, errors.New("malformed root hash")
	}
	return size, rootHash, nil
}

// verifyNoteSignatures checks the signature lines of a signed note: those
// of key under origin must all verify, and there must be one.
func verifyNoteSignatures(text, sigs string, key crypto.PublicKey, origin string) error {
	verified := false
	for line := range strings.Lines(sigs) {
		rest, ok := strings.CutPrefix(line, "— ")
		f := strings.Fields(rest)
		if !ok || len(f) != 2 || f[0] != origin {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(f[1])
		hint, errHint := noteKeyHash(f[0], key)
		if err != nil || errHint != nil || len(raw) <= 4 || !bytes.Equal(raw[:4], hint) {
			continue
		}
		if !verifySigstoreSig(key, []byte(text), raw[4:]) {
			return errors.New("signature by the log key does not verify")
		}
		verified = true
	}
	if !verified {
		return errors.New("no signature by the log key")
	}
	return nil
}

// noteKeyHash returns the 4 byte key hint of a signed note signature by
// key under name, as rekor-tiles' note.KeyHash: for ECDSA the SHA-256 of
// the PKIX key (as Rekor v1), for Ed25519 and RSA the hash of name, a
// newline, the algorithm and the key (C2SP signed-note).
func noteKeyHash(name string, key crypto.PublicKey) ([]byte, error) {
	h := sha256.New()
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		der, err := x509.MarshalPKIXPublicKey(k)
		if err != nil {
			return nil, fmt.Errorf("marshal log key: %w", err)
		}
		h.Write(der)
	case ed25519.PublicKey:
		h.Write([]byte(name + "\n\x01"))
		h.Write(k)
	case *rsa.PublicKey:
		der, err := x509.MarshalPKIXPublicKey(k)
		if err != nil {
			return nil, fmt.Errorf("marshal log key: %w", err)
		}
		h.Write([]byte(name + "\n\xffPKIX-RSA-PKCS#1v1.5"))
		h.Write(der)
	default:
		return nil, fmt.Errorf("no note key hash for a %T key", key)
	}
	return h.Sum(nil)[:4], nil
}

// verifySigstoreChain verifies the leaf against the Fulcio CAs at the
// verified time t and returns the chain from the leaf to the root.
func verifySigstoreChain(root *sigstoreRoot, leaf *x509.Certificate, t time.Time) ([]*x509.Certificate, error) {
	// Fulcio marks the SAN critical when the subject is empty; Go leaves a
	// SAN with a name type it does not know (otherName) unhandled.
	leaf.UnhandledCriticalExtensions = slices.DeleteFunc(slices.Clone(leaf.UnhandledCriticalExtensions),
		func(o asn1.ObjectIdentifier) bool { return o.Equal(oidSAN) })
	var errs []error
	for _, ca := range root.cas {
		if t.Before(ca.start) || (!ca.end.IsZero() && t.After(ca.end)) {
			continue
		}
		opts := x509.VerifyOptions{
			Roots: x509.NewCertPool(), Intermediates: x509.NewCertPool(),
			CurrentTime: t, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}
		for _, c := range ca.roots {
			opts.Roots.AddCert(c)
		}
		for _, c := range ca.intermediates {
			opts.Intermediates.AddCert(c)
		}
		chains, err := leaf.Verify(opts)
		if err == nil && len(chains) > 0 && len(chains[0]) > 1 {
			return chains[0], nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("no certificate authority valid at the signing time %s", t.UTC().Format(time.RFC3339))
	}
	return nil, fmt.Errorf("certificate chain at the signing time %s: %w", t.UTC().Format(time.RFC3339), errors.Join(errs...))
}

// verifySigstoreSCT checks that one SCT embedded in leaf is signed by a CT
// log of the trusted root (RFC 6962, section 3.2, precertificate entry).
func verifySigstoreSCT(root *sigstoreRoot, leaf, issuer *x509.Certificate) error {
	var list []byte
	for _, ext := range leaf.Extensions {
		if ext.Id.Equal(oidSCTList) {
			if _, err := asn1.Unmarshal(ext.Value, &list); err != nil {
				return fmt.Errorf("SCT list: %w", err)
			}
		}
	}
	if list == nil {
		return errors.New("certificate has no embedded SCT")
	}
	tbs, err := tbsWithoutExtension(leaf.RawTBSCertificate, oidSCTList)
	if err != nil {
		return fmt.Errorf("precertificate: %w", err)
	}
	issuerKeyHash := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	scts, err := tlsVector(list, 2)
	if err != nil {
		return fmt.Errorf("SCT list: %w", err)
	}
	for len(scts) > 0 {
		var sct []byte
		if sct, scts, err = tlsVectorRest(scts, 2); err != nil {
			return fmt.Errorf("SCT list: %w", err)
		}
		if verifySCT(root, sct, issuerKeyHash[:], tbs) {
			return nil
		}
	}
	return errors.New("no SCT verifies against a CT log of the trusted root")
}

// verifySCT checks one serialized SCT v1.
func verifySCT(root *sigstoreRoot, sct, issuerKeyHash, tbs []byte) bool {
	// version(1) log_id(32) timestamp(8) extensions<2> signature: hash(1)
	// sig(1) <2>
	if len(sct) < 1+32+8+2 || sct[0] != 0 {
		return false
	}
	logID, ts := sct[1:33], binary.BigEndian.Uint64(sct[33:41])
	ext, rest, err := tlsVectorRest(sct[41:], 2)
	if err != nil || len(rest) < 2 || rest[0] != 4 { // SHA-256
		return false
	}
	sigAlg := rest[1]
	sig, rest, err := tlsVectorRest(rest[2:], 2)
	if err != nil || len(rest) != 0 {
		return false
	}
	if ts > math.MaxInt64 {
		return false
	}
	at := time.UnixMilli(int64(ts))
	// digitally-signed struct: version, signature_type certificate_timestamp,
	// timestamp, entry_type precert_entry, issuer_key_hash, tbs<3>,
	// extensions<2>.
	var b bytes.Buffer
	b.Write([]byte{0, 0})
	b.Write(sct[33:41])
	b.Write([]byte{0, 1})
	b.Write(issuerKeyHash)
	b.Write([]byte{byte(len(tbs) >> 16), byte(len(tbs) >> 8), byte(len(tbs))})
	b.Write(tbs)
	b.Write([]byte{byte(len(ext) >> 8), byte(len(ext))})
	b.Write(ext)
	return slices.ContainsFunc(root.ctlogs[hex.EncodeToString(logID)], func(k *sigstoreLogKey) bool {
		return k.validAt(at) && verifySCTSig(k.key, sigAlg, b.Bytes(), sig)
	})
}

// verifySCTSig checks an SCT signature with the algorithm the SCT declares
// (RFC 5246 SignatureAndHashAlgorithm, the hash is SHA-256): ECDSA (3) of
// any curve, as a P-384 CT log signs SHA-256, or RSA PKCS#1 v1.5 (1).
func verifySCTSig(key crypto.PublicKey, alg byte, msg, sig []byte) bool {
	h := sha256.Sum256(msg)
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		return alg == 3 && ecdsa.VerifyASN1(k, h[:], sig)
	case *rsa.PublicKey:
		return alg == 1 && rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
	}
	return false
}

// tlsVector returns the content of a TLS vector with a length prefix of n
// bytes that fills data.
func tlsVector(data []byte, n int) ([]byte, error) {
	v, rest, err := tlsVectorRest(data, n)
	if err == nil && len(rest) != 0 {
		err = errors.New("trailing data")
	}
	return v, err
}

func tlsVectorRest(data []byte, n int) (v, rest []byte, err error) {
	if len(data) < n {
		return nil, nil, errors.New("truncated")
	}
	l := 0
	for _, c := range data[:n] {
		l = l<<8 | int(c)
	}
	if len(data)-n < l {
		return nil, nil, errors.New("truncated")
	}
	return data[n : n+l], data[n+l:], nil
}

// tbsWithoutExtension re-encodes a TBSCertificate without the extension
// oid, as RFC 6962 does to rebuild the precertificate.
func tbsWithoutExtension(tbs []byte, oid asn1.ObjectIdentifier) ([]byte, error) {
	fields, err := asn1Children(tbs, asn1.ClassUniversal, asn1.TagSequence)
	if err != nil || len(fields) == 0 {
		return nil, errors.New("TBSCertificate is not a SEQUENCE")
	}
	// The extensions are the last field, [3] EXPLICIT SEQUENCE OF Extension.
	last := len(fields) - 1
	wrapped, err := asn1Children(fields[last], asn1.ClassContextSpecific, 3)
	if err != nil || len(wrapped) != 1 {
		return nil, errors.New("TBSCertificate has no extensions")
	}
	exts, err := asn1Children(wrapped[0], asn1.ClassUniversal, asn1.TagSequence)
	if err != nil {
		return nil, fmt.Errorf("extensions: %w", err)
	}
	var kept [][]byte
	for _, e := range exts {
		var id struct {
			ID   asn1.ObjectIdentifier
			Rest asn1.RawValue `asn1:"optional"`
		}
		if _, err := asn1.Unmarshal(e, &id); err != nil || !id.ID.Equal(oid) {
			kept = append(kept, e)
		}
	}
	if len(exts)-len(kept) != 1 {
		return nil, fmt.Errorf("%d extensions %s, want 1", len(exts)-len(kept), oid)
	}
	extSeq, err := asn1Wrap(asn1.ClassUniversal, asn1.TagSequence, kept)
	if err != nil {
		return nil, err
	}
	if fields[last], err = asn1Wrap(asn1.ClassContextSpecific, 3, [][]byte{extSeq}); err != nil {
		return nil, err
	}
	return asn1Wrap(asn1.ClassUniversal, asn1.TagSequence, fields)
}

// asn1Children returns the DER encodings of the elements of the constructed
// value der of the given class and tag.
func asn1Children(der []byte, class, tag int) ([][]byte, error) {
	var v asn1.RawValue
	if rest, err := asn1.Unmarshal(der, &v); err != nil || len(rest) != 0 || v.Class != class || v.Tag != tag || !v.IsCompound {
		return nil, errors.New("unexpected ASN.1 value")
	}
	var out [][]byte
	for data := v.Bytes; len(data) > 0; {
		var c asn1.RawValue
		rest, err := asn1.Unmarshal(data, &c)
		if err != nil {
			return nil, fmt.Errorf("parse ASN.1 element: %w", err)
		}
		out = append(out, c.FullBytes)
		data = rest
	}
	return out, nil
}

func asn1Wrap(class, tag int, children [][]byte) ([]byte, error) {
	der, err := asn1.Marshal(asn1.RawValue{Class: class, Tag: tag, IsCompound: true, Bytes: bytes.Join(children, nil)})
	if err != nil {
		return nil, fmt.Errorf("encode ASN.1 value: %w", err)
	}
	return der, nil
}

// verify matches the SAN (URI or e-mail) and the OIDC issuer extension.
func (id sigstoreIdentity) verify(leaf *x509.Certificate) error {
	sans := make([]string, 0, len(leaf.URIs)+len(leaf.EmailAddresses))
	for _, u := range leaf.URIs {
		sans = append(sans, u.String())
	}
	sans = append(sans, leaf.EmailAddresses...)
	if !slices.ContainsFunc(sans, id.san.MatchString) {
		return fmt.Errorf("certificate SAN %q does not match the identity regexp", sans)
	}
	issuer, err := fulcioIssuer(leaf)
	if err != nil {
		return err
	}
	if issuer != id.issuer {
		return fmt.Errorf("certificate OIDC issuer %q is not %q", issuer, id.issuer)
	}
	return nil
}

// fulcioIssuer reads the OIDC issuer extension: the DER UTF8String of
// 1.3.6.1.4.1.57264.1.8, or the raw value of the older 1.3.6.1.4.1.57264.1.1.
func fulcioIssuer(leaf *x509.Certificate) (string, error) {
	var v1 string
	for _, ext := range leaf.Extensions {
		switch {
		case ext.Id.Equal(oidFulcioIssuerV2):
			var s string
			if rest, err := asn1.UnmarshalWithParams(ext.Value, &s, "utf8"); err != nil || len(rest) != 0 {
				return "", errors.New("malformed OIDC issuer extension")
			}
			return s, nil
		case ext.Id.Equal(oidFulcioIssuerV1):
			v1 = string(ext.Value)
		}
	}
	if v1 == "" {
		return "", errors.New("certificate has no OIDC issuer extension")
	}
	return v1, nil
}

// verifySigstoreSig verifies a log or CT signature: ECDSA with the hash of
// its curve, RSA PKCS#1 v1.5 with SHA-256, Ed25519.
func verifySigstoreSig(key crypto.PublicKey, msg, sig []byte) bool {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		var h []byte
		switch k.Curve {
		case elliptic.P256():
			s := sha256.Sum256(msg)
			h = s[:]
		case elliptic.P384():
			s := sha512.Sum384(msg)
			h = s[:]
		case elliptic.P521():
			s := sha512.Sum512(msg)
			h = s[:]
		default:
			return false
		}
		return ecdsa.VerifyASN1(k, h, sig)
	case *rsa.PublicKey:
		h := sha256.Sum256(msg)
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(k, msg, sig)
	}
	return false
}
