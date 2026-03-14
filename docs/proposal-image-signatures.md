# Proposal: Image Signature Verification

## Status: Implemented

## Priority: P3

## Summary

Verify the cryptographic signature and/or checksum of OS images **before**
writing them to disk. This ensures supply-chain integrity — guaranteeing
that the image downloaded from the image server has not been tampered with
in transit or at rest.

## Motivation

BOOTy downloads OS images over HTTP(S) and streams them directly to disk.
If the image server is compromised, or if a man-in-the-middle attack
modifies the image in transit (e.g., TLS interception proxy with a
compromised CA), a malicious image could be provisioned to bare-metal
machines with full hardware access.

| Attack Vector | Mitigation |
|--------------|------------|
| Compromised image server | GPG/Cosign signature verification |
| MitM on image download | SHA-256 checksum + TLS |
| Compromised build pipeline | Sigstore/Cosign with transparency log |
| Bit rot in storage | Checksum verification |

### Industry Context

| Tool | Image Verification |
|------|--------------------|
| **Ironic** | Supports `instance_info/image_checksum` (MD5/SHA-256/SHA-512); no signature verification |
| **MAAS** | SHA-256 checksums for images in simplestreams; no GPG |
| **Tinkerbell** | No built-in verification |
| **Container registries** | Cosign/Notary for OCI image signatures |

BOOTy already supports OCI image pulls (`pkg/image` has `FetchOCILayer()`
and `IsOCIReference()`). Adding Cosign verification for OCI images would
be a natural extension.

## Design

### Verification Methods

Three tiers of verification, from simplest to most secure:

#### Tier 1: Checksum (P3)

```bash
# /deploy/vars
export IMAGE_CHECKSUM="sha256:a1b2c3d4..."
```

```go
// pkg/image/verify.go
func VerifyChecksum(reader io.Reader, expected string) (io.Reader, error) {
    parts := strings.SplitN(expected, ":", 2)
    algo, hash := parts[0], parts[1]

    var h hash.Hash
    switch algo {
    case "sha256":
        h = sha256.New()
    case "sha512":
        h = sha512.New()
    default:
        return nil, fmt.Errorf("unsupported checksum algorithm: %s", algo)
    }

    // TeeReader computes checksum while streaming to disk
    tee := io.TeeReader(reader, h)
    return &checksumReader{
        Reader:   tee,
        hash:     h,
        expected: hash,
    }, nil
}

type checksumReader struct {
    io.Reader
    hash     hash.Hash
    expected string
}

func (r *checksumReader) Verify() error {
    actual := hex.EncodeToString(r.hash.Sum(nil))
    if actual != r.expected {
        return fmt.Errorf("checksum mismatch: expected %s, got %s", r.expected, actual)
    }
    return nil
}
```

#### Tier 2: GPG Signature (P3)

```bash
# /deploy/vars
export IMAGE_SIGNATURE_URL="https://images.example.com/ubuntu-22.04.img.sig"
export IMAGE_GPG_PUBKEY="/deploy/file-system/image-signing-key.pub"
```

```go
// pkg/image/gpg.go
func VerifyGPGSignature(imageData []byte, signatureData []byte, pubKeyPath string) error {
    keyFile, err := os.Open(pubKeyPath)
    if err != nil {
        return fmt.Errorf("open GPG public key: %w", err)
    }
    defer keyFile.Close()

    keyring, err := openpgp.ReadArmoredKeyRing(keyFile)
    if err != nil {
        return fmt.Errorf("read GPG keyring: %w", err)
    }

    _, err = openpgp.CheckDetachedSignature(
        keyring,
        bytes.NewReader(imageData),
        bytes.NewReader(signatureData),
    )
    return err
}
```

#### Tier 3: Cosign for OCI Images (P4)

For OCI-based image pulls, verify signatures using Sigstore Cosign:

```go
// pkg/image/cosign.go
func VerifyCosignSignature(imageRef string, pubKeyPath string) error {
    pubKey, err := cryptoutils.LoadPublicKey(pubKeyPath)
    if err != nil {
        return fmt.Errorf("load cosign public key: %w", err)
    }

    verifier, err := signature.LoadVerifier(pubKey, crypto.SHA256)
    if err != nil {
        return fmt.Errorf("create verifier: %w", err)
    }

    ref, err := name.ParseReference(imageRef)
    if err != nil {
        return fmt.Errorf("parse image reference: %w", err)
    }

    _, _, err = cosign.VerifyImageSignatures(
        context.Background(), ref,
        &cosign.CheckOpts{SigVerifier: verifier},
    )
    return err
}
```

### Integration

```go
// pkg/provision/orchestrator.go
func (o *Orchestrator) StreamImage(ctx context.Context) error {
    reader, err := image.Fetch(ctx, o.cfg.ImageURL)
    if err != nil {
        return err
    }

    // Wrap with checksum verification if configured
    if o.cfg.ImageChecksum != "" {
        reader, err = image.VerifyChecksum(reader, o.cfg.ImageChecksum)
        if err != nil {
            return fmt.Errorf("setup checksum verification: %w", err)
        }
    }

    // Stream to disk
    if err := image.StreamToDisk(reader, o.targetDisk); err != nil {
        return err
    }

    // Verify checksum after streaming
    if verifier, ok := reader.(*checksumReader); ok {
        if err := verifier.Verify(); err != nil {
            // Wipe the disk — image is compromised
            _ = o.disk.SecureErase(ctx, o.targetDisk)
            return fmt.Errorf("IMAGE INTEGRITY FAILURE: %w", err)
        }
    }

    return nil
}
```

### Security Response

If verification fails:

1. **Abort provisioning immediately**
2. **Wipe the disk** — don't leave a potentially compromised image
3. **Report to CAPRF** with error type `ImageIntegrityFailure`
4. **Set machine phase** to `PhaseFailed` with security annotation
5. **Log at ERROR level** with the expected and actual checksums

## Required Binaries in Initramfs

No additional binaries needed. All verification uses pure Go libraries:

| Go Library | Purpose | Already Used? |
|-----------|---------|--------------|
| `crypto/sha256`, `crypto/sha512` | Checksum computation (Tier 1) | **Yes** (stdlib) |
| `golang.org/x/crypto/openpgp` | GPG signature verification (Tier 2) | **No — add** |
| `sigstore/cosign/v2` | Cosign OCI signature verification (Tier 3) | **No — add** |

**Build tags**: Tier 2 and Tier 3 are optional via `//go:build gpg` and
`//go:build cosign` to avoid pulling large dependencies into slim/micro builds.

## Affected Files

| File | Change |
|------|--------|
| `pkg/image/verify.go` | New — checksum verification |
| `pkg/image/gpg.go` | New — GPG signature verification (Phase 2) |
| `pkg/image/cosign.go` | New — Cosign OCI verification (Phase 3) |
| `pkg/image/image.go` | Integrate verification into `Stream()` |
| `pkg/provision/orchestrator.go` | Wire checksum verification |
| `pkg/config/provider.go` | Add `ImageChecksum`, `ImageSignatureURL`, `ImageGPGPubKey` |
| `go.mod` | Add `golang.org/x/crypto/openpgp` (GPG), `sigstore/cosign` (Cosign) |

## Risks

- **Performance**: Checksum computation adds CPU overhead during streaming.
  SHA-256 on modern CPUs with AES-NI is ~2 GB/s — negligible for typical
  image sizes (5-20 GB).
- **GPG key management**: GPG public keys must be distributed securely via
  CAPRF secrets. Key rotation requires updating all provisioner configs.
- **Cosign dependency**: The `sigstore/cosign` library is large (~30 MB of
  dependencies). Consider a minimal verification-only import.
- **Compressed images**: Checksums must be computed on the compressed stream
  (before decompression) since that's what the image server signs.

## Effort Estimate

- Tier 1 (checksums): **2-3 days**
- Tier 2 (GPG): **3-4 days** — includes key management design
- Tier 3 (Cosign): **5-7 days** — larger dependency, OCI integration
- Total: **10-14 days**
