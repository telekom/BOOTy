// Package sigstoretest is a local, offline Sigstore for tests: a Fulcio-like
// CA that issues 10 minute certificates with an embedded SCT, a CT log key
// and a Rekor-like log key that signs entries (SET) and checkpoints. It
// writes the matching trusted_root.json, cosign 3 bundles attached as OCI
// referrers, and cosign 2 keyless signatures under the .sig tag. BOOTy
// itself does not import it.
//
//nolint:wrapcheck,gocritic // test fixture: errors go straight to t.Fatal, options are passed by value for readable tests
package sigstoretest

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	inTotoType  = "application/vnd.in-toto+json"
	// BundleMediaType is the media type of a cosign 3 bundle.
	BundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
	// CosignSignPredicateType is the predicate type `cosign sign` writes.
	CosignSignPredicateType = "https://sigstore.dev/cosign/sign/v1"
)

var (
	oidIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidSCTList  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
)

// Sigstore holds the keys of the local CA and logs.
type Sigstore struct {
	rootCert  *x509.Certificate
	interCert *x509.Certificate
	interKey  *ecdsa.PrivateKey
	ctKey     *ecdsa.PrivateKey
	rekorKey  *ecdsa.PrivateKey
	start     time.Time
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
	return s, nil
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
	return json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.trustedroot+json;version=0.1",
		"tlogs":     []any{rekor},
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

// Bundle returns a cosign 3 bundle (v0.3, DSSE envelope of an in-toto
// statement) that signs the image digest ("sha256:<hex>").
func (s *Sigstore) Bundle(digest string, o Options) ([]byte, error) {
	algo, hexDigest, ok := strings.Cut(digest, ":")
	if !ok {
		return nil, fmt.Errorf("digest %q has no algorithm", digest)
	}
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
	cert, key, err := s.leaf(o)
	if err != nil {
		return nil, err
	}
	sig, err := signSHA256(key, fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(inTotoType), inTotoType, len(payload), payload))
	if err != nil {
		return nil, err
	}
	envelope := map[string]any{"payload": payload, "payloadType": inTotoType, "signatures": []any{map[string]any{"sig": sig}}}
	envJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
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
	entry, err := s.logEntry("dsse", body, o, !o.NoInclusionProof)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"mediaType": BundleMediaType,
		"verificationMaterial": map[string]any{
			"certificate": map[string]any{"rawBytes": cert.Raw},
			"tlogEntries": []any{entry},
		},
		"dsseEnvelope": envelope,
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
	body, err := json.Marshal(map[string]any{
		"apiVersion": "0.0.1", "kind": "hashedrekord",
		"spec": map[string]any{
			"data":      map[string]any{"hash": map[string]string{"algorithm": "sha256", "value": sha256Hex(payload)}},
			"signature": map[string]any{"content": sig, "publicKey": map[string]any{"content": certPEM(cert)}},
		},
	})
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
	return map[string]string{
		"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(sig),
		"dev.sigstore.cosign/certificate":    string(certPEM(cert)),
		"dev.sigstore.cosign/chain":          string(certPEM(s.interCert)) + string(certPEM(s.rootCert)),
		"dev.sigstore.cosign/bundle":         string(rb),
	}, nil
}

// leaf issues a certificate for o.Identity and o.Issuer valid from a
// minute before to 9 minutes after the signing time, with an SCT.
func (s *Sigstore) leaf(o Options) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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

// PushBundle attaches bundle to the image at ref as an OCI referrer, as
// cosign 3 does: an empty config, the bundle as the only layer, and the
// bundle media type as the artifact type.
func PushBundle(ref name.Digest, bundle []byte, opts ...remote.Option) error {
	desc, err := remote.Head(ref, opts...)
	if err != nil {
		return err
	}
	layer := static.NewLayer(bundle, BundleMediaType)
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
		"schemaVersion": 2, "mediaType": types.OCIManifestSchema1, "artifactType": BundleMediaType,
		"config": descs[0], "layers": descs[1:],
		"subject": v1.Descriptor{MediaType: desc.MediaType, Digest: desc.Digest, Size: desc.Size},
		"annotations": map[string]string{
			"dev.sigstore.bundle.content":       "dsse-envelope",
			"dev.sigstore.bundle.predicateType": CosignSignPredicateType,
		},
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
