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
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// An offline verifier for keyless Sigstore signatures: a Fulcio certificate
// with an embedded SCT, recorded in a Rekor v1 transparency log, checked
// against a trusted_root.json. It covers what cosign writes for container
// images and nothing more, with the standard library only, so the ramdisk
// does not grow by the ~5.7 MB that sigstore-go adds (see
// docs/library-adoption.md). Supported:
//
//   - bundle media types v0.1 to v0.3, content DSSE envelope (one
//     signature) or message signature;
//   - Rekor v1 entries of kind dsse 0.0.1, intoto 0.0.2 and hashedrekord
//     0.0.1. Each needs a signed entry timestamp (SET), whose integrated
//     time is the time the certificate is checked at; an inclusion proof,
//     required from bundle v0.2 on, is checked against its signed
//     checkpoint;
//   - ECDSA, RSA PKCS#1 v1.5 and Ed25519 keys.
//
// Not supported, so refused: Rekor v2 entries (no SET), RFC 3161
// timestamps, public-key bundles.

const (
	sigstoreTrustedRootMediaType = "application/vnd.dev.sigstore.trustedroot+json;version=0.1"
	// A trusted root holds a few certificates and keys.
	maxSigstoreTrustedRootSize = 1 << 20
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
	cas    []sigstoreCA
	tlogs  map[string]sigstoreLogKey // by hex log ID
	ctlogs map[string]sigstoreLogKey
}

type sigstoreCA struct {
	roots, intermediates []*x509.Certificate
	start, end           time.Time
}

type sigstoreLogKey struct {
	key        crypto.PublicKey
	start, end time.Time
}

type validFor struct {
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end"`
}

type sigstoreLogJSON struct {
	PublicKey struct {
		RawBytes []byte   `json:"rawBytes"`
		ValidFor validFor `json:"validFor"`
	} `json:"publicKey"`
	LogID struct {
		KeyID []byte `json:"keyId"`
	} `json:"logId"`
}

// loadSigstoreRoot reads a trusted_root.json (protobuf-specs TrustedRoot,
// JSON form). Timestamp authorities are ignored.
func loadSigstoreRoot(path string) (*sigstoreRoot, error) {
	data, err := os.ReadFile(path) //nolint:gosec // configured trusted root path in the ramdisk
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
		CAs       []struct {
			CertChain struct {
				Certificates []struct {
					RawBytes []byte `json:"rawBytes"`
				} `json:"certificates"`
			} `json:"certChain"`
			ValidFor validFor `json:"validFor"`
		} `json:"certificateAuthorities"`
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
	for i, ca := range raw.CAs {
		certs := ca.CertChain.Certificates
		if len(certs) == 0 {
			return nil, fmt.Errorf("certificate authority %d has no certificates", i)
		}
		c := sigstoreCA{start: ca.ValidFor.Start}
		if ca.ValidFor.End != nil {
			c.end = *ca.ValidFor.End
		}
		// The chain is ordered from the intermediates to the root.
		for j, raw := range certs {
			cert, err := x509.ParseCertificate(raw.RawBytes)
			if err != nil {
				return nil, fmt.Errorf("certificate authority %d: %w", i, err)
			}
			if j == len(certs)-1 {
				c.roots = append(c.roots, cert)
			} else {
				c.intermediates = append(c.intermediates, cert)
			}
		}
		r.cas = append(r.cas, c)
	}
	if len(r.cas) == 0 || len(r.tlogs) == 0 || len(r.ctlogs) == 0 {
		return nil, errors.New("trusted root needs certificate authorities, tlogs and ctlogs")
	}
	return r, nil
}

func parseSigstoreLogs(logs []sigstoreLogJSON) (map[string]sigstoreLogKey, error) {
	out := make(map[string]sigstoreLogKey, len(logs))
	for i, l := range logs {
		key, err := x509.ParsePKIXPublicKey(l.PublicKey.RawBytes)
		if err != nil {
			return nil, fmt.Errorf("log %d: %w", i, err)
		}
		if len(l.LogID.KeyID) == 0 {
			return nil, fmt.Errorf("log %d has no log ID", i)
		}
		k := sigstoreLogKey{key: key, start: l.PublicKey.ValidFor.Start}
		if l.PublicKey.ValidFor.End != nil {
			k.end = *l.PublicKey.ValidFor.End
		}
		out[hex.EncodeToString(l.LogID.KeyID)] = k
	}
	return out, nil
}

func (k sigstoreLogKey) validAt(t time.Time) bool {
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
		TlogEntries []sigstoreTlogEntry `json:"tlogEntries"`
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

// verifySigstoreBundle verifies b against root and id and returns the DSSE
// payload, or with artifact the message signature over it. Exactly one of
// a DSSE envelope and artifact must be given.
func verifySigstoreBundle(root *sigstoreRoot, id sigstoreIdentity, b *sigstoreBundle, artifact []byte) ([]byte, error) {
	m := bundleVersionRe.FindStringSubmatch(b.MediaType)
	if m == nil {
		return nil, fmt.Errorf("unsupported bundle media type %q", b.MediaType)
	}
	needProof := m[1]+m[2] != "0.1"
	leaf, err := bundleLeaf(b)
	if err != nil {
		return nil, err
	}
	payload, sig, signed, err := bundleContent(b, artifact)
	if err != nil {
		return nil, err
	}
	if !verifyCosignKey(leaf.PublicKey, signed, sig) {
		return nil, errors.New("signature does not verify against the certificate")
	}
	integrated, err := verifySigstoreTlog(root, b.VerificationMaterial.TlogEntries, leaf, sig, payload, needProof)
	if err != nil {
		return nil, err
	}
	chain, err := verifySigstoreChain(root, leaf, integrated)
	if err != nil {
		return nil, err
	}
	if err := verifySigstoreSCT(root, leaf, chain[1]); err != nil {
		return nil, err
	}
	if err := id.verify(leaf); err != nil {
		return nil, err
	}
	return payload, nil
}

// bundleContent returns the payload, the signature and the signed bytes
// (the DSSE pre-authentication encoding or the artifact).
func bundleContent(b *sigstoreBundle, artifact []byte) (payload, sig, signed []byte, err error) {
	switch {
	case b.DSSEEnvelope != nil && b.MessageSignature == nil && artifact == nil:
		env := b.DSSEEnvelope
		if len(env.Signatures) != 1 {
			return nil, nil, nil, fmt.Errorf("DSSE envelope has %d signatures, want 1", len(env.Signatures))
		}
		payload = env.Payload
		signed = fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(env.PayloadType), env.PayloadType, len(payload), payload)
		return payload, env.Signatures[0].Sig, signed, nil
	case b.MessageSignature != nil && b.DSSEEnvelope == nil && artifact != nil:
		ms := b.MessageSignature
		h := sha256.Sum256(artifact)
		if ms.MessageDigest.Algorithm != "SHA2_256" || !bytes.Equal(ms.MessageDigest.Digest, h[:]) {
			return nil, nil, nil, errors.New("message digest does not match the artifact")
		}
		return artifact, ms.Signature, artifact, nil
	default:
		return nil, nil, nil, errors.New("bundle content does not match the expected signature kind")
	}
}

func bundleLeaf(b *sigstoreBundle) (*x509.Certificate, error) {
	vm := &b.VerificationMaterial
	var der []byte
	switch {
	case vm.Certificate != nil:
		der = vm.Certificate.RawBytes
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

// verifySigstoreTlog checks that one entry of a known log records sig, the
// leaf and payload, with a valid SET (and inclusion proof when present or
// needed). It returns the integrated time of that entry.
func verifySigstoreTlog(root *sigstoreRoot, entries []sigstoreTlogEntry, leaf *x509.Certificate, sig, payload []byte, needProof bool) (time.Time, error) {
	var errs []error
	for i := range entries {
		e := &entries[i]
		t, err := verifySigstoreTlogEntry(root, e, leaf, sig, payload, needProof)
		if err == nil {
			return t, nil
		}
		errs = append(errs, fmt.Errorf("tlog entry %d: %w", e.LogIndex, err))
	}
	if len(errs) == 0 {
		return time.Time{}, errors.New("bundle has no transparency log entry")
	}
	return time.Time{}, errors.Join(errs...)
}

func verifySigstoreTlogEntry(root *sigstoreRoot, e *sigstoreTlogEntry, leaf *x509.Certificate, sig, payload []byte, needProof bool) (time.Time, error) {
	logID := hex.EncodeToString(e.LogID.KeyID)
	key, ok := root.tlogs[logID]
	if !ok {
		return time.Time{}, fmt.Errorf("log %s is not in the trusted root", logID)
	}
	integrated := time.Unix(e.IntegratedTime, 0)
	if !key.validAt(integrated) {
		return time.Time{}, errors.New("log key not valid at the integrated time")
	}
	if e.InclusionPromise == nil || len(e.InclusionPromise.SignedEntryTimestamp) == 0 {
		return time.Time{}, errors.New("no signed entry timestamp (Rekor v2 entries are not supported)")
	}
	// RFC 8785 canonical JSON of the Rekor payload: the keys sorted, and
	// base64 and hex strings that need no escaping.
	set := fmt.Appendf(nil, `{"body":%q,"integratedTime":%d,"logID":%q,"logIndex":%d}`,
		base64.StdEncoding.EncodeToString(e.CanonicalizedBody), e.IntegratedTime, logID, e.LogIndex)
	if !verifySigstoreSig(key.key, set, e.InclusionPromise.SignedEntryTimestamp) {
		return time.Time{}, errors.New("signed entry timestamp does not verify")
	}
	switch {
	case e.InclusionProof != nil:
		if err := verifyInclusionProof(e, key.key); err != nil {
			return time.Time{}, fmt.Errorf("inclusion proof: %w", err)
		}
	case needProof:
		return time.Time{}, errors.New("inclusion proof missing")
	}
	if err := checkRekorBody(e, leaf, sig, payload); err != nil {
		return time.Time{}, err
	}
	return integrated, nil
}

// checkRekorBody checks that the log entry body records the signature, the
// certificate and the hash of the signed payload.
func checkRekorBody(e *sigstoreTlogEntry, leaf *x509.Certificate, sig, payload []byte) error {
	kv := e.KindVersion.Kind + " " + e.KindVersion.Version
	var body struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
		Spec       struct {
			// hashedrekord 0.0.1
			Data struct {
				Hash struct{ Algorithm, Value string } `json:"hash"`
			} `json:"data"`
			Signature struct {
				Content   []byte `json:"content"`
				PublicKey struct {
					Content []byte `json:"content"`
				} `json:"publicKey"`
			} `json:"signature"`
			// dsse 0.0.1
			PayloadHash struct{ Algorithm, Value string } `json:"payloadHash"`
			Signatures  []struct {
				Signature []byte `json:"signature"`
				Verifier  []byte `json:"verifier"`
			} `json:"signatures"`
			// intoto 0.0.2
			Content struct {
				PayloadHash struct{ Algorithm, Value string } `json:"payloadHash"`
				Envelope    struct {
					Signatures []struct {
						Sig       []byte `json:"sig"`
						PublicKey []byte `json:"publicKey"`
					} `json:"signatures"`
				} `json:"envelope"`
			} `json:"content"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(e.CanonicalizedBody, &body); err != nil {
		return fmt.Errorf("parse entry body: %w", err)
	}
	if body.Kind+" "+body.APIVersion != kv {
		return fmt.Errorf("entry body is %s %s, entry says %s", body.Kind, body.APIVersion, kv)
	}
	var hash struct{ Algorithm, Value string }
	var entrySig, entryCert []byte
	s := &body.Spec
	switch kv {
	case "hashedrekord 0.0.1":
		hash, entrySig, entryCert = s.Data.Hash, s.Signature.Content, s.Signature.PublicKey.Content
	case "dsse 0.0.1":
		if len(s.Signatures) != 1 {
			return fmt.Errorf("dsse entry has %d signatures, want 1", len(s.Signatures))
		}
		hash, entrySig, entryCert = s.PayloadHash, s.Signatures[0].Signature, s.Signatures[0].Verifier
	case "intoto 0.0.2":
		sigs := s.Content.Envelope.Signatures
		if len(sigs) != 1 {
			return fmt.Errorf("intoto entry has %d signatures, want 1", len(sigs))
		}
		// The intoto entry encodes the signature in base64 twice.
		decoded, err := base64.StdEncoding.DecodeString(string(sigs[0].Sig))
		if err != nil {
			return fmt.Errorf("intoto signature: %w", err)
		}
		hash, entrySig, entryCert = s.Content.PayloadHash, decoded, sigs[0].PublicKey
	default:
		return fmt.Errorf("unsupported entry kind %s", kv)
	}
	want := sha256.Sum256(payload)
	if hash.Algorithm != "sha256" || hash.Value != hex.EncodeToString(want[:]) {
		return errors.New("entry does not record the hash of the signed payload")
	}
	if !bytes.Equal(entrySig, sig) {
		return errors.New("entry does not record the signature")
	}
	block, _ := pem.Decode(entryCert)
	if block == nil || !bytes.Equal(block.Bytes, leaf.Raw) {
		return errors.New("entry does not record the signing certificate")
	}
	return nil
}

// verifyInclusionProof checks the RFC 6962 inclusion proof of the entry
// body and the checkpoint that the log signed over the root hash.
func verifyInclusionProof(e *sigstoreTlogEntry, key crypto.PublicKey) error {
	p := e.InclusionProof
	leaf := sha256.Sum256(append([]byte{0}, e.CanonicalizedBody...))
	rootHash, err := merkleRootFromProof(p.LogIndex, p.TreeSize, leaf[:], p.Hashes)
	if err != nil {
		return err
	}
	if !bytes.Equal(rootHash, p.RootHash) {
		return errors.New("computed root hash does not match")
	}
	size, cpRoot, err := verifyCheckpoint(p.Checkpoint.Envelope, key)
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
// its tree size and root hash.
func verifyCheckpoint(note string, key crypto.PublicKey) (size int64, rootHash []byte, err error) {
	text, sigs, ok := strings.Cut(note, "\n\n")
	if !ok {
		return 0, nil, errors.New("not a signed note")
	}
	text += "\n"
	verified := false
	for line := range strings.Lines(sigs) {
		f := strings.Fields(strings.TrimPrefix(line, "— "))
		if !strings.HasPrefix(line, "— ") || len(f) != 2 {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(f[1])
		if err == nil && len(raw) > 4 && verifySigstoreSig(key, []byte(text), raw[4:]) {
			verified = true
			break
		}
	}
	if !verified {
		return 0, nil, errors.New("no signature by the log key")
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 4 {
		return 0, nil, errors.New("too few lines")
	}
	size, err = strconv.ParseInt(lines[1], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("tree size: %w", err)
	}
	rootHash, err = base64.StdEncoding.DecodeString(lines[2])
	if err != nil {
		return 0, nil, fmt.Errorf("root hash: %w", err)
	}
	return size, rootHash, nil
}

// verifySigstoreChain verifies the leaf against the Fulcio CAs at the
// integrated time t and returns the chain from the leaf to the root.
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
		return nil, fmt.Errorf("no certificate authority valid at the integrated time %s", t.UTC().Format(time.RFC3339))
	}
	return nil, fmt.Errorf("certificate chain at the integrated time %s: %w", t.UTC().Format(time.RFC3339), errors.Join(errs...))
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
	sig, rest, err := tlsVectorRest(rest[2:], 2)
	if err != nil || len(rest) != 0 {
		return false
	}
	key, ok := root.ctlogs[hex.EncodeToString(logID)]
	if !ok || ts > math.MaxInt64 || !key.validAt(time.UnixMilli(int64(ts))) {
		return false
	}
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
	return verifySigstoreSig(key.key, b.Bytes(), sig)
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
