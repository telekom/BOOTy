package secureboot

import (
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"debug/elf"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/telekom/BOOTy/pkg/efi"
	"github.com/ulikunitz/xz"
)

// moduleSigMagic ends a signed kernel module (include/linux/module_signature.h).
const moduleSigMagic = "~Module signature appended~\n"

// moduleSigTrailerLen is the size of struct module_signature.
const moduleSigTrailerLen = 12

// pkeyIDPKCS7 is module_signature.id_type for a PKCS#7/CMS signature, the
// only type the kernel accepts.
const pkeyIDPKCS7 = 2

// moduleSigningKeyBits is the RSA size of the per-host key. RSA is what
// every distribution kernel verifies; 3072 bits is the NIST level for
// keys used past 2030.
const moduleSigningKeyBits = 3072

// maxModuleSize caps a module file and its decompressed form. The image is
// only semi-trusted, and running out of memory kills PID 1. The largest
// distribution modules (GPU drivers) are below 100 MiB uncompressed.
const maxModuleSize = 256 << 20

// ModuleDirs are the directories below /lib/modules/<kernel> that hold
// out-of-tree modules: updates (DKMS on Debian and Ubuntu) and extra (DKMS
// and kmod packages on RHEL-like systems).
var ModuleDirs = []string{"updates", "extra"}

var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
)

// OIDModuleSigningOnly is the extended key usage that limits a key to
// kernel module signing: shim refuses such a MokList certificate for boot
// images.
var OIDModuleSigningOnly = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 2312, 16, 1, 2}

// IsModuleSigningOnly reports whether cert carries OIDModuleSigningOnly.
func IsModuleSigningOnly(cert *x509.Certificate) bool {
	return slices.ContainsFunc(cert.UnknownExtKeyUsage, OIDModuleSigningOnly.Equal)
}

// SignResult is the outcome of SignModuleTree. It holds no key material.
type SignResult struct {
	// CertDER is the DER certificate of the per-host signing key.
	CertDER []byte
	// Modules are the signed module paths, relative to the target root.
	Modules []string
	// Kernels are the kernel versions with signed modules, for depmod.
	Kernels []string
	// SealSignature is the detached signature of the data that the seal
	// callback of SignModulesInto returned.
	SealSignature []byte
}

// SignModuleTree generates a per-host RSA key in memory, signs every module
// below lib/modules/*/{updates,extra} of the target root with it, and
// destroys the key before it returns. The key is never written anywhere and
// never leaves this function; only the certificate is returned. Paths are
// resolved inside rootDir, so a symlink in the image cannot redirect a write
// out of the target.
func SignModuleTree(rootDir, commonName string) (*SignResult, error) {
	return signModules(rootDir, nil, commonName, nil)
}

// SignModulesInto is SignModuleTree for a read-only target: it writes the
// signed copies to usr/<path> below stageDir (lib/modules is usr/lib/modules
// on merged-/usr systems) and leaves rootDir unchanged. When it found
// modules, it then calls seal with the certificate while the key still
// exists, and signs the data that seal returns, typically a dm-verity root
// hash, into SealSignature: a detached CMS signature like the module
// signatures, which the kernel checks for dm-verity root hashes too.
func SignModulesInto(rootDir, stageDir, commonName string, seal func(certDER []byte) ([]byte, error)) (*SignResult, error) {
	stage, err := os.OpenRoot(stageDir)
	if err != nil {
		return nil, fmt.Errorf("open staging directory: %w", err)
	}
	defer func() { _ = stage.Close() }()
	return signModules(rootDir, stage, commonName, seal)
}

func signModules(rootDir string, stage *os.Root, commonName string, seal func([]byte) ([]byte, error)) (*SignResult, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, fmt.Errorf("open target root: %w", err)
	}
	defer func() { _ = root.Close() }()
	modules, err := findModules(root.FS())
	if err != nil {
		return nil, err
	}
	// Check every module before the first write: a module that fails later
	// would leave the earlier ones without their original signatures.
	for _, m := range modules {
		if err := checkModule(root, m); err != nil {
			return nil, fmt.Errorf("module %s: %w", m, err)
		}
	}
	s, err := newModuleSigner(commonName)
	if err != nil {
		return nil, err
	}
	defer s.destroy()
	res := &SignResult{CertDER: s.cert.Raw}
	for _, m := range modules {
		if err := s.signFile(root, m, stage); err != nil {
			return nil, fmt.Errorf("sign module %s: %w", m, err)
		}
		res.Modules = append(res.Modules, m)
		// lib/modules/<kernel>/...
		if k := strings.Split(m, "/")[2]; !slices.Contains(res.Kernels, k) {
			res.Kernels = append(res.Kernels, k)
		}
	}
	if seal != nil && len(modules) > 0 {
		data, err := seal(res.CertDER)
		if err != nil {
			return nil, err
		}
		if res.SealSignature, err = s.signDetached(data); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// findModules returns the module files below lib/modules/*/{updates,extra}.
func findModules(fsys fs.FS) ([]string, error) {
	kernels, err := fs.ReadDir(fsys, "lib/modules")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list kernels: %w", err)
	}
	var modules []string
	for _, k := range kernels {
		if !k.IsDir() {
			continue
		}
		for _, dir := range ModuleDirs {
			base := path.Join("lib/modules", k.Name(), dir)
			err := fs.WalkDir(fsys, base, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.Type().IsRegular() && isModuleName(d.Name()) {
					modules = append(modules, p)
				}
				return nil
			})
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("walk %s: %w", base, err)
			}
		}
	}
	return modules, nil
}

func isModuleName(name string) bool {
	for _, ext := range []string{".ko", ".ko.zst", ".ko.xz", ".ko.gz"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

type moduleSigner struct {
	key  *rsa.PrivateKey
	cert *x509.Certificate
}

func newModuleSigner(commonName string) (*moduleSigner, error) {
	key, err := rsa.GenerateKey(rand.Reader, moduleSigningKeyBits)
	if err != nil {
		return nil, fmt.Errorf("generate module signing key: %w", err)
	}
	s := &moduleSigner{key: key}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		s.destroy()
		return nil, fmt.Errorf("certificate serial: %w", err)
	}
	// A self-signed CA with keyCertSign: kernels with
	// CONFIG_INTEGRITY_CA_MACHINE_KEYRING put only CA certificates from MOK
	// into the .machine keyring that verifies modules, the others accept
	// any. The private key is destroyed after signing, so being a CA lets
	// it vouch for nothing else. The extended key usage limits it to kernel
	// modules, as in the Fedora and Ubuntu MOK certificates. The kernel
	// does not check the validity period of module signing keys.
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		UnknownExtKeyUsage:    []asn1.ObjectIdentifier{OIDModuleSigningOnly},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err == nil {
		s.cert, err = x509.ParseCertificate(der)
	}
	if err != nil {
		s.destroy()
		return nil, fmt.Errorf("create module signing certificate: %w", err)
	}
	return s, nil
}

// destroy overwrites the private key numbers and drops the key.
// ponytail: crypto/rsa keeps an internal precomputed copy of the key that
// is not reachable from outside; building with GOEXPERIMENT=runtimesecret
// and wrapping SignModuleTree in secret.Do would erase that too.
func (s *moduleSigner) destroy() {
	if s.key == nil {
		return
	}
	wipe := func(n *big.Int) {
		if n != nil {
			clear(n.Bits())
			n.SetInt64(0)
		}
	}
	wipe(s.key.D)
	for _, p := range s.key.Primes {
		wipe(p)
	}
	wipe(s.key.Precomputed.Dp)
	wipe(s.key.Precomputed.Dq)
	wipe(s.key.Precomputed.Qinv)
	s.key = nil
}

// readModule returns the open module file and its uncompressed content
// without signature. It refuses anything but an ELF file: the signature is
// a bare RSA signature over the content, and the per-host key must not sign
// image-chosen bytes such as a certificate or Authenticode attributes.
func readModule(root *os.Root, name string, flag int) (*os.File, []byte, error) {
	f, err := root.OpenFile(name, flag, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxModuleSize+1))
	if err == nil && len(data) > maxModuleSize {
		err = fmt.Errorf("larger than %d bytes", maxModuleSize)
	}
	var raw []byte
	if err == nil {
		if raw, err = decompressModule(name, data); err != nil {
			err = fmt.Errorf("decompress: %w", err)
		}
	}
	if err == nil {
		raw, err = StripModuleSignature(raw)
	}
	if err == nil && !bytes.HasPrefix(raw, []byte(elf.ELFMAG)) {
		err = errors.New("not an ELF file")
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, raw, nil
}

// checkModule reports whether signFile would accept the module, without
// changing it.
func checkModule(root *os.Root, name string) error {
	f, _, err := readModule(root, name, os.O_RDONLY)
	if err == nil {
		_ = f.Close()
	}
	return err
}

// signFile signs one module, keeping its compression: in place, or with a
// stage as a copy at usr/<name> in the stage. In place, the file is
// rewritten, not replaced, so its owner, mode and extended attributes (the
// SELinux label modules_object_t on RHEL-like images) stay.
func (s *moduleSigner) signFile(root *os.Root, name string, stage *os.Root) error {
	flag := os.O_RDWR
	if stage != nil {
		flag = os.O_RDONLY
	}
	f, raw, err := readModule(root, name, flag)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	signed, err := s.sign(raw)
	if err != nil {
		return err
	}
	out, err := compressModule(name, signed)
	if err != nil {
		return fmt.Errorf("compress: %w", err)
	}
	if stage != nil {
		dst := path.Join("usr", name)
		if err := stage.MkdirAll(path.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("create staging directory: %w", err)
		}
		if err := stage.WriteFile(dst, out, 0o644); err != nil {
			return fmt.Errorf("write signed copy: %w", err)
		}
		return nil
	}
	if _, err := f.WriteAt(out, 0); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Truncate(int64(len(out))); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// sign returns the module (without signature) with a signature of the
// per-host key appended, in the format of scripts/sign-file:
// module || PKCS#7 || struct module_signature || magic.
func (s *moduleSigner) sign(mod []byte) ([]byte, error) {
	p7, err := s.signDetached(mod)
	if err != nil {
		return nil, err
	}
	var trailer [moduleSigTrailerLen]byte
	trailer[2] = pkeyIDPKCS7
	binary.BigEndian.PutUint32(trailer[8:], uint32(len(p7))) //nolint:gosec // a few hundred bytes
	return slices.Concat(mod, p7, trailer[:], []byte(moduleSigMagic)), nil
}

// signDetached returns the detached CMS signature of data in the format of
// sign-file, which the kernel verifies for modules and dm-verity root hashes.
func (s *moduleSigner) signDetached(data []byte) ([]byte, error) {
	digest := sha256.Sum256(data)
	sig, err := rsa.SignPKCS1v15(nil, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return modulePKCS7(s.cert, sig)
}

// StripModuleSignature returns the module without its appended signature.
// A module without a signature is returned unchanged.
func StripModuleSignature(module []byte) ([]byte, error) {
	if !bytes.HasSuffix(module, []byte(moduleSigMagic)) {
		return module, nil
	}
	end := len(module) - len(moduleSigMagic) - moduleSigTrailerLen
	if end < 0 {
		return nil, errors.New("truncated module signature")
	}
	sigLen := int(binary.BigEndian.Uint32(module[end+8:]))
	if sigLen > end {
		return nil, errors.New("module signature longer than the module")
	}
	return module[:end-sigLen], nil
}

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type signerInfo struct {
	Version            int
	SID                issuerAndSerial
	DigestAlgorithm    algorithmIdentifier
	SignatureAlgorithm algorithmIdentifier
	Signature          []byte
}

type encapContentInfo struct {
	ContentType asn1.ObjectIdentifier
}

type signedData struct {
	Version          int
	DigestAlgorithms []algorithmIdentifier `asn1:"set"`
	ContentInfo      encapContentInfo
	SignerInfos      []signerInfo `asn1:"set"`
}

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     signedData `asn1:"explicit,tag:0"`
}

// modulePKCS7 encodes the detached CMS SignedData that sign-file writes:
// no certificates, no signed attributes, signer by issuer and serial.
func modulePKCS7(cert *x509.Certificate, sig []byte) ([]byte, error) {
	sha := algorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue}
	der, err := asn1.Marshal(contentInfo{
		ContentType: oidSignedData,
		Content: signedData{
			Version:          1,
			DigestAlgorithms: []algorithmIdentifier{sha},
			ContentInfo:      encapContentInfo{ContentType: oidData},
			SignerInfos: []signerInfo{{
				Version:            1,
				SID:                issuerAndSerial{Issuer: asn1.RawValue{FullBytes: cert.RawIssuer}, Serial: cert.SerialNumber},
				DigestAlgorithm:    sha,
				SignatureAlgorithm: algorithmIdentifier{Algorithm: oidRSAEncryption, Parameters: asn1.NullRawValue},
				Signature:          sig,
			}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode module signature: %w", err)
	}
	return der, nil
}

// decompressModule returns the uncompressed module.
//
//nolint:wrapcheck // signFile wraps the error
func decompressModule(name string, data []byte) ([]byte, error) {
	var r io.Reader
	switch {
	case strings.HasSuffix(name, ".zst"):
		dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxModuleSize))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		return dec.DecodeAll(data, nil)
	case strings.HasSuffix(name, ".xz"):
		xr, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		r = xr
	case strings.HasSuffix(name, ".gz"):
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		r = gr
	default:
		return data, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxModuleSize+1))
	if err == nil && len(raw) > maxModuleSize {
		err = fmt.Errorf("decompressed module larger than %d bytes", maxModuleSize)
	}
	return raw, err
}

// compressModule compresses a module like its file name says.
//
//nolint:wrapcheck // signFile wraps the error
func compressModule(name string, raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	var w io.WriteCloser
	var err error
	switch {
	case strings.HasSuffix(name, ".zst"):
		w, err = zstd.NewWriter(&buf)
	case strings.HasSuffix(name, ".xz"):
		// The in-kernel decompressor supports CRC32 checks only.
		w, err = xz.WriterConfig{CheckSum: xz.CRC32, DictCap: 1 << 20}.NewWriter(&buf)
	case strings.HasSuffix(name, ".gz"):
		w = gzip.NewWriter(&buf)
	default:
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MOKOwnerGUID owns the MokList entry of BOOTy. mok-enroll.efi replaces
// every MokList entry with this owner, so a reprovisioned host keeps only
// its newest key and other MOK entries stay untouched.
const MOKOwnerGUID = "11378d35-e20e-4116-94d7-758d7540a17f"

// MOKSignatureList returns certDER as an EFI_SIGNATURE_LIST of one X.509
// certificate owned by MOKOwnerGUID: the payload of mok-enroll.efi.
func MOKSignatureList(certDER []byte) []byte {
	x509Type := guidLE(0xa5c059a1, 0x94e4, 0x4aa7, [8]byte{0x87, 0xb5, 0xab, 0x15, 0x5c, 0x2b, 0xf0, 0x72})
	owner := guidLE(0x11378d35, 0xe20e, 0x4116, [8]byte{0x94, 0xd7, 0x75, 0x8d, 0x75, 0x40, 0xa1, 0x7f})
	sigSize := len(owner) + len(certDER)
	out := append([]byte{}, x509Type...)
	out = binary.LittleEndian.AppendUint32(out, uint32(28+sigSize)) //nolint:gosec // certificate size
	out = binary.LittleEndian.AppendUint32(out, 0)
	out = binary.LittleEndian.AppendUint32(out, uint32(sigSize)) //nolint:gosec // certificate size
	return append(append(out, owner...), certDER...)
}

func guidLE(a uint32, b, c uint16, d [8]byte) []byte {
	out := binary.LittleEndian.AppendUint32(nil, a)
	out = binary.LittleEndian.AppendUint16(out, b)
	out = binary.LittleEndian.AppendUint16(out, c)
	return append(out, d[:]...)
}

// shimLockGUID owns the shim MOK variables.
const shimLockGUID = "605dab50-e046-4300-abb6-3dd810dd8b23"

// QueueMOKEnrollment asks MokManager to enroll certDER at the next boot,
// as mokutil --import --simple-hash does: MokAuth is SHA-256 over MokNew
// and the UCS-2 password, which MokManager asks for once at the console.
// BOOTy writes the variables itself, so no binary of the image runs with
// the password or can queue another certificate.
func QueueMOKEnrollment(vars *efi.EFIVarReader, certDER []byte, password string) error {
	if password == "" || len(password) > 256 {
		return errors.New("the MOK password must have 1 to 256 characters")
	}
	esl := MOKSignatureList(certDER)
	h := sha256.New()
	h.Write(esl)
	for _, c := range password {
		if c < 0x20 || c > 0x7e {
			return errors.New("the MOK password must be printable ASCII, which MokManager can read from the keyboard")
		}
		h.Write([]byte{byte(c), 0})
	}
	// MokAuth first: MokNew alone would be a request without a password.
	const attrs = 0x7 // non-volatile, boot service and runtime access, as mokutil
	if err := vars.WriteVar("MokAuth-"+shimLockGUID, attrs, h.Sum(nil)); err != nil {
		return fmt.Errorf("queue MOK enrollment: %w", err)
	}
	if err := vars.WriteVar("MokNew-"+shimLockGUID, attrs, esl); err != nil {
		return fmt.Errorf("queue MOK enrollment: %w", err)
	}
	return nil
}
