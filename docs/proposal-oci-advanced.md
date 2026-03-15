# Proposal: Advanced OCI Image Support

## Status: Phase 1 Implemented (PR #51)

## Priority: P1

## Dependencies: [Image Signatures](proposal-image-signatures.md)

## Summary

Extend BOOTy's OCI image support with multi-layer extraction, OCI index
(manifest list) resolution, image signature verification (Cosign/Notation),
OCI artifact caching on target disk, delta/incremental updates via layer
diffing, and OCI-based initramfs self-updates.

## Motivation

BOOTy already supports OCI image pulling via `google/go-containerregistry`.
This proposal extends that foundation for production use:

| Current | Gap |
|---------|-----|
| Single-layer OCI image pull | No multi-layer extraction |
| No platform resolution | Can't select amd64 vs arm64 from manifest list |
| No signature verification | Can't verify image provenance |
| Full image download every time | No incremental updates |
| No image caching | Re-download on retry/reprovision |

### Industry Context

| Tool | OCI Support |
|------|------------|
| **Ironic** | No OCI support (qcow2/raw only) |
| **MAAS** | No OCI support (squashfs/tgz) |
| **Tinkerbell** | Full OCI workflow (images as OCI artifacts) |
| **Flatcar** | OCI-based updates via Nebraska |
| **Kairos** | Full OCI-based OS (container → disk) |

## Design

### Architecture

```
                    ┌──────────────────────┐
                    │   OCI Registry       │
                    │   (GHCR, Harbor, etc) │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │  1. Resolve Index     │ ← Select platform from manifest list
                    │  2. Verify Signature  │ ← Cosign/Notation verification
                    │  3. Pull Manifest     │
                    │  4. Check Cache       │ ← Skip layers already on disk
                    │  5. Pull Layers       │ ← Parallel layer download
                    │  6. Extract to Disk   │ ← Multi-layer overlay → flat filesystem
                    │  7. Update Cache      │ ← Store layer digests for next time
                    └──────────────────────┘
```

### OCI Image Manager

```go
// pkg/image/oci/manager.go
package oci

import (
    "context"
    "fmt"
    "log/slog"
    "runtime"

    "github.com/google/go-containerregistry/pkg/name"
    "github.com/google/go-containerregistry/pkg/v1"
    "github.com/google/go-containerregistry/pkg/v1/remote"
    "github.com/google/go-containerregistry/pkg/v1/types"
)

// Manager handles OCI image operations for OS provisioning.
type Manager struct {
    log       *slog.Logger
    cacheDir  string // target disk cache (e.g., /mnt/target/.booty-cache)
    platform  v1.Platform
}

// Config holds OCI image configuration.
type Config struct {
    Reference     string `json:"reference"`      // "ghcr.io/org/os:v1.2.3"
    Platform      string `json:"platform"`       // "linux/amd64" (auto-detected)
    VerifySignature bool `json:"verifySignature"` // enable Cosign/Notation
    CosignKey     string `json:"cosignKey,omitempty"`
    CacheEnabled  bool   `json:"cacheEnabled"`
    Parallel      int    `json:"parallel"`       // concurrent layer downloads (default: 4)
}

func New(log *slog.Logger, cfg Config) *Manager {
    platform := v1.Platform{
        OS:           "linux",
        Architecture: runtime.GOARCH,
    }
    if cfg.Platform != "" {
        // Parse "linux/amd64" or "linux/arm64"
        platform = parsePlatform(cfg.Platform)
    }
    return &Manager{
        log:      log,
        platform: platform,
    }
}

// Pull resolves, verifies, and extracts an OCI image to the target path.
func (m *Manager) Pull(ctx context.Context, ref string, targetPath string) error {
    // 1. Parse reference
    imgRef, err := name.ParseReference(ref)
    if err != nil {
        return fmt.Errorf("parse OCI reference %s: %w", ref, err)
    }

    // 2. Resolve manifest list → platform-specific manifest
    desc, err := remote.Get(imgRef)
    if err != nil {
        return fmt.Errorf("fetch OCI descriptor: %w", err)
    }

    var img v1.Image
    switch desc.MediaType {
    case types.OCIImageIndex, types.DockerManifestList:
        idx, err := desc.ImageIndex()
        if err != nil {
            return fmt.Errorf("get image index: %w", err)
        }
        img, err = m.resolveFromIndex(idx)
        if err != nil {
            return fmt.Errorf("resolve platform image: %w", err)
        }
    default:
        img, err = desc.Image()
        if err != nil {
            return fmt.Errorf("get image: %w", err)
        }
    }

    // 3. Extract layers to target
    return m.extractImage(ctx, img, targetPath)
}

func (m *Manager) resolveFromIndex(idx v1.ImageIndex) (v1.Image, error) {
    manifest, err := idx.IndexManifest()
    if err != nil {
        return nil, err
    }
    for _, desc := range manifest.Manifests {
        if desc.Platform != nil && desc.Platform.Equals(m.platform) {
            return idx.Image(desc.Digest)
        }
    }
    return nil, fmt.Errorf("no image for platform %s/%s", m.platform.OS, m.platform.Architecture)
}
```

### Signature Verification

```go
// pkg/image/oci/verify.go
package oci

import (
    "context"
    "fmt"

    "github.com/sigstore/cosign/v2/pkg/cosign"
    ociremote "github.com/sigstore/cosign/v2/pkg/oci/remote"
)

// VerifySignature checks the Cosign signature of an OCI image.
func (m *Manager) VerifySignature(ctx context.Context, ref string, publicKey string) error {
    // Use keyless (Fulcio/Rekor) if no key specified
    // Use public key verification if key provided
    checkOpts := &cosign.CheckOpts{}

    if publicKey != "" {
        verifier, err := loadPublicKey(publicKey)
        if err != nil {
            return fmt.Errorf("load verification key: %w", err)
        }
        checkOpts.SigVerifier = verifier
    } else {
        // Keyless verification via Sigstore
        checkOpts.RekorClient = getRekorClient()
    }

    imgRef, err := name.ParseReference(ref)
    if err != nil {
        return fmt.Errorf("parse reference: %w", err)
    }

    _, _, err = cosign.VerifyImageSignatures(ctx, imgRef, checkOpts)
    if err != nil {
        return fmt.Errorf("signature verification failed for %s: %w", ref, err)
    }

    m.log.Info("OCI image signature verified", "ref", ref)
    return nil
}
```

### Layer Caching

```go
// pkg/image/oci/cache.go
package oci

import (
    "encoding/json"
    "os"
    "path/filepath"
)

// LayerCache tracks which layers are already on disk.
type LayerCache struct {
    dir string
}

type CacheManifest struct {
    Layers map[string]LayerMeta `json:"layers"` // digest → metadata
}

type LayerMeta struct {
    Digest      string `json:"digest"`
    Size        int64  `json:"size"`
    ExtractedAt string `json:"extractedAt"`
}

// HasLayer checks if a layer is already extracted on disk.
func (c *LayerCache) HasLayer(digest string) bool {
    manifest := c.loadManifest()
    _, ok := manifest.Layers[digest]
    return ok
}
```

### Persistent Cache Partition

A dedicated cache partition that survives disk wipes and reprovisioning,
so an image layer is pulled at most **once per physical server**
across any number of reprovision cycles.

#### Design

The persistent cache uses a small, labeled GPT partition (default 20 GB)
that BOOTy creates on first boot and **never wipes** during normal
provisioning or deprovisioning operations.

```
┌──────────────────────────────────────────────────────┐
│ Disk Layout (after first BOOTy boot)                 │
│                                                      │
│  Partition 1: EFI System Partition (512 MB)           │
│  Partition 2: OS Root (remainder)                     │
│  Partition N: BOOTY-CACHE (20 GB, ext4, LABEL=bcache)│
│              └─ .booty-cache/                        │
│                  ├─ manifest.json                     │
│                  ├─ layers/                           │
│                  │   ├─ sha256:abc123...  (blob)      │
│                  │   ├─ sha256:def456...  (blob)      │
│                  │   └─ sha256:789012...  (blob)      │
│                  └─ images/                           │
│                      └─ ghcr.io/org/os/              │
│                          └─ manifest-v1.2.3.json     │
└──────────────────────────────────────────────────────┘
```

#### Cache Partition Lifecycle

```go
// pkg/image/oci/persistent.go
package oci

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/exec"
    "path/filepath"
)

const (
    cachePartLabel = "BOOTY-CACHE"
    cacheMountPath = "/mnt/booty-cache"
    cacheSubDir    = ".booty-cache"
    defaultCacheMB = 20480 // 20 GB
)

// PersistentCache manages a dedicated disk partition for OCI layer caching.
// The partition is created once and preserved across wipe/reprovision cycles.
type PersistentCache struct {
    log       *slog.Logger
    mountPath string
    cacheDir  string
}

// EnsureCachePartition finds or creates the cache partition.
// If the BOOTY-CACHE partition already exists on any disk, mount it.
// If not, create it on the root disk's last partition slot.
func EnsureCachePartition(ctx context.Context, log *slog.Logger, rootDisk string) (*PersistentCache, error) {
    // 1. Check if cache partition already exists (by label)
    dev, err := findPartitionByLabel(cachePartLabel)
    if err == nil && dev != "" {
        log.Info("found existing cache partition", "device", dev)
        if err := mountPartition(dev, cacheMountPath); err != nil {
            return nil, fmt.Errorf("mount cache partition: %w", err)
        }
        return newPersistentCache(log), nil
    }

    // 2. Create cache partition on root disk
    log.Info("creating persistent cache partition", "disk", rootDisk, "sizeMB", defaultCacheMB)
    partNum, err := createCachePartition(ctx, rootDisk, defaultCacheMB)
    if err != nil {
        return nil, fmt.Errorf("create cache partition: %w", err)
    }

    // 3. Format as ext4 with label
    partDev := partitionDevice(rootDisk, partNum)
    if err := formatCachePartition(ctx, partDev); err != nil {
        return nil, fmt.Errorf("format cache partition: %w", err)
    }

    // 4. Mount
    if err := mountPartition(partDev, cacheMountPath); err != nil {
        return nil, fmt.Errorf("mount new cache partition: %w", err)
    }

    return newPersistentCache(log), nil
}

func newPersistentCache(log *slog.Logger) *PersistentCache {
    cacheDir := filepath.Join(cacheMountPath, cacheSubDir)
    _ = os.MkdirAll(cacheDir, 0755)
    _ = os.MkdirAll(filepath.Join(cacheDir, "layers"), 0755)
    _ = os.MkdirAll(filepath.Join(cacheDir, "images"), 0755)
    return &PersistentCache{
        log:       log,
        mountPath: cacheMountPath,
        cacheDir:  cacheDir,
    }
}

func findPartitionByLabel(label string) (string, error) {
    return filepath.EvalSymlinks(filepath.Join("/dev/disk/by-label", label))
}

func createCachePartition(ctx context.Context, disk string, sizeMB int) (int, error) {
    // Use sgdisk to add a partition at the end of the disk
    // --new=0:0:+20480M creates a new partition with the next available number
    out, err := exec.CommandContext(ctx, "sgdisk",
        fmt.Sprintf("--new=0:-%dM:0", sizeMB),
        fmt.Sprintf("--change-name=0:%s", cachePartLabel),
        disk).CombinedOutput()
    if err != nil {
        return 0, fmt.Errorf("sgdisk: %s: %w", out, err)
    }
    // Re-read partition table
    _ = exec.CommandContext(ctx, "partprobe", disk).Run()

    // Find the partition number we just created
    dev, err := findPartitionByLabel(cachePartLabel)
    if err != nil {
        return 0, fmt.Errorf("find new partition: %w", err)
    }
    return extractPartitionNumber(dev), nil
}

func formatCachePartition(ctx context.Context, dev string) error {
    return exec.CommandContext(ctx, "mkfs.ext4",
        "-L", cachePartLabel,
        "-m", "0",       // no reserved blocks
        "-q", dev).Run() // quiet mode
}
```

#### Wipe Protection

The cache partition MUST be excluded from disk wipe operations:

```go
// pkg/disk/wipe.go — modified to skip cache partition
func (m *Manager) WipeDisk(ctx context.Context, device string) error {
    partitions, err := m.listPartitions(ctx, device)
    if err != nil {
        return fmt.Errorf("list partitions: %w", err)
    }

    for _, p := range partitions {
        // Skip the persistent OCI cache partition
        if p.Label == "BOOTY-CACHE" {
            slog.Info("preserving cache partition", "partition", p.Device, "label", p.Label)
            continue
        }
        // Wipe all other partitions
        if err := m.wipePartition(ctx, p.Device); err != nil {
            return fmt.Errorf("wipe partition %s: %w", p.Device, err)
        }
    }

    // Recreate partition table, preserving cache partition
    return m.recreateTablePreservingCache(ctx, device)
}
```

Deprovisioning also preserves the cache:

```go
// pkg/provision/orchestrator.go — deprovision step
func (o *Orchestrator) Deprovision(ctx context.Context) error {
    // Wipe OS partitions but preserve BOOTY-CACHE
    if err := o.disk.WipeDisk(ctx, o.rootDisk); err != nil {
        return fmt.Errorf("wipe disk: %w", err)
    }
    o.log.Info("disk wiped, cache partition preserved")
    return nil
}
```

#### Cache-Aware OCI Pull

```go
// pkg/image/oci/manager.go — enhanced Pull with persistent cache
func (m *Manager) PullWithCache(ctx context.Context, ref string, targetPath string, cache *PersistentCache) error {
    imgRef, err := name.ParseReference(ref)
    if err != nil {
        return fmt.Errorf("parse OCI reference %s: %w", ref, err)
    }

    // Check if the entire image is already cached
    if cache.HasImage(ref) {
        m.log.Info("full image cache hit — skipping network pull", "ref", ref)
        return cache.ExtractCachedImage(ctx, ref, targetPath)
    }

    desc, err := remote.Get(imgRef)
    if err != nil {
        return fmt.Errorf("fetch OCI descriptor: %w", err)
    }

    img, err := m.resolveImage(desc)
    if err != nil {
        return err
    }

    layers, err := img.Layers()
    if err != nil {
        return fmt.Errorf("get layers: %w", err)
    }

    // Pull only missing layers
    var pulled, cached int
    for _, layer := range layers {
        digest, _ := layer.Digest()
        if cache.HasLayer(digest.String()) {
            cached++
            m.log.Debug("layer cache hit", "digest", digest.String())
            continue
        }

        // Download and store in persistent cache
        if err := cache.StoreLayer(ctx, layer); err != nil {
            return fmt.Errorf("cache layer %s: %w", digest, err)
        }
        pulled++
    }

    m.log.Info("OCI pull complete",
        "ref", ref,
        "pulled", pulled,
        "cached", cached,
        "total", len(layers))

    // Store image manifest in cache
    if err := cache.StoreImageManifest(ref, img); err != nil {
        m.log.Warn("failed to cache image manifest", "error", err)
    }

    // Extract all layers (from cache) to target
    return cache.ExtractAllLayers(ctx, layers, targetPath)
}
```

#### Cache Management

```go
// pkg/image/oci/persistent.go — cache operations

// HasImage checks if a complete image (all layers) is cached.
func (c *PersistentCache) HasImage(ref string) bool {
    manifestPath := c.imageManifestPath(ref)
    if _, err := os.Stat(manifestPath); err != nil {
        return false
    }
    // Verify all referenced layers exist
    manifest, err := c.loadImageManifest(manifestPath)
    if err != nil {
        return false
    }
    for _, digest := range manifest.LayerDigests {
        if !c.HasLayer(digest) {
            return false
        }
    }
    return true
}

// StoreLayer downloads and stores a layer blob in the cache.
func (c *PersistentCache) StoreLayer(ctx context.Context, layer v1.Layer) error {
    digest, err := layer.Digest()
    if err != nil {
        return fmt.Errorf("get layer digest: %w", err)
    }

    layerPath := filepath.Join(c.cacheDir, "layers", digest.String())
    f, err := os.Create(layerPath)
    if err != nil {
        return fmt.Errorf("create cache file: %w", err)
    }
    defer f.Close()

    rc, err := layer.Compressed()
    if err != nil {
        return fmt.Errorf("get layer reader: %w", err)
    }
    defer rc.Close()

    if _, err := io.Copy(f, rc); err != nil {
        _ = os.Remove(layerPath) // clean up partial download
        return fmt.Errorf("write layer to cache: %w", err)
    }

    // Verify digest
    return c.verifyLayerDigest(layerPath, digest.String())
}

// GarbageCollect removes cached layers not referenced by any stored manifest.
func (c *PersistentCache) GarbageCollect(ctx context.Context) error {
    referenced := c.allReferencedDigests()
    entries, _ := os.ReadDir(filepath.Join(c.cacheDir, "layers"))
    var freed int64
    for _, e := range entries {
        if _, ok := referenced[e.Name()]; !ok {
            path := filepath.Join(c.cacheDir, "layers", e.Name())
            info, _ := e.Info()
            if info != nil {
                freed += info.Size()
            }
            _ = os.Remove(path)
            c.log.Debug("removed stale cached layer", "digest", e.Name())
        }
    }
    c.log.Info("cache garbage collection complete", "freedMB", freed/(1024*1024))
    return nil
}
```

#### Configuration (Persistent Cache)

```bash
# /deploy/vars
export OCI_PERSISTENT_CACHE="true"              # enable persistent cache partition
export OCI_CACHE_SIZE_MB="20480"                 # cache partition size (default 20 GB)
export OCI_CACHE_GC="true"                       # garbage collect unreferenced layers
export OCI_CACHE_FORCE_PULL="false"              # bypass cache (for debugging)
```

### Required Binaries in Initramfs

The persistent cache partition feature requires partition management binaries
(already present for disk provisioning):

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `sgdisk` | `gdisk` | Create cache partition on first boot | all | **Yes** |
| `partprobe` | `parted` | Re-read partition table after cache creation | all | **Yes** |
| `mkfs.ext4` | `e2fsprogs` | Format cache partition | all | **Yes** (via `e2fsck`) |
| `mount` | `util-linux` / busybox | Mount cache partition | all | **Yes** (busybox) |
| `blkid` | `util-linux` / busybox | Find partition by label | all | **Yes** (busybox) |

No new binaries needed. All OCI operations use pure Go libraries:

| Go Library | Purpose | Already Used? |
|-----------|---------|--------------|
| `google/go-containerregistry` | OCI pull/extract | **Yes** |
| `sigstore/cosign/v2` | Signature verification | **No — add** |

### Go Dependencies

| Package | Purpose | Size Impact |
|---------|---------|-------------|
| `github.com/sigstore/cosign/v2` | Cosign verification | ~3 MB |

**Build tag**: `//go:build cosign` — signature verification optional.

### Configuration

```bash
# /deploy/vars
export IMAGE="ghcr.io/org/ubuntu-server:22.04"  # existing
export IMAGE_FORMAT="oci"                         # "oci", "raw", "gzip", "lz4", "xz", "zstd"
export OCI_PLATFORM="linux/amd64"                # auto-detected if empty
export OCI_VERIFY="true"                          # verify Cosign signature
export OCI_COSIGN_KEY="<PEM public key>"         # empty = keyless (Sigstore)
export OCI_CACHE="true"                           # cache layers on target disk
export OCI_PARALLEL="4"                           # concurrent layer downloads
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/image/oci/manager.go` | OCI image manager with index resolution + cache-aware pull |
| `pkg/image/oci/verify.go` | Cosign signature verification |
| `pkg/image/oci/cache.go` | In-memory layer caching |
| `pkg/image/oci/persistent.go` | Persistent cache partition management + layer store |
| `pkg/image/oci/extract.go` | Multi-layer extraction to filesystem |
| `pkg/image/streamer.go` | Integrate OCI manager |
| `pkg/disk/wipe.go` | Skip `BOOTY-CACHE` partition during disk wipe |
| `pkg/config/provider.go` | OCI config fields + persistent cache settings |
| `go.mod` | Add `sigstore/cosign/v2` (optional) |

## Testing

### Unit Tests

- `oci/manager_test.go` — Platform resolution from mock manifest list.
  Table-driven: amd64, arm64, unknown platform, single-platform image.
- `oci/verify_test.go` — Signature verification with test key + signed image.
- `oci/cache_test.go` — Cache hit/miss logic, manifest persistence.

### E2E Tests

- **ContainerLab** (tag `e2e_integration`):
  - Pull multi-arch image from test registry → verify correct platform selected
  - Pull signed image → verify signature passes
  - Pull unsigned image with verify=true → verify clean error

### Persistent Cache Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - Provision → verify cache partition created with `BOOTY-CACHE` label
  - Reprovision same image → verify zero network pull (full cache hit)
  - Reprovision different image → verify only new layers pulled
  - Deprovision → verify cache partition NOT wiped
  - Provision after deprovision → verify cache still intact
  - Cache GC → verify unreferenced layers removed, referenced preserved
  - Cache corruption → verify re-download on digest mismatch

## Risks

| Risk | Mitigation |
|------|------------|
| Cosign library size (+3 MB) | Build tag gating; not in slim/micro |
| Registry auth variations | Use existing go-containerregistry auth chain |
| Layer extraction order matters | Follow OCI spec layer ordering |
| Cache corruption | SHA256 verification on cached layer use |
| Cache partition survives secure erase | By design — use `OCI_CACHE_FORCE_PULL=true` or explicit cache wipe command |
| Cache partition fills up | GC removes unreferenced layers; configurable size; monitor free space |
| Partition creation on first boot adds time | One-time cost (~5s); subsequent boots skip creation |
| Cache on wrong disk (multi-disk servers) | Create on root disk by default; configurable via `OCI_CACHE_DISK` |

## Effort Estimate

12–16 engineering days (manifest resolution + signature + ephemeral caching +
persistent cache partition + wipe protection + GC + multi-layer extraction +
tests).
