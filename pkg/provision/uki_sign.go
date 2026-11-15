//go:build linux

package provision

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf16"
)

// Per-host UKI signing in Go (SECUREBOOT_SIGNING=per-host). No binary of
// the image runs while the private keys exist: an image tool could copy
// them, and with them sign any UKI for this host (#935 stopped running the
// image's mokutil with MOK_PASSWORD for the same reason).
//
//   - ukiPCRSignature is `systemd-measure sign` with its default phases;
//   - appendPESections adds .pcrpkey and .pcrsig as ukify does;
//   - authenticodeSign is `sbsign`.

// ukiMeasuredSections are the UKI sections that systemd-stub measures into
// PCR 11, in its order (UnifiedSection in systemd's uki.h). .pcrsig is not
// measured; UKIs with .profile, .dtbauto, .hwids or .efifw are refused.
var ukiMeasuredSections = []string{"linux", "osrel", "cmdline", "initrd", "ucode", "splash", "dtb", "uname", "sbat", "pcrpkey"}

// ukiPCRPhases are the boot phases that systemd-measure signs by default:
// systemd-pcrextend measures each word into PCR 11.
var ukiPCRPhases = []string{"enter-initrd", "enter-initrd:leave-initrd",
	"enter-initrd:leave-initrd:sysinit", "enter-initrd:leave-initrd:sysinit:ready"}

const (
	tpmCCPolicyPCR = 0x0000017F
	tpmAlgSHA256   = 0x000B
)

// ukiPCRPolicies returns, per phase of ukiPCRPhases, the TPM2 PolicyPCR
// digest of the PCR 11 value that systemd-stub and systemd-pcrextend
// produce for sections: H(0 || CC || TPML_PCR_SELECTION {sha256: PCR 11}
// || H(PCR 11)) from the empty policy.
func ukiPCRPolicies(sections map[string][]byte) [][32]byte {
	extend := func(pcr [32]byte, data []byte) [32]byte {
		h := sha256.Sum256(data)
		return sha256.Sum256(append(pcr[:], h[:]...))
	}
	var pcr [32]byte
	for _, name := range ukiMeasuredSections {
		if b, ok := sections[name]; ok {
			pcr = extend(extend(pcr, append([]byte("."+name), 0)), b)
		}
	}
	pols := make([][32]byte, 0, len(ukiPCRPhases))
	for _, phase := range ukiPCRPhases {
		v := pcr
		for word := range strings.SplitSeq(phase, ":") {
			v = extend(v, []byte(word))
		}
		pcrDigest := sha256.Sum256(v[:])
		var in bytes.Buffer
		in.Write(make([]byte, sha256.Size))
		_ = binary.Write(&in, binary.BigEndian, uint32(tpmCCPolicyPCR))
		_ = binary.Write(&in, binary.BigEndian, uint32(1))
		_ = binary.Write(&in, binary.BigEndian, uint16(tpmAlgSHA256))
		in.Write([]byte{3, 0x00, 0x08, 0x00})
		in.Write(pcrDigest[:])
		pols = append(pols, sha256.Sum256(in.Bytes()))
	}
	return pols
}

// ukiPCRSigEntry is one signed policy in .pcrsig.
type ukiPCRSigEntry struct {
	PCRs []int  `json:"pcrs"`
	PKFP string `json:"pkfp"`
	Pol  string `json:"pol"`
	Sig  string `json:"sig"`
}

// ukiPCRSignature returns the .pcrsig JSON for sections (with .pcrpkey):
// the policies of ukiPCRPolicies signed with key (RSASSA-PKCS1-v1_5,
// SHA-256), as systemd-measure writes them.
func ukiPCRSignature(sections map[string][]byte, key *rsa.PrivateKey) ([]byte, error) {
	// pkfp is the SHA-256 of the PKCS#1 public key (OpenSSL i2d_PublicKey).
	fp := sha256.Sum256(x509.MarshalPKCS1PublicKey(&key.PublicKey))
	var entries []ukiPCRSigEntry
	for _, pol := range ukiPCRPolicies(sections) {
		h := sha256.Sum256(pol[:])
		sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, h[:])
		if err != nil {
			return nil, fmt.Errorf("sign the PCR 11 policy: %w", err)
		}
		entries = append(entries, ukiPCRSigEntry{[]int{11}, hex.EncodeToString(fp[:]), hex.EncodeToString(pol[:]), base64.StdEncoding.EncodeToString(sig)})
	}
	out, err := json.Marshal(map[string][]ukiPCRSigEntry{"sha256": entries})
	if err != nil {
		return nil, fmt.Errorf("encode the PCR 11 signature: %w", err)
	}
	return out, nil
}

// checkUKIInitrdPolicy checks that sigJSON has the PCR 11 policy of the
// enter-initrd phase of sections, signed by pubDER (RSA or ECDSA, as
// systemd-measure signs): the initramfs unseals in that phase. A .pcrsig
// for the later phases only would pass the test unseal of the first-boot
// unit, which runs in userspace, and lock the TPM2 out from the next boot.
func checkUKIInitrdPolicy(sections map[string][]byte, sigJSON, pubDER []byte) error {
	for _, name := range ukiUnmodeledSections {
		if sections[name] != nil {
			return fmt.Errorf("cannot check the PCR 11 policy of a UKI with a .%s section", name)
		}
	}
	pub, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		return fmt.Errorf(".pcrpkey: %w", err)
	}
	var doc struct {
		SHA256 []ukiPCRSigEntry `json:"sha256"`
	}
	if err := json.Unmarshal(bytes.TrimRight(sigJSON, "\x00"), &doc); err != nil {
		return fmt.Errorf(".pcrsig: %w", err)
	}
	pol := ukiPCRPolicies(sections)[0]
	h := sha256.Sum256(pol[:])
	for _, e := range doc.SHA256 {
		sig, err := base64.StdEncoding.DecodeString(e.Sig)
		if err != nil || !slices.Equal(e.PCRs, []int{11}) || e.Pol != hex.EncodeToString(pol[:]) {
			continue
		}
		switch k := pub.(type) {
		case *rsa.PublicKey:
			err = rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig)
		case *ecdsa.PublicKey:
			if !ecdsa.VerifyASN1(k, h[:], sig) {
				err = errors.New("bad ECDSA signature")
			}
		default:
			err = fmt.Errorf("unsupported key type %T", pub)
		}
		if err == nil {
			return nil
		}
	}
	return errors.New(".pcrsig has no policy for the enter-initrd phase of this UKI signed by its .pcrpkey: the initramfs could not unseal")
}

// peLayout holds the header offsets of a PE32+ image.
type peLayout struct {
	coff, opt, secTable, nsec          int
	sectAlign, fileAlign, sizeOfHeader uint32
}

const (
	peOptChecksum     = 64
	peOptSizeOfImage  = 56
	peOptSecurityDir  = 112 + 8*4
	peSectionHdrSize  = 40
	peSectionReadData = 0x40000040 // IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
)

func parsePELayout(b []byte) (*peLayout, error) {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return nil, errors.New("not a PE file")
	}
	lfanew := int(binary.LittleEndian.Uint32(b[0x3c:]))
	if lfanew < 0x40 || lfanew > len(b)-24 || !bytes.Equal(b[lfanew:lfanew+4], []byte("PE\x00\x00")) {
		return nil, errors.New("not a PE file")
	}
	l := &peLayout{coff: lfanew + 4, opt: lfanew + 24}
	l.nsec = int(binary.LittleEndian.Uint16(b[l.coff+2:]))
	l.secTable = l.opt + int(binary.LittleEndian.Uint16(b[l.coff+16:]))
	// The section table follows the data directories, so that the added
	// section headers cannot overwrite the security directory.
	if l.opt+peOptSecurityDir+8 > l.secTable || l.secTable > len(b) || binary.LittleEndian.Uint16(b[l.opt:]) != 0x20b ||
		binary.LittleEndian.Uint32(b[l.opt+108:]) <= 4 || l.secTable+peSectionHdrSize*l.nsec > len(b) {
		return nil, errors.New("not a PE32+ image with a security directory")
	}
	l.sectAlign = binary.LittleEndian.Uint32(b[l.opt+32:])
	l.fileAlign = binary.LittleEndian.Uint32(b[l.opt+36:])
	l.sizeOfHeader = binary.LittleEndian.Uint32(b[l.opt+60:])
	if !peAlignmentOK(l.sectAlign, l.fileAlign) || int(l.sizeOfHeader) > len(b) {
		return nil, errors.New("unexpected PE alignment")
	}
	return l, nil
}

// peAlignmentOK: FileAlignment is a power of 2 up to 64 KiB (8 at least
// for the signature), SectionAlignment one at least as large (PE format).
// FileAlignment pads the added sections.
func peAlignmentOK(sect, file uint32) bool {
	pow2 := func(n uint32) bool { return n&(n-1) == 0 }
	return file >= 8 && file <= 0x10000 && pow2(file) && sect >= file && pow2(sect)
}

func alignUp[T uint32 | uint64](n, a T) T { return (n + a - 1) / a * a }

// appendPESections returns the unsigned PE b with sections appended, as
// ukify adds .pcrpkey and .pcrsig. b must be laid out as ukify writes it:
// the section data right after the headers, without gaps or trailing data,
// so that the Authenticode hash covers every byte except the checksum and
// the security directory.
func appendPESections(b []byte, sections ...[2][]byte) ([]byte, error) {
	l, err := parsePELayout(b)
	if err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(b[l.opt+peOptSecurityDir+4:]) != 0 {
		return nil, errors.New("the PE is signed")
	}
	// 64 bits: offsets and addresses near 4 GiB must not wrap.
	end, vend := uint64(l.sizeOfHeader), uint64(0)
	type span struct{ ptr, size uint32 }
	var raw []span
	for i := range l.nsec {
		h := b[l.secTable+peSectionHdrSize*i:]
		va, vsize := binary.LittleEndian.Uint32(h[12:]), binary.LittleEndian.Uint32(h[8:])
		vend = max(vend, uint64(va)+uint64(max(vsize, binary.LittleEndian.Uint32(h[16:]))))
		if size := binary.LittleEndian.Uint32(h[16:]); size > 0 {
			raw = append(raw, span{binary.LittleEndian.Uint32(h[20:]), size})
		}
	}
	slices.SortFunc(raw, func(a, b span) int { return int(a.ptr) - int(b.ptr) })
	for _, s := range raw {
		if uint64(s.ptr) != end {
			return nil, fmt.Errorf("unexpected PE layout: section data at %#x, want %#x", s.ptr, end)
		}
		end += uint64(s.size)
	}
	if end != uint64(len(b)) {
		return nil, fmt.Errorf("unexpected PE layout: %d bytes after the last section", len(b)-int(end))
	}
	if len(b)%int(l.fileAlign) != 0 {
		return nil, errors.New("unexpected PE layout: the file size is not a multiple of FileAlignment")
	}
	table := l.secTable + peSectionHdrSize*l.nsec
	free := table + peSectionHdrSize*len(sections)
	if free > int(l.sizeOfHeader) || slices.ContainsFunc(b[table:free], func(c byte) bool { return c != 0 }) {
		return nil, errors.New("no room in the PE headers for more sections")
	}
	out := slices.Clone(b)
	va := alignUp(vend, uint64(l.sectAlign))
	for i, s := range sections {
		h := out[table+peSectionHdrSize*i:]
		copy(h[:8], s[0])
		size := uint32(len(s[1])) //nolint:gosec // a public key and a JSON document
		rawSize := alignUp(size, l.fileAlign)
		binary.LittleEndian.PutUint32(h[8:], size)
		binary.LittleEndian.PutUint32(h[12:], uint32(va)) //nolint:gosec // SizeOfImage is checked below
		binary.LittleEndian.PutUint32(h[16:], rawSize)
		binary.LittleEndian.PutUint32(h[20:], uint32(len(out))) //nolint:gosec // below maxUKISize
		binary.LittleEndian.PutUint32(h[36:], peSectionReadData)
		out = append(append(out, s[1]...), make([]byte, rawSize-size)...)
		va = alignUp(va+uint64(size), uint64(l.sectAlign))
	}
	if va > math.MaxUint32 {
		return nil, errors.New("unexpected PE layout: no address space for more sections")
	}
	binary.LittleEndian.PutUint16(out[l.coff+2:], uint16(l.nsec+len(sections))) //nolint:gosec // checked against the header room
	binary.LittleEndian.PutUint32(out[l.opt+peOptSizeOfImage:], uint32(va))
	return out, nil
}

var (
	oidSpcIndirectData  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 4}
	oidSpcPEImageData   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 15}
	oidSpcSpOpusInfo    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 12}
	oidAttrContentType  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidAttrMessageDigst = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidPKCS7SignedData  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidDigestSHA256     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncrypt       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
)

// der encodes one DER TLV with the concatenated contents.
func der(tag byte, contents ...[]byte) []byte {
	body := slices.Concat(contents...)
	n := len(body)
	out := []byte{tag}
	switch {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 0x100:
		out = append(out, 0x81, byte(n))
	case n < 0x10000:
		out = append(out, 0x82, byte(n>>8), byte(n))
	default:
		out = append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	}
	return append(out, body...)
}

func derOID(oid asn1.ObjectIdentifier) []byte {
	b, _ := asn1.Marshal(oid) //nolint:errcheck // constant OIDs
	return b
}

// authenticodeDigest is the SHA-256 Authenticode hash of an unsigned PE
// in the layout of appendPESections: every byte but the checksum and the
// security directory entry.
func authenticodeDigest(b []byte, l *peLayout) [32]byte {
	h := sha256.New()
	cs, sd := l.opt+peOptChecksum, l.opt+peOptSecurityDir
	h.Write(b[:cs])
	h.Write(b[cs+4 : sd])
	h.Write(b[sd+8:])
	return [32]byte(h.Sum(nil))
}

// authenticodeSign signs the unsigned PE b for Secure Boot, as sbsign does:
// a PKCS#7 SignedData over SpcIndirectDataContent with the signer
// certificate, in a WIN_CERTIFICATE appended to the file.
func authenticodeSign(b []byte, key *rsa.PrivateKey, cert *x509.Certificate) ([]byte, error) {
	l, err := parsePELayout(b)
	if err != nil {
		return nil, err
	}
	if len(b)%8 != 0 || binary.LittleEndian.Uint32(b[l.opt+peOptSecurityDir+4:]) != 0 {
		return nil, errors.New("the PE is signed or not 8-byte aligned")
	}
	digest := authenticodeDigest(b, l)
	sha := der(0x30, derOID(oidDigestSHA256), []byte{0x05, 0x00})
	obsolete := utf16.Encode([]rune("<<<Obsolete>>>"))
	bmp := make([]byte, 0, 2*len(obsolete))
	for _, r := range obsolete {
		bmp = binary.BigEndian.AppendUint16(bmp, r)
	}
	// SpcIndirectDataContent { SpcAttributeTypeAndOptionalValue
	// { SPC_PE_IMAGE_DATAOBJ, SpcPeImageData { flags, file [0] { [2] { [0] BMP } } } },
	// DigestInfo }.
	peImageData := der(0x30, []byte{0x03, 0x01, 0x00}, der(0xa0, der(0xa2, der(0x80, bmp))))
	idcBody := slices.Concat(der(0x30, derOID(oidSpcPEImageData), peImageData), der(0x30, sha, der(0x04, digest[:])))
	idc := der(0x30, idcBody)
	// The messageDigest covers the content octets of the SEQUENCE.
	idcSum := sha256.Sum256(idcBody)
	attrs := [][]byte{
		der(0x30, derOID(oidAttrContentType), der(0x31, derOID(oidSpcIndirectData))),
		der(0x30, derOID(oidAttrMessageDigst), der(0x31, der(0x04, idcSum[:]))),
		der(0x30, derOID(oidSpcSpOpusInfo), der(0x31, der(0x30))),
	}
	slices.SortFunc(attrs, bytes.Compare) // DER SET OF order
	signed := sha256.Sum256(der(0x31, attrs...))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, signed[:])
	if err != nil {
		return nil, fmt.Errorf("sign the UKI for Secure Boot: %w", err)
	}
	serial, err := asn1.Marshal(cert.SerialNumber)
	if err != nil {
		return nil, fmt.Errorf("certificate serial: %w", err)
	}
	signerInfo := der(0x30, []byte{0x02, 0x01, 0x01}, der(0x30, cert.RawIssuer, serial), sha,
		der(0xa0, attrs...), der(0x30, derOID(oidRSAEncrypt), []byte{0x05, 0x00}), der(0x04, sig))
	p7 := der(0x30, derOID(oidPKCS7SignedData), der(0xa0, der(0x30,
		[]byte{0x02, 0x01, 0x01}, der(0x31, sha),
		der(0x30, derOID(oidSpcIndirectData), der(0xa0, idc)),
		der(0xa0, cert.Raw),
		der(0x31, signerInfo))))
	// WIN_CERTIFICATE { dwLength, wRevision 0x0200, wCertificateType
	// WIN_CERT_TYPE_PKCS_SIGNED_DATA }, padded to 8 bytes.
	n := alignUp(uint32(8+len(p7)), 8) //nolint:gosec // a few KiB
	wc := binary.LittleEndian.AppendUint32(nil, n)
	wc = binary.LittleEndian.AppendUint16(wc, 0x0200)
	wc = binary.LittleEndian.AppendUint16(wc, 0x0002)
	wc = append(append(wc, p7...), make([]byte, int(n)-8-len(p7))...)
	out := slices.Concat(b, wc)
	binary.LittleEndian.PutUint32(out[l.opt+peOptSecurityDir:], uint32(len(b))) //nolint:gosec // below maxUKISize
	binary.LittleEndian.PutUint32(out[l.opt+peOptSecurityDir+4:], n)
	return out, nil
}
