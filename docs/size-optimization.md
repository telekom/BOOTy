# Binary & Image Size Optimization

## Benchmark Results

Measured on Go 1.26.1 / linux/amd64, UPX 4.2.2.  
All builds use `CGO_ENABLED=1 -linkmode external -extldflags '-static' -s -w` unless noted.

| Variant | Binary (bytes) | Binary (MB) | vs baseline |
|---|---|---|---|
| Baseline (`-s -w -static`) | 23,555,824 | 22.5 MB | — |
| **+ `-trimpath`** ✅ implemented | 23,506,448 | 22.4 MB | −0.2% |
| CGO=0 + `-trimpath` (micro target) | 22,290,594 | 21.3 MB | −5.4% |
| Baseline + UPX `--best` (LZMA) | 9,030,604 | 8.6 MB | −61.7% |
| Baseline + UPX `-9` | 9,121,436 | 8.7 MB | −61.3% |
| CGO=0 + UPX `-9` | 8,284,624 | 7.9 MB | −64.8% |

> Note: the binary is packed inside a `cpio.gz` initramfs. The net gain per
> optimization depends on how compressible the binary is relative to the rest
> of the initramfs contents (FRR binaries, tool binaries, kernel modules).
> `-trimpath` removes host build-system paths from DWARF/debug info — since
> `-s -w` already strips that section, the on-disk effect is small (~49 KB),
> but it improves **reproducible builds** and removes local path leakage from
> the binary.

## Implemented (non-breaking)

### `-trimpath` flag (`-0.2%` binary, reproducible builds)

Added to every `go build` invocation:

- `Makefile` `LDFLAGS`
- `initrd.Dockerfile` — `dev` stage (CGO=1) and `micro-dev` stage (CGO=0)
- `.github/workflows/ci.yml` — all three build steps
- `.github/workflows/release-v2.yml` — release binary build

`-trimpath` removes the absolute build-system paths embedded in the Go
binary's file-name table. With `-s -w` already stripping the full symbol/DWARF
sections the size saving is minor, but it is a best-practice for:
- **Reproducible builds** — output is identical regardless of `$GOPATH` / workspace location
- **No path leakage** — runner home paths (`/home/runner/…`) are not embedded in release artifacts

## Not Implemented (breaking / risky / needs validation)

### UPX compression (`−61%` binary)

UPX can compress the `init` binary from ~22 MB to ~8.7 MB. However:

- **PID 1 risk**: `init` runs as PID 1. The kernel hands control to it directly
  after mounting the initramfs. UPX works by prepending a stub that
  `mmap`s memory, decompresses, and `execv`s the real binary. This requires
  `/proc` (for `/proc/self/exe`) to already be mounted — which PID 1 itself
  is responsible for mounting. This creates a chicken-and-egg problem that
  must be validated with a real QEMU boot before enabling.
- **Initramfs double-compression**: the initramfs is `cpio.gz`. UPX-compressed
  binaries consist largely of already-compressed data, so the outer `gzip`
  gains little on them. The net saving on total initramfs size is smaller than
  the raw binary numbers suggest.
- **Boot latency**: decompression at every boot adds ~50–200 ms per binary
  (negligible, but measurable).
- **UPX `--best` vs `-9`**: `--best` (LZMA) is 0.9% smaller but takes 431 s
  vs 13 s for `-9`; neither is viable for CI without a dedicated cache step.

**Recommended path if you want to pursue UPX:**
1. Add `upx` to the `dev` Docker stage (not CI runner).
2. Compress only non-PID-1 binaries (FRR daemons, tool binaries) where the
   risk is lower.
3. Validate PID 1 UPX in the existing `test-kvm` QEMU boot test before
   enabling for the `init` binary.

### `xz` / `zstd` initramfs compression (`−10–15%` vs gzip)

The `cpio` is currently compressed with `gzip`. Switching to `xz` would
reduce the final initramfs by roughly 10–15% with no runtime penalty (the
kernel decompresses it once at boot). `zstd` offers similar or better ratios
at much faster decompression.

**Why not implemented now**: requires the **kernel** to have been built with
`CONFIG_RD_XZ` / `CONFIG_RD_ZSTD`. The kernel used here is the stock Debian
`linux-image-amd64`, which does support both, but the kernel stage in
`initrd.Dockerfile` would also need updating, and the ISO boot label would
need no changes (the kernel auto-detects compression). The change is low-risk
but touches the kernel stage which is outside the scope of this PR.

### Dead-code / profile-guided optimization (PGO)

Go 1.21+ supports PGO via `-pgo=profile.pprof`. A representative CPU profile
collected during a real boot would allow the compiler to inline hot paths and
de-prioritize cold code. Estimated saving: 3–8% binary size, plus throughput
improvement. Requires instrumenting a boot run to collect a profile first.

### Stripping tool binaries with `strip -s`

The tool binaries copied from Debian packages (`mdadm`, `parted`, `cryptsetup`,
etc.) include symbol tables and are not stripped by default. Running
`strip -s` on them in the `tools` stage before the `ldd` scan would reduce
their size by 20–40% each, saving several MB in the full/gobgp variants.
Non-breaking, but a separate PR to keep the diff reviewable.
