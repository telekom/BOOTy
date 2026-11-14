// Package sigstoretest is a local, offline Sigstore for tests: a Fulcio-like
// CA that issues 10 minute certificates with an embedded SCT, a CT log key,
// a Rekor v1-like log key that signs entries (SET) and checkpoints, a Rekor
// v2-like (rekor-tiles) Ed25519 log that signs checkpoints only, and an RFC
// 3161 timestamp authority. It writes the matching trusted_root.json,
// bundles v0.1 to v0.3 (DSSE or message signature) attached as OCI
// referrers, and cosign 2 keyless signatures under the .sig tag. BOOTy
// itself does not import it.
//
//nolint:wrapcheck,gocritic,cyclop,gocognit,nestif,funlen // test fixture: errors go straight to t.Fatal, options are passed by value for readable tests, one flat function per artifact
package sigstoretest

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // ESS signingCertificate v1 uses SHA-1
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const (
	rekorOrigin = "rekor.localhost"
	// RekorV2Origin is the checkpoint origin of the Rekor v2 log.
	RekorV2Origin = "log2025.rekor.localhost"
	inTotoType    = "application/vnd.in-toto+json"
	// BundleMediaType is the media type of a cosign 3 bundle.
	BundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
	// CosignSignPredicateType is the predicate type `cosign sign` writes.
	CosignSignPredicateType = "https://sigstore.dev/cosign/sign/v1"
)

var (
	oidIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidSCTList  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
	oidSHA256   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
)

// Bundle media types, v0.1 to v0.3; v0.3 also has BundleMediaType.
const (
	BundleMediaTypeV01 = "application/vnd.dev.sigstore.bundle+json;version=0.1"
	BundleMediaTypeV02 = "application/vnd.dev.sigstore.bundle+json;version=0.2"
	BundleMediaTypeV03 = "application/vnd.dev.sigstore.bundle+json;version=0.3"
)

// Sigstore holds the keys of the local CA and logs.
type Sigstore struct {
	rootCert  *x509.Certificate
	interCert *x509.Certificate
	interKey  *ecdsa.PrivateKey
	ctKey     *ecdsa.PrivateKey
	rekorKey  *ecdsa.PrivateKey
	rekorV2   ed25519.PrivateKey
	tsa       *tsa
	start     time.Time
}

// tsa is a timestamp authority: a root and a timestamping certificate.
type tsa struct {
	root, cert *x509.Certificate
	key        *ecdsa.PrivateKey
}

// Options control one signature. Identity and Issuer sign correctly; the
// other fields break or shift one part.
type Options struct {
	Identity string // URI SAN when it has a scheme, else e-mail SAN
	Issuer   string // OIDC issuer extension
	// SignedAt is when the certificate is issued and the log integrates
	// the entry; zero is now.
	SignedAt time.Time
	// IntegratedAt overrides the integrated time of the log entry.
	IntegratedAt time.Time
	// Predicate overrides the statement predicate type.
	Predicate string
	// NoSCT issues the certificate without an SCT.
	NoSCT bool
	// NoInclusionProof leaves the inclusion proof out of the bundle.
	NoInclusionProof bool
	// BadInclusionProof breaks the root hash of the inclusion proof.
	BadInclusionProof bool
	// BadSET breaks the signed entry timestamp.
	BadSET bool
	// NoSET leaves the signed entry timestamp out of a Rekor v1 entry.
	NoSET bool

	// MediaType is the bundle media type; default BundleMediaType (v0.3).
	MediaType string
	// CertificateChain puts the certificate in x509CertificateChain, as
	// bundles before v0.3 do.
	CertificateChain bool
	// MessageSignature signs the image digest (as `cosign sign-blob` signs
	// the manifest) instead of a DSSE in-toto statement.
	MessageSignature bool
	// RekorV2 logs a hashedrekord 0.0.2 entry in the Rekor v2 log: no SET,
	// an inclusion proof in a tree of 3 and a signed note checkpoint.
	RekorV2 bool
	// BadCheckpoint breaks the checkpoint signature.
	BadCheckpoint bool
	// WrongLogKey signs the checkpoint with another key under the same
	// name and key hint.
	WrongLogKey bool
	// BadProofHash breaks a hash of the inclusion proof.
	BadProofHash bool

	// Timestamp adds an RFC 3161 timestamp of the signature.
	Timestamp bool
	// TimestampAt is its time; zero is the signing time.
	TimestampAt time.Time
	// BadTimestampImprint stamps another hash than the signature's.
	BadTimestampImprint bool
	// UntrustedTSA stamps with an authority that is not in the trusted root.
	UntrustedTSA bool
	// TimestampWithoutCert leaves the TSA certificate out of the token; the
	// trusted root lists it.
	TimestampWithoutCert bool
	// TimestampESS selects the signed attribute naming the TSA certificate:
	// "" signingCertificateV2 (SHA-256), "v1" signingCertificate (SHA-1),
	// "none" leaves it out, "wrong" names another certificate.
	TimestampESS string

	// LeafP384 gives the certificate a P-384 key that still signs with
	// SHA-256, which a verifier must refuse.
	LeafP384 bool
	// NoDigitalSignature issues the certificate without the
	// digitalSignature key usage.
	NoDigitalSignature bool
}

// New creates the CA and the log keys, valid from a day ago.
func New() (*Sigstore, error) {
	s := &Sigstore{start: time.Now().Add(-24 * time.Hour).Truncate(time.Second)}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	ca := func(serial int64, cn string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn, Organization: []string{"sigstoretest"}},
			NotBefore: s.start, NotAfter: s.start.Add(10 * 365 * 24 * time.Hour),
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true, IsCA: true,
		}
	}
	rootTmpl := ca(1, "sigstoretest root")
	if s.rootCert, err = createCert(rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey); err != nil {
		return nil, err
	}
	if s.interKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	interTmpl := ca(2, "sigstoretest intermediate")
	interTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	interTmpl.MaxPathLenZero = true
	if s.interCert, err = createCert(interTmpl, s.rootCert, &s.interKey.PublicKey, rootKey); err != nil {
		return nil, err
	}
	if s.ctKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	if s.rekorKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	if _, s.rekorV2, err = ed25519.GenerateKey(rand.Reader); err != nil {
		return nil, err
	}
	if s.tsa, err = newTSA(s.start); err != nil {
		return nil, err
	}
	return s, nil
}

// newTSA creates a timestamp authority valid from start: a root CA and a
// leaf with the only, critical, extended key usage timeStamping.
func newTSA(start time.Time) (*tsa, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	t := &tsa{}
	if t.key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	rootTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sigstoretest tsa root"},
		NotBefore: start, NotAfter: start.Add(10 * 365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	if t.root, err = createCert(rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey); err != nil {
		return nil, err
	}
	eku, err := asn1.Marshal([]asn1.ObjectIdentifier{{1, 3, 6, 1, 5, 5, 7, 3, 8}})
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "sigstoretest tsa"},
		NotBefore: start, NotAfter: start.Add(10 * 365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 37}, Critical: true, Value: eku}},
	}
	if t.cert, err = createCert(leafTmpl, t.root, &t.key.PublicKey, rootKey); err != nil {
		return nil, err
	}
	return t, nil
}

// TrustedRootJSON returns the trusted_root.json of the CA and the logs.
func (s *Sigstore) TrustedRootJSON() ([]byte, error) {
	validFor := map[string]string{"start": s.start.UTC().Format(time.RFC3339)}
	tlog := func(key *ecdsa.PrivateKey, baseURL string) (map[string]any, error) {
		der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return nil, err
		}
		id := sha256.Sum256(der)
		return map[string]any{
			"baseUrl": baseURL, "hashAlgorithm": "SHA2_256",
			"publicKey": map[string]any{"rawBytes": der, "keyDetails": "PKIX_ECDSA_P256_SHA_256", "validFor": validFor},
			"logId":     map[string]any{"keyId": id[:]},
		}, nil
	}
	rekor, err := tlog(s.rekorKey, "https://"+rekorOrigin)
	if err != nil {
		return nil, err
	}
	ctlog, err := tlog(s.ctKey, "https://ctlog.localhost")
	if err != nil {
		return nil, err
	}
	v2der, err := x509.MarshalPKIXPublicKey(s.rekorV2.Public())
	if err != nil {
		return nil, err
	}
	rekorV2 := map[string]any{
		"baseUrl": "https://" + RekorV2Origin, "hashAlgorithm": "SHA2_256",
		"publicKey": map[string]any{"rawBytes": v2der, "keyDetails": "PKIX_ED25519", "validFor": validFor},
		"logId":     map[string]any{"keyId": s.rekorV2KeyHash()},
	}
	return json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.trustedroot+json;version=0.1",
		"tlogs":     []any{rekor, rekorV2},
		"timestampAuthorities": []any{map[string]any{
			"subject":   map[string]string{"organization": "sigstoretest", "commonName": "sigstoretest tsa"},
			"uri":       "https://tsa.localhost/api/v1/timestamp",
			"certChain": map[string]any{"certificates": []any{map[string]any{"rawBytes": s.tsa.cert.Raw}, map[string]any{"rawBytes": s.tsa.root.Raw}}},
			"validFor":  validFor,
		}},
		"certificateAuthorities": []any{map[string]any{
			"subject": map[string]string{"organization": "sigstoretest", "commonName": "sigstoretest"},
			"uri":     "https://fulcio.localhost",
			"certChain": map[string]any{"certificates": []any{
				map[string]any{"rawBytes": s.interCert.Raw}, map[string]any{"rawBytes": s.rootCert.Raw},
			}},
			"validFor": validFor,
		}},
		"ctlogs": []any{ctlog},
	})
}

// Bundle returns a bundle that signs the image digest ("sha256:<hex>"): by
// default as cosign 3 does (v0.3, DSSE envelope of an in-toto statement,
// Rekor v1 entry), changed by o.
func (s *Sigstore) Bundle(digest string, o Options) ([]byte, error) {
	algo, hexDigest, ok := strings.Cut(digest, ":")
	if !ok {
		return nil, fmt.Errorf("digest %q has no algorithm", digest)
	}
	cert, key, err := s.leaf(o)
	if err != nil {
		return nil, err
	}
	bundle := map[string]any{"mediaType": BundleMediaType}
	if o.MediaType != "" {
		bundle["mediaType"] = o.MediaType
	}
	var sig, v2Digest, body []byte
	kind := "dsse"
	if o.MessageSignature {
		raw, err := hex.DecodeString(hexDigest)
		if err != nil {
			return nil, err
		}
		if sig, err = ecdsa.SignASN1(rand.Reader, key, raw); err != nil {
			return nil, err
		}
		bundle["messageSignature"] = map[string]any{
			"messageDigest": map[string]any{"algorithm": "SHA2_256", "digest": raw}, "signature": sig,
		}
		v2Digest, kind = raw, "hashedrekord"
		body, err = hashedRekordV1(hexDigest, sig, cert)
		if err != nil {
			return nil, err
		}
	} else {
		predicate := o.Predicate
		if predicate == "" {
			predicate = CosignSignPredicateType
		}
		payload, err := json.Marshal(map[string]any{
			"_type":         "https://in-toto.io/Statement/v1",
			"subject":       []any{map[string]any{"digest": map[string]string{algo: hexDigest}}},
			"predicateType": predicate,
			"predicate":     map[string]any{},
		})
		if err != nil {
			return nil, err
		}
		pae := fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(inTotoType), inTotoType, len(payload), payload)
		if sig, err = signSHA256(key, pae); err != nil {
			return nil, err
		}
		h := sha256.Sum256(pae)
		v2Digest = h[:]
		envelope := map[string]any{"payload": payload, "payloadType": inTotoType, "signatures": []any{map[string]any{"sig": sig}}}
		bundle["dsseEnvelope"] = envelope
		envJSON, err := json.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		body, err = json.Marshal(map[string]any{
			"apiVersion": "0.0.1", "kind": "dsse",
			"spec": map[string]any{
				"envelopeHash": map[string]string{"algorithm": "sha256", "value": sha256Hex(envJSON)},
				"payloadHash":  map[string]string{"algorithm": "sha256", "value": sha256Hex(payload)},
				"signatures":   []any{map[string]any{"signature": sig, "verifier": certPEM(cert)}},
			},
		})
		if err != nil {
			return nil, err
		}
	}
	var entry map[string]any
	if o.RekorV2 {
		entry, err = s.logEntryV2(cert, sig, v2Digest, o)
	} else {
		entry, err = s.logEntry(kind, body, o, !o.NoInclusionProof)
	}
	if err != nil {
		return nil, err
	}
	vm := map[string]any{"tlogEntries": []any{entry}}
	if o.CertificateChain {
		vm["x509CertificateChain"] = map[string]any{"certificates": []any{map[string]any{"rawBytes": cert.Raw}}}
	} else {
		vm["certificate"] = map[string]any{"rawBytes": cert.Raw}
	}
	if o.Timestamp {
		ts, err := s.timestamp(sig, o)
		if err != nil {
			return nil, err
		}
		vm["timestampVerificationData"] = map[string]any{"rfc3161Timestamps": []any{map[string]any{"signedTimestamp": ts}}}
	}
	bundle["verificationMaterial"] = vm
	return json.Marshal(bundle)
}

func hashedRekordV1(hexDigest string, sig []byte, cert *x509.Certificate) ([]byte, error) {
	return json.Marshal(map[string]any{
		"apiVersion": "0.0.1", "kind": "hashedrekord",
		"spec": map[string]any{
			"data":      map[string]any{"hash": map[string]string{"algorithm": "sha256", "value": hexDigest}},
			"signature": map[string]any{"content": sig, "publicKey": map[string]any{"content": certPEM(cert)}},
		},
	})
}

// Legacy returns the layer annotations of a cosign 2 keyless signature of
// payload under the .sig tag: signature, certificate, chain and Rekor
// bundle (entry with SET, no inclusion proof).
func (s *Sigstore) Legacy(payload []byte, o Options) (map[string]string, error) {
	cert, key, err := s.leaf(o)
	if err != nil {
		return nil, err
	}
	sig, err := signSHA256(key, payload)
	if err != nil {
		return nil, err
	}
	body, err := hashedRekordV1(sha256Hex(payload), sig, cert)
	if err != nil {
		return nil, err
	}
	entry, err := s.logEntry("hashedrekord", body, o, false)
	if err != nil {
		return nil, err
	}
	rb, err := json.Marshal(map[string]any{
		"SignedEntryTimestamp": entry["inclusionPromise"].(map[string]any)["signedEntryTimestamp"],
		"Payload": map[string]any{
			"body": body, "integratedTime": signedAtOrIntegrated(o),
			"logIndex": int64(0), "logID": hex.EncodeToString(entry["logId"].(map[string]any)["keyId"].([]byte)),
		},
	})
	if err != nil {
		return nil, err
	}
	ann := map[string]string{
		"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(sig),
		"dev.sigstore.cosign/certificate":    string(certPEM(cert)),
		"dev.sigstore.cosign/chain":          string(certPEM(s.interCert)) + string(certPEM(s.rootCert)),
		"dev.sigstore.cosign/bundle":         string(rb),
	}
	if o.Timestamp {
		ts, err := s.timestamp(sig, o)
		if err != nil {
			return nil, err
		}
		tsJSON, err := json.Marshal(map[string]any{"SignedRFC3161Timestamp": ts})
		if err != nil {
			return nil, err
		}
		ann["dev.sigstore.cosign/rfc3161timestamp"] = string(tsJSON)
	}
	return ann, nil
}

// leaf issues a certificate for o.Identity and o.Issuer valid from a
// minute before to 9 minutes after the signing time, with an SCT.
func (s *Sigstore) leaf(o Options) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	curve := elliptic.P256()
	if o.LeafP384 {
		curve = elliptic.P384()
	}
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	at := signedAt(o)
	issuerV2, err := asn1.MarshalWithParams(o.Issuer, "utf8")
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, NotBefore: at.Add(-time.Minute), NotAfter: at.Add(9 * time.Minute),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		ExtraExtensions: []pkix.Extension{{Id: oidIssuerV1, Value: []byte(o.Issuer)}, {Id: oidIssuerV2, Value: issuerV2}},
	}
	if o.NoDigitalSignature {
		tmpl.KeyUsage = x509.KeyUsageCertSign
	}
	if u, err := url.Parse(o.Identity); err == nil && u.Scheme != "" {
		tmpl.URIs = []*url.URL{u}
	} else {
		tmpl.EmailAddresses = []string{o.Identity}
	}
	// The certificate without the SCT list is the precertificate that the
	// SCT signs; the final certificate adds the list as its last extension.
	pre, err := createCert(tmpl, s.interCert, &key.PublicKey, s.interKey)
	if err != nil || o.NoSCT {
		return pre, key, err
	}
	sctExt, err := s.sctExtension(pre.RawTBSCertificate, at)
	if err != nil {
		return nil, nil, err
	}
	tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, sctExt)
	cert, err := createCert(tmpl, s.interCert, &key.PublicKey, s.interKey)
	return cert, key, err
}

// sctExtension signs an RFC 6962 SCT over the precertificate TBS and
// returns the SCT list extension.
func (s *Sigstore) sctExtension(tbs []byte, at time.Time) (pkix.Extension, error) {
	der, err := x509.MarshalPKIXPublicKey(&s.ctKey.PublicKey)
	if err != nil {
		return pkix.Extension{}, err
	}
	logID := sha256.Sum256(der)
	ts := binary.BigEndian.AppendUint64(nil, uint64(at.UnixMilli())) //nolint:gosec // positive time
	issuerKeyHash := sha256.Sum256(s.interCert.RawSubjectPublicKeyInfo)
	var signed bytes.Buffer
	signed.Write([]byte{0, 0}) // v1, certificate_timestamp
	signed.Write(ts)
	signed.Write([]byte{0, 1}) // precert_entry
	signed.Write(issuerKeyHash[:])
	signed.Write([]byte{byte(len(tbs) >> 16), byte(len(tbs) >> 8), byte(len(tbs))})
	signed.Write(tbs)
	signed.Write([]byte{0, 0}) // no extensions
	sig, err := signSHA256(s.ctKey, signed.Bytes())
	if err != nil {
		return pkix.Extension{}, err
	}
	var sct bytes.Buffer
	sct.WriteByte(0)
	sct.Write(logID[:])
	sct.Write(ts)
	sct.Write([]byte{0, 0, 4, 3}) // no extensions, SHA-256, ECDSA
	sct.Write([]byte{byte(len(sig) >> 8), byte(len(sig))})
	sct.Write(sig)
	item := append([]byte{byte(sct.Len() >> 8), byte(sct.Len())}, sct.Bytes()...)
	list := append([]byte{byte(len(item) >> 8), byte(len(item))}, item...)
	value, err := asn1.Marshal(list)
	if err != nil {
		return pkix.Extension{}, err
	}
	return pkix.Extension{Id: oidSCTList, Value: value}, nil
}

// logEntry records body as the only entry of a log: a SET, and with proof
// an inclusion proof with a signed checkpoint. It returns the JSON form of
// a TransparencyLogEntry.
func (s *Sigstore) logEntry(kind string, body []byte, o Options, proof bool) (map[string]any, error) {
	der, err := x509.MarshalPKIXPublicKey(&s.rekorKey.PublicKey)
	if err != nil {
		return nil, err
	}
	id := sha256.Sum256(der)
	integrated := signedAtOrIntegrated(o)
	set, err := signSHA256(s.rekorKey, fmt.Appendf(nil, `{"body":%q,"integratedTime":%d,"logID":%q,"logIndex":0}`,
		base64.StdEncoding.EncodeToString(body), integrated, hex.EncodeToString(id[:])))
	if err != nil {
		return nil, err
	}
	if o.BadSET {
		set[len(set)-1] ^= 0xff
	}
	entry := map[string]any{
		"logIndex": "0", "logId": map[string]any{"keyId": id[:]},
		"kindVersion":       map[string]string{"kind": kind, "version": "0.0.1"},
		"integratedTime":    strconv.FormatInt(integrated, 10), // protojson quotes int64
		"inclusionPromise":  map[string]any{"signedEntryTimestamp": set},
		"canonicalizedBody": body,
	}
	if o.NoSET {
		delete(entry, "inclusionPromise")
		delete(entry, "integratedTime")
	}
	if !proof {
		return entry, nil
	}
	rootHash := sha256.Sum256(append([]byte{0}, body...))
	note := fmt.Sprintf("%s - 1\n1\n%s\n", rekorOrigin, base64.StdEncoding.EncodeToString(rootHash[:]))
	noteSig, err := signSHA256(s.rekorKey, []byte(note))
	if err != nil {
		return nil, err
	}
	checkpoint := fmt.Sprintf("%s\n— %s %s\n", note, rekorOrigin, base64.StdEncoding.EncodeToString(append(id[:4:4], noteSig...)))
	proofRoot := bytes.Clone(rootHash[:])
	if o.BadInclusionProof {
		proofRoot[0] ^= 0xff
	}
	entry["inclusionProof"] = map[string]any{
		"logIndex": "0", "rootHash": proofRoot, "treeSize": "1", "hashes": []any{},
		"checkpoint": map[string]string{"envelope": checkpoint},
	}
	return entry, nil
}

// rekorV2KeyHash is the Rekor v2 log ID: the C2SP signed-note key hash of
// the Ed25519 key under the origin; its first 4 bytes are the key hint.
func (s *Sigstore) rekorV2KeyHash() []byte {
	return ed25519KeyHash(s.rekorV2.Public().(ed25519.PublicKey))
}

func ed25519KeyHash(pub ed25519.PublicKey) []byte {
	h := sha256.Sum256(append([]byte(RekorV2Origin+"\n\x01"), pub...))
	return h[:]
}

// logEntryV2 records a hashedrekord 0.0.2 entry (rekor-tiles) of sig over
// digest at index 1 of a tree of 3, with a signed note checkpoint.
func (s *Sigstore) logEntryV2(cert *x509.Certificate, sig, digest []byte, o Options) (map[string]any, error) {
	b64 := base64.StdEncoding.EncodeToString
	// RFC 8785 canonical JSON: the keys sorted.
	body := fmt.Appendf(nil, `{"apiVersion":"0.0.2","kind":"hashedrekord","spec":{"hashedRekordV002":{"data":{"algorithm":"SHA2_256","digest":%q},`+
		`"signature":{"content":%q,"verifier":{"keyDetails":"PKIX_ECDSA_P256_SHA_256","x509Certificate":{"rawBytes":%q}}}}}}`,
		b64(digest), b64(sig), b64(cert.Raw))
	leafHash := func(b []byte) []byte { h := sha256.Sum256(append([]byte{0}, b...)); return h[:] }
	node := func(l, r []byte) []byte { h := sha256.Sum256(slices.Concat([]byte{1}, l, r)); return h[:] }
	l0, l2 := leafHash([]byte("entry 0")), leafHash([]byte("entry 2"))
	root := node(node(l0, leafHash(body)), l2)
	note := fmt.Sprintf("%s\n3\n%s\n", RekorV2Origin, b64(root))
	key := s.rekorV2
	if o.WrongLogKey {
		var err error
		if _, key, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	noteSig := ed25519.Sign(key, []byte(note))
	if o.BadCheckpoint {
		noteSig[0] ^= 0xff
	}
	hint := s.rekorV2KeyHash()[:4]
	checkpoint := fmt.Sprintf("%s\n— %s %s\n", note, RekorV2Origin, b64(append(hint[:4:4], noteSig...)))
	if o.BadProofHash {
		l0 = bytes.Clone(l0)
		l0[0] ^= 0xff
	}
	entry := map[string]any{
		"logIndex": "1", "logId": map[string]any{"keyId": s.rekorV2KeyHash()},
		"kindVersion":       map[string]string{"kind": "hashedrekord", "version": "0.0.2"},
		"canonicalizedBody": body,
	}
	if !o.NoInclusionProof {
		entry["inclusionProof"] = map[string]any{
			"logIndex": "1", "rootHash": root, "treeSize": "3", "hashes": []any{l0, l2},
			"checkpoint": map[string]string{"envelope": checkpoint},
		}
	}
	return entry, nil
}

// timestamp returns a DER RFC 3161 TimeStampResp over the hash of sig.
func (s *Sigstore) timestamp(sig []byte, o Options) ([]byte, error) {
	authority := s.tsa
	if o.UntrustedTSA {
		var err error
		if authority, err = newTSA(s.start); err != nil {
			return nil, err
		}
	}
	at := o.TimestampAt
	if at.IsZero() {
		at = signedAt(o)
	}
	imprint := sha256.Sum256(sig)
	if o.BadTimestampImprint {
		imprint[0] ^= 0xff
	}
	sha256Alg := pkix.AlgorithmIdentifier{Algorithm: oidSHA256}
	tstInfo, err := asn1.Marshal(struct {
		Version        int
		Policy         asn1.ObjectIdentifier
		MessageImprint struct {
			HashAlgorithm pkix.AlgorithmIdentifier
			HashedMessage []byte
		}
		SerialNumber *big.Int
		GenTime      time.Time `asn1:"generalized"`
	}{
		Version: 1, Policy: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 2},
		MessageImprint: struct {
			HashAlgorithm pkix.AlgorithmIdentifier
			HashedMessage []byte
		}{sha256Alg, imprint[:]},
		SerialNumber: big.NewInt(time.Now().UnixNano()), GenTime: at.UTC().Truncate(time.Second),
	})
	if err != nil {
		return nil, err
	}
	oidTSTInfo := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	contentType, err := asn1.Marshal(struct {
		Type   asn1.ObjectIdentifier
		Values []asn1.ObjectIdentifier `asn1:"set"`
	}{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}, []asn1.ObjectIdentifier{oidTSTInfo}})
	if err != nil {
		return nil, err
	}
	tstHash := sha256.Sum256(tstInfo)
	messageDigest, err := asn1.Marshal(struct {
		Type   asn1.ObjectIdentifier
		Values [][]byte `asn1:"set"`
	}{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}, [][]byte{tstHash[:]}})
	if err != nil {
		return nil, err
	}
	attrs := [][]byte{contentType, messageDigest}
	if o.TimestampESS != "none" {
		ess, err := essAttribute(authority.cert, o.TimestampESS)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, ess)
	}
	slices.SortFunc(attrs, bytes.Compare) // DER SET OF
	signedAttrs, err := tlv(asn1.ClassUniversal, asn1.TagSet, attrs...)
	if err != nil {
		return nil, err
	}
	attrSig, err := signSHA256(authority.key, signedAttrs)
	if err != nil {
		return nil, err
	}
	implicitAttrs, err := tlv(asn1.ClassContextSpecific, 0, attrs...)
	if err != nil {
		return nil, err
	}
	sid, err := asn1.Marshal(struct {
		Issuer asn1.RawValue
		Serial *big.Int
	}{asn1.RawValue{FullBytes: authority.cert.RawIssuer}, authority.cert.SerialNumber})
	if err != nil {
		return nil, err
	}
	der := func(v any) []byte { b, _ := asn1.Marshal(v); return b } //nolint:errcheck // static values
	signerInfo, err := tlv(asn1.ClassUniversal, asn1.TagSequence,
		der(1), sid, der(sha256Alg), implicitAttrs,
		der(pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}}), der(attrSig))
	if err != nil {
		return nil, err
	}
	eContent, err := tlv(asn1.ClassContextSpecific, 0, der(tstInfo))
	if err != nil {
		return nil, err
	}
	encap, err := tlv(asn1.ClassUniversal, asn1.TagSequence, der(oidTSTInfo), eContent)
	if err != nil {
		return nil, err
	}
	digestAlgs, err := tlv(asn1.ClassUniversal, asn1.TagSet, der(sha256Alg))
	if err != nil {
		return nil, err
	}
	signerInfos, err := tlv(asn1.ClassUniversal, asn1.TagSet, signerInfo)
	if err != nil {
		return nil, err
	}
	fields := [][]byte{der(3), digestAlgs, encap}
	if !o.TimestampWithoutCert {
		certs, err := tlv(asn1.ClassContextSpecific, 0, authority.cert.Raw)
		if err != nil {
			return nil, err
		}
		fields = append(fields, certs)
	}
	signedData, err := tlv(asn1.ClassUniversal, asn1.TagSequence, append(fields, signerInfos)...)
	if err != nil {
		return nil, err
	}
	content, err := tlv(asn1.ClassContextSpecific, 0, signedData)
	if err != nil {
		return nil, err
	}
	token, err := tlv(asn1.ClassUniversal, asn1.TagSequence, der(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}), content)
	if err != nil {
		return nil, err
	}
	status, err := tlv(asn1.ClassUniversal, asn1.TagSequence, der(0))
	if err != nil {
		return nil, err
	}
	return tlv(asn1.ClassUniversal, asn1.TagSequence, status, token)
}

// essAttribute returns the signed attribute signingCertificateV2 (or, with
// kind "v1", signingCertificate) naming cert, or with kind "wrong" a hash of
// other bytes.
func essAttribute(cert *x509.Certificate, kind string) ([]byte, error) {
	raw := cert.Raw
	if kind == "wrong" {
		raw = []byte("another certificate")
	}
	oid := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
	h := sha256.Sum256(raw)
	certHash := h[:]
	if kind == "v1" {
		oid = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 12}
		h1 := sha1.Sum(raw) //nolint:gosec // as specified
		certHash = h1[:]
	}
	octet, err := asn1.Marshal(certHash)
	if err != nil {
		return nil, err
	}
	// ESSCertIDv2 with the default hashAlgorithm (SHA-256) has only the hash.
	id, err := tlv(asn1.ClassUniversal, asn1.TagSequence, octet)
	if err != nil {
		return nil, err
	}
	certs, err := tlv(asn1.ClassUniversal, asn1.TagSequence, id)
	if err != nil {
		return nil, err
	}
	value, err := tlv(asn1.ClassUniversal, asn1.TagSequence, certs)
	if err != nil {
		return nil, err
	}
	values, err := tlv(asn1.ClassUniversal, asn1.TagSet, value)
	if err != nil {
		return nil, err
	}
	oidDER, err := asn1.Marshal(oid)
	if err != nil {
		return nil, err
	}
	return tlv(asn1.ClassUniversal, asn1.TagSequence, oidDER, values)
}

// tlv encodes a constructed value of class and tag holding children.
func tlv(class, tag int, children ...[]byte) ([]byte, error) {
	return asn1.Marshal(asn1.RawValue{Class: class, Tag: tag, IsCompound: true, Bytes: bytes.Join(children, nil)})
}

// PushBundle attaches bundle to the image at ref as an OCI referrer, as
// cosign 3 does: an empty config, the bundle as the only layer, and the
// bundle media type as the artifact type.
func PushBundle(ref name.Digest, bundle []byte, opts ...remote.Option) error {
	var meta struct {
		MediaType        string          `json:"mediaType"`
		MessageSignature json.RawMessage `json:"messageSignature"`
	}
	if err := json.Unmarshal(bundle, &meta); err != nil {
		return err
	}
	content := "dsse-envelope"
	if meta.MessageSignature != nil {
		content = "message-signature"
	}
	return PushReferrer(ref, meta.MediaType, bundle, map[string]string{
		"dev.sigstore.bundle.content":       content,
		"dev.sigstore.bundle.predicateType": CosignSignPredicateType,
	}, opts...)
}

// PushReferrer attaches content as the only layer of an OCI referrer of
// artifact type artifactType (also the layer media type) to the image at ref.
func PushReferrer(ref name.Digest, artifactType string, content []byte, annotations map[string]string, opts ...remote.Option) error {
	desc, err := remote.Head(ref, opts...)
	if err != nil {
		return err
	}
	layer := static.NewLayer(content, types.MediaType(artifactType))
	config := static.NewLayer([]byte("{}"), "application/vnd.oci.empty.v1+json")
	descs := make([]v1.Descriptor, 0, 2)
	for _, l := range []v1.Layer{config, layer} {
		if err := remote.WriteLayer(ref.Context(), l, opts...); err != nil {
			return err
		}
		d, err := l.Digest()
		if err != nil {
			return err
		}
		size, err := l.Size()
		if err != nil {
			return err
		}
		mt, err := l.MediaType()
		if err != nil {
			return err
		}
		descs = append(descs, v1.Descriptor{MediaType: mt, Digest: d, Size: size})
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": types.OCIManifestSchema1, "artifactType": artifactType,
		"config": descs[0], "layers": descs[1:],
		"subject":     v1.Descriptor{MediaType: desc.MediaType, Digest: desc.Digest, Size: desc.Size},
		"annotations": annotations,
	})
	if err != nil {
		return err
	}
	digest, _, err := v1.SHA256(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return remote.Put(ref.Context().Digest(digest.String()), rawManifest(raw), opts...)
}

// PushLegacy stores a simple-signing payload with annotations under the
// sha256-<hex>.sig tag of ref, as cosign 2 does.
func PushLegacy(ref name.Digest, payload []byte, annotations map[string]string, opts ...remote.Option) error {
	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer:       static.NewLayer(payload, "application/vnd.dev.cosign.simplesigning.v1+json"),
		Annotations: annotations,
	})
	if err != nil {
		return err
	}
	return remote.Write(ref.Context().Tag(strings.Replace(ref.DigestStr(), ":", "-", 1)+".sig"), img, opts...)
}

// SimpleSigningPayload returns the payload cosign 2 signs for digest.
func SimpleSigningPayload(repo, digest string) []byte {
	return fmt.Appendf(nil,
		`{"critical":{"identity":{"docker-reference":%s},"image":{"docker-manifest-digest":%s},"type":"cosign container image signature"},"optional":null}`,
		strconv.Quote(repo), strconv.Quote(digest))
}

type rawManifest []byte

func (r rawManifest) RawManifest() ([]byte, error)        { return r, nil }
func (r rawManifest) MediaType() (types.MediaType, error) { return types.OCIManifestSchema1, nil }

func signedAtOrIntegrated(o Options) int64 {
	if !o.IntegratedAt.IsZero() {
		return o.IntegratedAt.Unix()
	}
	return signedAt(o).Unix()
}

func signedAt(o Options) time.Time {
	if o.SignedAt.IsZero() {
		return time.Now()
	}
	return o.SignedAt
}

func createCert(tmpl, parent *x509.Certificate, pub crypto.PublicKey, key crypto.Signer) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func certPEM(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

func signSHA256(key *ecdsa.PrivateKey, msg []byte) ([]byte, error) {
	h := sha256.Sum256(msg)
	return ecdsa.SignASN1(rand.Reader, key, h[:])
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
