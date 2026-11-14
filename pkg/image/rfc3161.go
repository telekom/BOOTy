package image

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// RFC 3161 signed timestamps, verified as sigstore-go and
// timestamp-authority do: a TimeStampResp whose token is CMS SignedData of
// a TSTInfo, signed (over signed attributes) by a timestamping certificate
// that chains to a timestamp authority of the trusted root at genTime;
// the message imprint is the hash of the artifact signature.

var (
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidTSTInfo       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidEKU           = asn1.ObjectIdentifier{2, 5, 29, 37}
)

// timeStampToken is the part of a TimeStampResp that BOOTy checks.
type timeStampToken struct {
	tstInfo     []byte // encapsulated TSTInfo DER
	certs       []*x509.Certificate
	sid         asn1.RawValue
	digest      crypto.Hash // of the signer info
	signedAttrs []byte      // [0] IMPLICIT SET OF Attribute, as encoded
	signature   []byte
	imprintHash crypto.Hash
	imprint     []byte
	genTime     time.Time
}

// verifyRFC3161 verifies the DER TimeStampResp resp over sig against the
// timestamp authorities of r and returns its genTime.
func (r *sigstoreRoot) verifyRFC3161(resp, sig []byte) (time.Time, error) {
	if len(r.tsas) == 0 {
		return time.Time{}, errors.New("the trusted root has no timestamp authority")
	}
	tok, err := parseTimeStampResp(resp)
	if err != nil {
		return time.Time{}, err
	}
	h := tok.imprintHash.New()
	h.Write(sig)
	if !bytes.Equal(h.Sum(nil), tok.imprint) {
		return time.Time{}, errors.New("message imprint is not the hash of the signature")
	}
	if err := tok.verifySignedAttrs(); err != nil {
		return time.Time{}, err
	}
	var errs []error
	for i := range r.tsas {
		if err := tok.verifyBy(&r.tsas[i]); err != nil {
			errs = append(errs, err)
			continue
		}
		return tok.genTime, nil
	}
	return time.Time{}, fmt.Errorf("no timestamp authority of the trusted root signed it: %w", errors.Join(errs...))
}

// verifyBy checks that tsa signed the token at genTime.
func (tok *timeStampToken) verifyBy(tsa *sigstoreCA) error {
	if !tsa.validAt(tok.genTime) {
		return fmt.Errorf("timestamp %s outside the validity of the authority", tok.genTime.UTC().Format(time.RFC3339))
	}
	signer, err := tok.signer(tsa)
	if err != nil {
		return err
	}
	if tsa.leaf != nil && !signer.Equal(tsa.leaf) {
		return errors.New("signer is not the timestamping certificate of the authority")
	}
	opts := x509.VerifyOptions{
		Roots: x509.NewCertPool(), Intermediates: x509.NewCertPool(),
		CurrentTime: tok.genTime, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}
	for _, c := range tsa.roots {
		opts.Roots.AddCert(c)
	}
	for _, c := range append(tsa.intermediates, tok.certs...) {
		opts.Intermediates.AddCert(c)
	}
	if _, err := signer.Verify(opts); err != nil {
		return fmt.Errorf("timestamping certificate: %w", err)
	}
	if err := checkTimestampingLeaf(signer); err != nil {
		return err
	}
	alg, err := cmsSignatureAlgorithm(signer.PublicKey, tok.digest)
	if err != nil {
		return err
	}
	// The signature covers the DER SET OF of the signed attributes.
	signed := bytes.Clone(tok.signedAttrs)
	signed[0] = 0x31
	if err := signer.CheckSignature(alg, signed, tok.signature); err != nil {
		return fmt.Errorf("timestamp signature: %w", err)
	}
	return nil
}

// checkTimestampingLeaf applies RFC 3161, section 2.3: an end-entity
// certificate with the only, critical, extended key usage timeStamping.
func checkTimestampingLeaf(c *x509.Certificate) error {
	critical := false
	for _, ext := range c.Extensions {
		if ext.Id.Equal(oidEKU) {
			critical = ext.Critical
		}
	}
	if c.IsCA || !critical || len(c.UnknownExtKeyUsage) != 0 ||
		len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageTimeStamping {
		return errors.New("timestamping certificate needs the only, critical, extended key usage timeStamping")
	}
	return nil
}

// signer finds the certificate of the signer info among the certificates
// of the token or else the authority's timestamping certificate.
func (tok *timeStampToken) signer(tsa *sigstoreCA) (*x509.Certificate, error) {
	certs := tok.certs
	if len(certs) == 0 && tsa.leaf != nil {
		certs = []*x509.Certificate{tsa.leaf}
	}
	match, err := tok.sidMatcher()
	if err != nil {
		return nil, err
	}
	for _, c := range certs {
		if match(c) {
			return c, nil
		}
	}
	return nil, errors.New("no certificate of the timestamp signer")
}

// sidMatcher matches a certificate against the signer identifier: issuer
// and serial number, or [0] subject key identifier.
func (tok *timeStampToken) sidMatcher() (func(*x509.Certificate) bool, error) {
	switch {
	case tok.sid.Class == asn1.ClassUniversal && tok.sid.Tag == asn1.TagSequence:
		var is struct {
			Issuer asn1.RawValue
			Serial *big.Int
		}
		if rest, err := asn1.Unmarshal(tok.sid.FullBytes, &is); err != nil || len(rest) != 0 {
			return nil, errors.New("malformed signer identifier")
		}
		return func(c *x509.Certificate) bool {
			return bytes.Equal(c.RawIssuer, is.Issuer.FullBytes) && c.SerialNumber.Cmp(is.Serial) == 0
		}, nil
	case tok.sid.Class == asn1.ClassContextSpecific && tok.sid.Tag == 0:
		return func(c *x509.Certificate) bool {
			return len(c.SubjectKeyId) > 0 && bytes.Equal(c.SubjectKeyId, tok.sid.Bytes)
		}, nil
	}
	return nil, errors.New("malformed signer identifier")
}

// verifySignedAttrs checks the content type and message digest attributes
// against the encapsulated TSTInfo.
func (tok *timeStampToken) verifySignedAttrs() error {
	attrs, err := asn1Children(tok.signedAttrs, asn1.ClassContextSpecific, 0)
	if err != nil {
		return fmt.Errorf("signed attributes: %w", err)
	}
	h := tok.digest.New()
	h.Write(tok.tstInfo)
	var contentType, digest bool
	for _, a := range attrs {
		var attr struct {
			Type   asn1.ObjectIdentifier
			Values asn1.RawValue
		}
		if rest, err := asn1.Unmarshal(a, &attr); err != nil || len(rest) != 0 {
			return errors.New("malformed signed attribute")
		}
		values, err := asn1Children(attr.Values.FullBytes, asn1.ClassUniversal, asn1.TagSet)
		if err != nil || len(values) != 1 {
			return errors.New("malformed signed attribute value")
		}
		switch {
		case attr.Type.Equal(oidContentType):
			var oid asn1.ObjectIdentifier
			_, err := asn1.Unmarshal(values[0], &oid)
			contentType = err == nil && oid.Equal(oidTSTInfo)
		case attr.Type.Equal(oidMessageDigest):
			var md []byte
			_, err := asn1.Unmarshal(values[0], &md)
			digest = err == nil && bytes.Equal(md, h.Sum(nil))
		}
	}
	if !contentType || !digest {
		return errors.New("signed attributes do not name the TSTInfo content type and digest")
	}
	return nil
}

// parseTimeStampResp parses TimeStampResp (RFC 3161, section 2.4.2) with
// the token ContentInfo of SignedData (RFC 5652) with one signer info.
func parseTimeStampResp(der []byte) (*timeStampToken, error) {
	sd, err := signedDataOfTimeStampResp(der)
	if err != nil {
		return nil, err
	}
	tok := &timeStampToken{}
	if tok.tstInfo, err = parseEncapsulatedTSTInfo(sd[2]); err != nil {
		return nil, err
	}
	if tok.certs, err = parseCMSCertificates(sd[3 : len(sd)-1]); err != nil {
		return nil, err
	}
	signers, err := asn1Children(sd[len(sd)-1], asn1.ClassUniversal, asn1.TagSet)
	if err != nil || len(signers) != 1 {
		return nil, errors.New("timestamp token needs exactly one signer info")
	}
	if err := tok.parseSignerInfo(signers[0]); err != nil {
		return nil, err
	}
	if err := tok.parseTSTInfo(); err != nil {
		return nil, err
	}
	return tok, nil
}

// signedDataOfTimeStampResp checks the granted status and returns the
// fields of the token's SignedData: version, digestAlgorithms,
// encapContentInfo, [0] certificates, [1] crls, signerInfos.
func signedDataOfTimeStampResp(der []byte) ([][]byte, error) {
	resp, err := asn1Children(der, asn1.ClassUniversal, asn1.TagSequence)
	if err != nil || len(resp) != 2 {
		return nil, errors.New("not a timestamp response with a token")
	}
	status, err := asn1Children(resp[0], asn1.ClassUniversal, asn1.TagSequence)
	var code int
	if err != nil || len(status) == 0 {
		return nil, errors.New("malformed timestamp status")
	}
	if _, err := asn1.Unmarshal(status[0], &code); err != nil || code > 1 { // granted, grantedWithMods
		return nil, fmt.Errorf("timestamp status %d is not granted", code)
	}
	ci, err := asn1Children(resp[1], asn1.ClassUniversal, asn1.TagSequence)
	var ciType asn1.ObjectIdentifier
	if err != nil || len(ci) != 2 {
		return nil, errors.New("malformed timestamp token")
	}
	if _, err := asn1.Unmarshal(ci[0], &ciType); err != nil || !ciType.Equal(oidSignedData) {
		return nil, errors.New("timestamp token is not CMS SignedData")
	}
	wrapped, err := asn1Children(ci[1], asn1.ClassContextSpecific, 0)
	if err != nil || len(wrapped) != 1 {
		return nil, errors.New("malformed timestamp token")
	}
	sd, err := asn1Children(wrapped[0], asn1.ClassUniversal, asn1.TagSequence)
	if err != nil || len(sd) < 4 {
		return nil, errors.New("malformed SignedData")
	}
	return sd, nil
}

// parseCMSCertificates parses the [0] certificates among the optional
// SignedData fields; [1] crls are skipped.
func parseCMSCertificates(fields [][]byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	for _, f := range fields {
		certs, err := asn1Children(f, asn1.ClassContextSpecific, 0)
		if err != nil {
			continue
		}
		for _, der := range certs {
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, fmt.Errorf("timestamp certificate: %w", err)
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func parseEncapsulatedTSTInfo(der []byte) ([]byte, error) {
	encap, err := asn1Children(der, asn1.ClassUniversal, asn1.TagSequence)
	var eType asn1.ObjectIdentifier
	if err != nil || len(encap) != 2 {
		return nil, errors.New("timestamp token has no content")
	}
	if _, err := asn1.Unmarshal(encap[0], &eType); err != nil || !eType.Equal(oidTSTInfo) {
		return nil, errors.New("timestamp token content is not a TSTInfo")
	}
	eContent, err := asn1Children(encap[1], asn1.ClassContextSpecific, 0)
	var tst []byte
	if err != nil || len(eContent) != 1 {
		return nil, errors.New("malformed TSTInfo")
	}
	if rest, err := asn1.Unmarshal(eContent[0], &tst); err != nil || len(rest) != 0 {
		return nil, errors.New("malformed TSTInfo")
	}
	return tst, nil
}

// parseSignerInfo reads version, sid, digestAlgorithm, [0] signedAttrs,
// signatureAlgorithm and signature; signed attributes are required.
func (tok *timeStampToken) parseSignerInfo(der []byte) error {
	si, err := asn1Children(der, asn1.ClassUniversal, asn1.TagSequence)
	if err != nil || len(si) < 6 {
		return errors.New("timestamp signer info has no signed attributes")
	}
	if _, err := asn1.Unmarshal(si[1], &tok.sid); err != nil {
		return errors.New("malformed signer identifier")
	}
	var digestAlg pkix.AlgorithmIdentifier
	if _, err := asn1.Unmarshal(si[2], &digestAlg); err != nil {
		return errors.New("malformed signer digest algorithm")
	}
	if tok.digest = hashByOID(digestAlg.Algorithm); tok.digest == 0 {
		return fmt.Errorf("unsupported signer digest algorithm %s", digestAlg.Algorithm)
	}
	if _, err := asn1Children(si[3], asn1.ClassContextSpecific, 0); err != nil {
		return errors.New("timestamp signer info has no signed attributes")
	}
	tok.signedAttrs = si[3]
	if rest, err := asn1.Unmarshal(si[5], &tok.signature); err != nil || len(rest) != 0 {
		return errors.New("malformed timestamp signature")
	}
	return nil
}

// parseTSTInfo reads version, policy, messageImprint, serialNumber and
// genTime.
func (tok *timeStampToken) parseTSTInfo() error {
	f, err := asn1Children(tok.tstInfo, asn1.ClassUniversal, asn1.TagSequence)
	if err != nil || len(f) < 5 {
		return errors.New("malformed TSTInfo")
	}
	var imprint struct {
		HashAlgorithm pkix.AlgorithmIdentifier
		HashedMessage []byte
	}
	if rest, err := asn1.Unmarshal(f[2], &imprint); err != nil || len(rest) != 0 {
		return errors.New("malformed message imprint")
	}
	if tok.imprintHash = hashByOID(imprint.HashAlgorithm.Algorithm); tok.imprintHash == 0 {
		return fmt.Errorf("unsupported message imprint hash %s", imprint.HashAlgorithm.Algorithm)
	}
	tok.imprint = imprint.HashedMessage
	if rest, err := asn1.UnmarshalWithParams(f[4], &tok.genTime, "generalized"); err != nil || len(rest) != 0 {
		return errors.New("malformed genTime")
	}
	return nil
}

// hashByOID maps SHA-256, SHA-384 and SHA-512 (SHA-1 is too weak); 0 is
// unsupported.
func hashByOID(oid asn1.ObjectIdentifier) crypto.Hash {
	for i, h := range []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512} {
		if oid.Equal(asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, i + 1}) {
			return h
		}
	}
	return 0
}

// cmsSignatureAlgorithm is the x509 signature algorithm of a signer key
// with the digest of the signer info.
func cmsSignatureAlgorithm(key crypto.PublicKey, h crypto.Hash) (x509.SignatureAlgorithm, error) {
	i := map[crypto.Hash]int{crypto.SHA256: 0, crypto.SHA384: 1, crypto.SHA512: 2}[h]
	switch key.(type) {
	case *ecdsa.PublicKey:
		return []x509.SignatureAlgorithm{x509.ECDSAWithSHA256, x509.ECDSAWithSHA384, x509.ECDSAWithSHA512}[i], nil
	case *rsa.PublicKey:
		return []x509.SignatureAlgorithm{x509.SHA256WithRSA, x509.SHA384WithRSA, x509.SHA512WithRSA}[i], nil
	case ed25519.PublicKey:
		return x509.PureEd25519, nil
	}
	return 0, fmt.Errorf("unsupported timestamp signer key %T", key)
}
