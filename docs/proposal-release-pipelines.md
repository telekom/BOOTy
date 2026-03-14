# Proposal: Production Release Pipelines

## Status: Proposal

## Priority: P0

## Summary

Implement proper release automation: semantic versioning from conventional
commits, release channels (stable/beta/nightly), SBOM generation, vulnerability
scanning, artifact signing, auto-generated changelogs, and a promotion pipeline
with manual approval gates. Produces a full artifact matrix: binary + initramfs
+ ISO for each flavor × architecture.

## Motivation

The current `release.yml` workflow is functional but missing production
essentials:

| Current | Gap |
|---------|-----|
| Manual tag triggers release | No automated version bumping |
| Single release channel | No beta/nightly for testing |
| No SBOM | Supply-chain compliance gap |
| No vulnerability scanning | Unknown CVEs in images |
| No artifact signing | Can't verify artifact integrity |
| Basic changelog | No structured release notes |

### Industry Context

| Project | Release Pipeline |
|---------|-----------------|
| **Kubernetes** | Semantic versioning, krel automation, SBOM, signing |
| **containerd** | GoReleaser + multi-arch + SBOM |
| **Cilium** | Release branches, automated changelog, signing |
| **Talos** | GoReleaser, multi-arch ISO/PXE/AWS artifacts |

## Design

### Release Architecture

```
                    ┌─────────────┐
                    │ Conventional │
                    │   Commits    │
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │   Nightly    │ ← automatic, every night on main
                    │  (v1.2.3-   │
                    │   nightly.N)│
                    └──────┬──────┘
                           │ manual trigger
                    ┌──────▼──────┐
                    │    Beta     │ ← v1.2.3-beta.1
                    │  (testing)  │
                    └──────┬──────┘
                           │ manual approval
                    ┌──────▼──────┐
                    │   Stable    │ ← v1.2.3
                    │ (production)│
                    └─────────────┘
```

### Artifact Matrix

| Artifact | Architectures | Initramfs Flavors |
|----------|--------------|-------------------|
| Binary (`booty`) | amd64, arm64 | N/A |
| Initramfs (`.cpio.gz`) | amd64 | default (FRR), gobgp, slim, micro |
| ISO (`.iso`) | amd64 | default, gobgp |
| Container image | amd64, arm64 | default, gobgp, slim, micro |
| SBOM (`.spdx.json`) | — | Per artifact |
| Signature (`.sig`) | — | Per artifact |
| Checksum (`.sha256`) | — | Per artifact |

### Workflow: Nightly (`nightly.yml`)

```yaml
# .github/workflows/nightly.yml
name: Nightly Build
on:
  schedule:
    - cron: '0 2 * * *'  # 2 AM UTC
  workflow_dispatch:

jobs:
  version:
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.version.outputs.version }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - id: version
        run: |
          BASE=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
          echo "version=${BASE}-nightly.$(date +%Y%m%d)" >> "$GITHUB_OUTPUT"

  build:
    needs: version
    strategy:
      matrix:
        arch: [amd64, arm64]
        flavor: [default, gobgp, slim, micro]
    # ... build each combination ...

  scan:
    needs: build
    steps:
      - name: Vulnerability scan
        uses: aquasecurity/trivy-action@master
        with:
          scan-type: fs
          severity: CRITICAL,HIGH
```

### Workflow: Release (`release-v2.yml`)

```yaml
# .github/workflows/release-v2.yml
name: Release
on:
  push:
    tags: ['v*']

jobs:
  build:
    strategy:
      matrix:
        arch: [amd64, arm64]
        flavor: [default, gobgp, slim, micro]
    steps:
      - name: Build binary
        run: |
          CGO_ENABLED=1 GOOS=linux GOARCH=${{ matrix.arch }} \
          go build -a -ldflags "-linkmode external -extldflags '-static' -s -w \
            -X main.Version=${{ github.ref_name }} \
            -X main.Build=$(date -u +%Y%m%dT%H%M%S)" -o booty

      - name: Build initramfs
        run: |
          docker buildx build --target=${{ matrix.flavor }} \
            --platform=linux/${{ matrix.arch }} \
            -t ghcr.io/${{ github.repository }}:${{ github.ref_name }}-${{ matrix.flavor }} \
            --push .

  sbom:
    needs: build
    steps:
      - name: Generate SBOM
        uses: anchore/sbom-action@v0
        with:
          format: spdx-json
          output-file: sbom.spdx.json

  sign:
    needs: build
    steps:
      - name: Sign with Cosign
        uses: sigstore/cosign-installer@v3
      - run: |
          cosign sign --yes ghcr.io/${{ github.repository }}:${{ github.ref_name }}

  changelog:
    steps:
      - name: Generate changelog
        uses: orhun/git-cliff-action@v3
        with:
          config: cliff.toml

  release:
    needs: [build, sbom, sign, changelog]
    steps:
      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          body_path: CHANGELOG.md
          files: |
            booty-amd64
            booty-arm64
            initramfs-*.cpio.gz
            booty-*.iso
            sbom.spdx.json
            *.sha256
```

### Required Binaries / Tools

| Tool | Purpose | Where Used |
|------|---------|------------|
| `cosign` | Artifact signing (Sigstore) | CI only |
| `trivy` | Vulnerability scanning | CI only |
| `syft` | SBOM generation | CI only |
| `git-cliff` | Changelog from conventional commits | CI only |
| `docker buildx` | Multi-arch container builds | CI only |

**Note**: None of these tools are needed in BOOTy's initramfs. They run
exclusively in CI.

## Files Changed

| File | Change |
|------|--------|
| `.github/workflows/nightly.yml` | New nightly build workflow |
| `.github/workflows/release-v2.yml` | Enhanced release with SBOM/signing/changelog |
| `.github/workflows/promote.yml` | Beta → stable promotion with approval gate |
| `cliff.toml` | git-cliff changelog configuration |
| `.goreleaser.yml` | Optional GoReleaser config (alternative to custom workflows) |
| `Makefile` | `make release-local` for local artifact builds |

## Testing

### Unit Tests

- Version string parsing and formatting
- Changelog template rendering
- Artifact matrix generation logic

### E2E / CI Tests

- **Dry-run release**: Build all artifacts without publishing (runs on
  every PR that touches release workflows)
- **Nightly smoke test**: After nightly build, boot the initramfs in QEMU
  and verify basic functionality
- **Size regression**: Compare artifact sizes against previous release;
  alert on >10% growth

## Risks

| Risk | Mitigation |
|------|------------|
| Signing key compromise | Key stored in GitHub OIDC (keyless Cosign) |
| Nightly build flake | Retry logic; non-blocking (don't page) |
| Large artifact storage | GitHub Releases has 2GB limit; GHCR for containers |
| Conventional commit discipline | Pre-commit hooks + PR title validation |

## Effort Estimate

8–12 engineering days (nightly + release + signing + SBOM + changelog +
promotion pipeline).
