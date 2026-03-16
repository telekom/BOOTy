# Proposal: Binary Optimization — Go Build Slimming

## Status: Phase 1 Implemented (Build Metadata)

## Priority: P2

## Usage (Phase 1)

### Build Info

Import `github.com/telekom/BOOTy/pkg/buildinfo` for build metadata:

```go
info := buildinfo.Get()
fmt.Printf("Version: %s, Commit: %s, Flavor: %s\n", info.Version, info.Commit, info.Flavor)

// JSON output
data, _ := info.JSON()
fmt.Println(string(data))
```

### Setting Build Variables via LDFlags

Use `LDFlags()` to generate the correct `-ldflags` string:

```bash
go build -ldflags "$(go run ./cmd/ldflags v1.2.3 abc123 2026-01-01 gobgp)" -o booty
```

Or directly:

```bash
go build -ldflags "-s -w \
  -X github.com/telekom/BOOTy/pkg/buildinfo.version=v1.2.3 \
  -X github.com/telekom/BOOTy/pkg/buildinfo.commit=abc123 \
  -X github.com/telekom/BOOTy/pkg/buildinfo.buildDate=2026-01-01 \
  -X github.com/telekom/BOOTy/pkg/buildinfo.flavor=gobgp" -o booty
```

### Flavor Constants

Available flavors: `full`, `gobgp`, `slim`, `micro`.

### Size Analysis

```go
components := buildinfo.EstimateComponents()
total := buildinfo.TotalEstimate(components)
fmt.Printf("Estimated binary size: %.1f MB\n", total)
```

### Dependencies

```go
deps := buildinfo.Dependencies()
for _, d := range deps {
    fmt.Printf("%s %s\n", d.Path, d.Version)
}
```

## Summary

Reduce BOOTy's Go binary size and initramfs footprint through: build tag
gating for optional features, dead code elimination analysis, compression
improvements, dependency audit and replacement, statically-linked binary
profiling, and per-flavor binary builds that include only needed code.

## Motivation

BOOTy runs in initramfs where every byte counts. Current sizes:

| Flavor | Approximate Size | Target |
|--------|-----------------|--------|
| micro | ~10 MB | ~6 MB |
| slim | ~15 MB | ~10 MB |
| gobgp | ~40 MB | ~25 MB |
| full | ~80 MB | ~50 MB |

Adding features (Kafka, Cosign, OTel, GoBGP) will grow the binary and
initramfs. This proposal establishes a framework for size management.

### Size Contributors (estimated)

| Component | Binary Size Impact |
|----------|-------------------|
| Go runtime + stdlib | ~5 MB (base) |
| GoBGP v3 | ~8 MB |
| go-containerregistry | ~3 MB |
| sarama (Kafka) | ~2 MB |
| cosign v2 | ~3 MB |
| OpenTelemetry SDK | ~1.5 MB |
| cobra + viper | ~1 MB |
| FRR binaries + libs | ~30 MB |
| Kernel modules | ~10 MB |
| busybox | ~2 MB |
| System tools | ~5 MB |

## Design

### Build Tag Architecture

```
//go:build !micro
  ├─ //go:build !slim
  │   ├─ //go:build kafka        → Kafka handler
  │   ├─ //go:build cosign       → OCI signature verification
  │   ├─ //go:build telemetry    → OpenTelemetry SDK
  │   ├─ //go:build gobgp        → GoBGP networking
  │   └─ //go:build frr          → FRR integration
  └─ //go:build slim
      └─ DHCP + static only, no BGP, no OCI, no Kafka

//go:build micro
  └─ Pure Go: no external binaries, no CGO, DHCP only
```

### Feature Registry

```go
// pkg/features/features.go
package features

// Feature flags determined at compile time via build tags.
// Each optional feature registers itself via init().

var registry = make(map[string]bool)

// Register marks a feature as available.
func Register(name string) {
    registry[name] = true
}

// Enabled checks if a feature was compiled in.
func Enabled(name string) bool {
    return registry[name]
}

// List returns all compiled-in features.
func List() []string {
    var features []string
    for name := range registry {
        features = append(features, name)
    }
    return features
}
```

```go
// pkg/features/kafka.go
//go:build kafka

package features

func init() {
    Register("kafka")
}
```

```go
// pkg/features/cosign.go
//go:build cosign

package features

func init() {
    Register("cosign")
}
```

### Per-Flavor Build Targets

```makefile
# Makefile additions

# Binary variants
.PHONY: build-micro build-slim build-default build-gobgp build-full

build-micro:
	CGO_ENABLED=0 go build -tags micro -ldflags "-s -w" -o booty-micro

build-slim:
	CGO_ENABLED=1 go build -tags slim -ldflags "-linkmode external -extldflags '-static' -s -w" -o booty-slim

build-default:
	CGO_ENABLED=1 go build -ldflags "-linkmode external -extldflags '-static' -s -w" -o booty

build-gobgp:
	CGO_ENABLED=1 go build -tags gobgp -ldflags "-linkmode external -extldflags '-static' -s -w" -o booty-gobgp

build-full:
	CGO_ENABLED=1 go build -tags "gobgp kafka cosign telemetry" -ldflags "-linkmode external -extldflags '-static' -s -w" -o booty-full

# Size analysis
.PHONY: size-report
size-report:
	@echo "=== Binary sizes ==="
	@ls -lh booty-* 2>/dev/null || echo "Build binaries first"
	@echo ""
	@echo "=== Module sizes (booty-full) ==="
	@go tool nm -size booty-full 2>/dev/null | sort -rnk2 | head -30
```

### Dependency Analysis

```go
// cmd/size-analysis/main.go (build-time tool, not shipped)
package main

import (
    "fmt"
    "os/exec"
    "strings"
)

func main() {
    // Use `go tool nm -size <binary>` to analyze symbol sizes
    // Group by package to identify largest contributors
    // Output sorted report
    cmd := exec.Command("go", "tool", "nm", "-size", "booty")
    out, _ := cmd.Output()

    packages := make(map[string]int64)
    for _, line := range strings.Split(string(out), "\n") {
        // Parse: address size type name
        // Extract package prefix from name
        // Aggregate sizes
    }

    for pkg, size := range packages {
        fmt.Printf("%8d KB  %s\n", size/1024, pkg)
    }
}
```

### Compression Improvements

```dockerfile
# initrd.Dockerfile — use zstd compression for initramfs
# Current: gzip (cpio.gz)
# Proposed: zstd level 19 for ~20% better compression

# In the builder stage:
RUN find . | cpio -o -H newc | zstd -19 -T0 > /output/initrd.img.zst

# For gzip fallback (older bootloaders):
RUN find . | cpio -o -H newc | gzip -9 > /output/initrd.img
```

### Required Binaries in Initramfs

No additional binaries needed. This proposal reduces binary count.

### Size Budget per Flavor

| Flavor | Max Binary | Max Initramfs | Features |
|--------|-----------|--------------|----------|
| micro | 8 MB | 10 MB | DHCP only, pure Go |
| slim | 12 MB | 15 MB | DHCP + static |
| default | 20 MB | 50 MB | DHCP + static + FRR |
| gobgp | 18 MB | 40 MB | DHCP + static + GoBGP |
| full | 30 MB | 80 MB | All features |

## Files Changed

| File | Change |
|------|--------|
| `pkg/features/features.go` | Feature registry |
| `pkg/features/kafka.go` | Kafka build tag registration |
| `pkg/features/cosign.go` | Cosign build tag registration |
| `pkg/features/telemetry.go` | OTel build tag registration |
| `pkg/features/gobgp.go` | GoBGP build tag registration |
| `Makefile` | Per-flavor build targets + size-report |
| `initrd.Dockerfile` | Zstd compression option |
| `cmd/booty.go` | Feature-gated initialization |
| `.github/workflows/ci.yml` | Size regression check |

## Testing

### Unit Tests

- `features/features_test.go` — Feature registration and query. Build
  with different tags and verify feature list.
- Size regression test in CI: compare binary sizes against recorded
  baselines; fail if >10% growth without explicit approval.

### CI Validation

- **Size report job**: Build all flavors, record sizes, compare against
  baseline, annotate PR with size diff.
- **Build tag matrix**: Verify each flavor compiles and links correctly.

## Risks

| Risk | Mitigation |
|------|------------|
| Build tag combinatorial explosion | Hierarchical tags (micro ⊂ slim ⊂ default ⊂ full) |
| Feature accidentally compiled out | CI builds and tests all flavors |
| Dependency pulls in large transitive deps | `go mod tidy` + dep analysis in CI |
| Zstd initramfs not supported by bootloader | Provide both .gz and .zst |

## Effort Estimate

6–10 engineering days (build tag architecture + per-flavor builds + 
compression + CI size checks + dependency audit).
