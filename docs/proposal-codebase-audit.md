# Proposal: Codebase Audit — Bugs, Hardening, Refactoring & Documentation

## Status: Proposal

## Priority: Mixed (P0–P3, see per-item priorities)

## Summary

Comprehensive audit of the BOOTy codebase covering **logic bugs**,
**security hardening**, **error-handling gaps**, **test coverage holes**,
**refactoring opportunities**, and **documentation deficiencies** discovered
through static analysis and manual code review.

Findings are grouped into seven categories. Each item lists its priority,
affected file(s), and a recommended fix.

---

## 1. Logic Bugs & Edge Cases

### 1.1 Secure Erase Never Invoked (P1)

**Files:** `pkg/provision/orchestrator.go`, `pkg/disk/manager.go`

`SecureEraseAllDisks()` is implemented in the disk manager but never called
from the 30-step provisioning pipeline. The `SECURE_ERASE` config variable is
parsed from `/deploy/vars` but the orchestrator only calls `wipeDisks()`
(fast sgdisk + wipefs), not `secureEraseDisks()`. Machines expecting
cryptographic erasure for compliance are silently skipped.

**Fix:** Add a conditional step between `wipe-disks` and `detect-disk`:

```go
if o.cfg.SecureErase {
    if err := o.disk.SecureEraseAllDisks(ctx); err != nil {
        return fmt.Errorf("secure erase: %w", err)
    }
}
```

### 1.2 GoBGP Stack Leaks Underlay on Overlay Failure (P1)

**File:** `pkg/network/gobgp/stack.go`

`Stack.Setup()` starts the underlay tier, then the overlay tier. If
`overlay.Setup()` fails, the method returns an error without tearing down the
already-running underlay (BGP server, router advertisement goroutine, netlink
interfaces). This leaves the host with orphaned resources.

**Fix:** Add deferred cleanup:

```go
if err := s.underlay.Setup(ctx); err != nil {
    return fmt.Errorf("underlay setup: %w", err)
}
defer func() {
    if retErr != nil {
        s.underlay.Teardown()
    }
}()
```

### 1.3 Disk Wipe Always Returns nil (P2)

**File:** `pkg/disk/manager.go`

`WipeAllDisks()` runs sgdisk + wipefs per disk but always returns `nil`, even
if every disk fails to wipe. Provisioning proceeds with unclean disks, which
can cause image streaming to the wrong partition layout or stale RAID
metadata to be detected.

**Fix:** Collect errors per disk and return an aggregated error if all disks
fail. Individual disk wipe failures can remain non-fatal (logged), but total
failure should abort.

### 1.4 Partition Number Extraction Missing Validation (P2)

**File:** `pkg/disk/types.go`

`partNumberFromDevice("/dev/sda")` (called without a partition suffix) returns
an empty string. Downstream callers in `createEFIBootEntry()` pass this to
`efibootmgr --part ""`, producing cryptic failures.

**Fix:** Return an error if the extracted partition number is empty:

```go
num := partNumberFromDevice(dev)
if num == "" {
    return fmt.Errorf("cannot extract partition number from %q", dev)
}
```

### 1.5 VXLAN MTU Underflow Falls Back to Potentially Invalid Value (P2)

**File:** `pkg/network/gobgp/overlay.go`

If MTU is set below the 50-byte VXLAN overhead, the code falls back to 1500.
This fallback may exceed the physical MTU, causing silent packet drops.

**Fix:** Validate `MTU >= vxlanOverhead + 576` (IPv4 minimum) in
`Config.Validate()` and reject invalid configurations early rather than
guessing at runtime.

### 1.6 NIC Wait Loop Hardcoded to 10 Seconds (P3)

**File:** `pkg/network/gobgp/underlay.go`

`waitForNICs()` retries 20 × 500 ms = 10 seconds. In environments with slow
NIC driver initialization (e.g., Mellanox CX-7 with firmware handshake), this
is insufficient. In fast environments, it wastes time sleeping unnecessarily.

**Fix:** Accept timeout via config (`NIC_DETECT_TIMEOUT_SEC`) with a sensible
default (10 s), and use exponential backoff instead of fixed sleep.

### 1.7 GRUB Parser Ignores Submenu Nesting and Multiline Args (P3)

**File:** `pkg/kexec/grub.go`

The line-by-line GRUB parser doesn't handle:

- Nested `submenu { menuentry { } }` blocks (common in Ubuntu's advanced
  menu)
- Backslash-continued kernel argument lines (`linux /vmlinuz ... \`)

This can cause kexec to boot with incomplete kernel arguments.

**Fix:** Track brace depth; join backslash-continued lines before parsing.

### 1.8 Loopback IP Parse Error Silently Ignored (P2)

**File:** `pkg/network/gobgp/overlay.go`

When assigning the overlay IP to the loopback, the code tries `/128` then
falls back to `/32`. If both fail, the error is silently dropped and `addr`
may be nil, causing a nil-pointer dereference in the subsequent
`netlink.AddrAdd()` call.

**Fix:** Return the error explicitly after both parse attempts fail.

---

## 2. Security Hardening

### 2.1 Path Traversal via Machine Files (P1)

**File:** `pkg/provision/configurator.go`

`copyTree()` copies all files from `/deploy/file-system` and
`/deploy/machine-files` recursively into `/newroot` without path validation.
If the ISO contains symlinks or `../` path components, files could be written
outside the root filesystem boundary.

**Fix:** Resolve and validate the destination path:

```go
absRoot, _ := filepath.Abs(destRoot)
absDest, _ := filepath.Abs(destPath)
if !strings.HasPrefix(absDest, absRoot+string(os.PathSeparator)) {
    return fmt.Errorf("path traversal attempt: %q escapes %q", destPath, destRoot)
}
```

### 2.2 Shell Injection in Mellanox Setup (P2)

**File:** `pkg/provision/configurator.go`

`setupMellanox()` constructs shell commands via `fmt.Sprintf("mstconfig -d %s
set NUM_OF_VFS=%d", devPath, numVFs)` and passes them to a shell. If a device
path in `/dev/mst/` contains spaces or shell metacharacters, commands could be
injected.

**Fix:** Use `exec.CommandContext(ctx, "mstconfig", "-d", devPath, "set",
fmt.Sprintf("NUM_OF_VFS=%d", numVFs))` with discrete arguments.

### 2.3 Credential Leaks in Config Dump (P2)

**File:** `pkg/provision/orchestrator.go`

`dumpConfig()` logs many config values at debug level. While `Token` is
explicitly excluded, image URLs may contain embedded credentials
(`oci://user:pass@registry/image:tag`). These are logged in plaintext.

**Fix:** Parse URLs and redact the `userinfo` component before logging:

```go
if u, err := url.Parse(imageURL); err == nil {
    u.User = nil
    slog.Debug("image URL", "url", u.String())
}
```

### 2.4 LLDP Socket File Descriptor Leak (P2)

**File:** `pkg/network/lldp/lldp.go`

If `unix.Bind()` fails after `unix.Socket()` succeeds, the socket file
descriptor is never closed.

**Fix:** Add `defer unix.Close(fd)` immediately after successful socket
creation, and use `dup` or restructure the function to transfer ownership.

### 2.5 FRR APT Key Installed Without Fingerprint Verification (P3)

**File:** `initrd.Dockerfile`

The FRR signing key is fetched via `curl -s` and piped to `tee` without
verifying its GPG fingerprint. A compromised CDN could inject a rogue key.

**Fix:** Pin the key fingerprint and verify after download:

```dockerfile
RUN curl -s ... | gpg --dearmor > /etc/apt/keyrings/frr.gpg \
 && echo "expected-fingerprint" | gpg --keyring /etc/apt/keyrings/frr.gpg --verify
```

---

## 3. Error Handling & Resilience

### 3.1 Status Reporting Errors Silently Discarded (P1)

**File:** `pkg/provision/orchestrator.go`

`_ = o.provider.ReportStatus(ctx, config.StatusError, msg)` ignores the
return value without logging. If CAPRF is unreachable during a failure, the
controller never learns the machine failed, leaving it in a stuck state.

**Fix:** Log the error:

```go
if err := o.provider.ReportStatus(ctx, config.StatusError, msg); err != nil {
    slog.Warn("failed to report error status to caprf", "error", err)
}
```

### 3.2 Debug Commands Use context.Background() (P1)

**File:** `pkg/provision/orchestrator.go`

`DumpDebugState()` runs shell commands (dmesg, lsblk, ip) with
`context.Background()`. If the main context is cancelled (e.g., SIGTERM
during failure), these debug commands run indefinitely and block shutdown.

**Fix:** Use a timeout context:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
```

### 3.3 Log Handler Close Blocks Indefinitely (P2)

**File:** `pkg/caprf/loghandler.go`

`Close()` waits on `<-h.done` with no timeout. If the log-drain goroutine is
stuck (e.g., blocked on a full HTTP POST), `Close()` hangs and prevents clean
shutdown.

**Fix:** Use a select with timeout:

```go
select {
case <-h.done:
case <-time.After(5 * time.Second):
    slog.Warn("log handler drain timed out")
}
```

### 3.4 Chroot Teardown Ignores Unmount Errors (P2)

**File:** `pkg/disk/manager.go`

`teardownChrootBindMounts()` ignores all unmount errors. If `/dev` can't be
unmounted (busy process), subsequent mounts in a retry scenario will fail
with stale mount points.

**Fix:** Collect and return aggregated unmount errors so the orchestrator
can retry or escalate.

### 3.5 DHCP Timeout Hardcoded to 15 Seconds Per Interface (P3)

**File:** `pkg/network/dhcp.go`

`time.NewTimer(15 * time.Second)` is non-configurable. On networks with
slow relay agents or high DHCP load, 15 seconds may be insufficient. With
many interfaces (e.g., bonded 4-port NICs), each gets a sequential 15-second
timeout, totaling 60+ seconds before failure.

**Fix:** Make configurable via `DHCP_TIMEOUT_SEC` environment variable with
15 s default.

### 3.6 Network Connectivity Wait Hardcoded to 5 Minutes (P3)

**File:** `main.go`

`WaitForConnectivity(ctx, target, 5*time.Minute)` is fixed. In air-gapped
or high-latency environments, 5 minutes may be hit frequently, causing
unnecessary reboots.

**Fix:** Make configurable via `NETWORK_TIMEOUT_SEC` with 300 s default.

---

## 4. Test Coverage Gaps

### 4.1 Orchestrator Pipeline Not Unit-Tested (P0)

**File:** `pkg/provision/provision_test.go`

The 30-step `Orchestrator.Provision()` method — the **critical path** of the
entire system — has zero unit test coverage. Individual configurator helpers
(hostname, DNS, GRUB) are tested, but the pipeline sequencing, error
propagation between steps, and state transitions are not.

**Recommended tests:**

| Test Case | What It Validates |
|-----------|-------------------|
| `TestProvision_HappyPath` | All 30 steps execute in order |
| `TestProvision_ImageStreamFails` | Error propagation + status reporting |
| `TestProvision_DiskNotFound` | Graceful failure + debug dump |
| `TestProvision_HealthCheckFails` | Abort before destructive steps |
| `TestProvision_NonFatalSteps` | Inventory/firmware fail → continue |

### 4.2 GoBGP Stack Integration Not Tested (P1)

**Files:** `pkg/network/gobgp/stack.go`, `overlay.go`, `underlay.go`

Unit tests exist for config parsing and peering modes, but the following have
zero test coverage:

- `Stack.Setup()` / `Stack.Teardown()` — orchestration of tiers
- `Stack.WaitForConnectivity()` — BGP peer readiness polling
- `OverlayTier.Setup()` / `Teardown()` — VXLAN + bridge + EVPN routes
- `UnderlayTier.waitForNICs()` — NIC detection loop
- `UnderlayTier.discoverLinkLocalPeer()` — IPv6 LL peer discovery
- `advertiseType5()` — EVPN route advertisement
- `watchRoutes()` — route state monitoring

**Risk:** GoBGP is the preferred network stack. Undetected regressions in
setup/cleanup could leave machines with broken networking.

### 4.3 Inventory Collectors Partially Untested (P2)

**File:** `pkg/inventory/collector_test.go`

Tested: CPU, memory, disk collection. **Not tested:**

- `collectNICs()` — NIC enumeration from sysfs
- `collectPCI()` — PCI device discovery
- `findAccelerators()` — GPU/accelerator detection heuristics

### 4.4 Firmware Version Comparison Edge Cases (P2)

**File:** `pkg/firmware/collector_test.go`

`compareVersions()` uses numeric parsing with lexical fallback. Not tested:

- Mixed format: `"U46 v2.72"` vs `"U50"` (BIOS vendor strings)
- Pre-release: `"1.0.0-rc1"` vs `"1.0.0"`
- Different segment counts: `"1.0"` vs `"1.0.0"`

### 4.5 FRR Manager.Setup() Not Tested (P2)

**File:** `pkg/network/frr/manager_test.go`

Config render and individual helpers are tested, but the full `Setup()`
pipeline (FRR startup → config write → peer add → connectivity wait) has
no integration test.

### 4.6 Kexec Load/Execute Not Tested (P3)

**File:** `pkg/kexec/kexec_linux.go`

`Load()` and `Execute()` wrap Linux syscalls and are inherently hard to test
in CI, but edge cases (missing kernel file, missing initrd, invalid FD) can
be tested with mocked syscalls.

---

## 5. Refactoring Opportunities

### 5.1 Extract Common Retry Logic (P2)

**Files:** `pkg/image/stream.go`, `pkg/caprf/client.go`

Both implement exponential backoff with jitter for HTTP retries. The
pattern should be extracted to `pkg/utils/retry.go`:

```go
func WithRetry(ctx context.Context, maxAttempts int, fn func() error) error {
    for attempt := range maxAttempts {
        if err := fn(); err == nil {
            return nil
        }
        sleep := time.Duration(1<<attempt) * time.Second
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(sleep):
        }
    }
    return fmt.Errorf("max retries (%d) exceeded", maxAttempts)
}
```

### 5.2 Consolidate File Copy Helpers (P3)

**File:** `pkg/provision/configurator.go`

`CopyProvisionerFiles()` and `CopyMachineFiles()` are identical except for
the source path. Consolidate into `CopyTreeIntoChroot(srcBase string)`.

### 5.3 Unify NIC Detection Functions (P2)

**Files:** `pkg/network/dhcp.go`, `pkg/network/nic_linux.go`

`physicalInterfaces()` in dhcp.go and `DetectPhysicalNICs()` in nic_linux.go
implement overlapping NIC detection logic. These should be unified into a
single exported function in the network package.

### 5.4 OCI Layer Count Validation (P2)

**File:** `pkg/image/oci.go`

`FetchOCILayer()` takes `layers[len(layers)-1]` without validating layer
count. Multi-layer images silently extract the wrong layer.

**Fix:** Add validation:

```go
if len(layers) != 1 {
    return nil, fmt.Errorf("expected single-layer OCI image, got %d layers",
        len(layers))
}
```

### 5.5 Use Context Consistently in Configurator Methods (P3)

**File:** `pkg/provision/configurator.go`

Many methods accept a `context.Context` parameter but never use it
(SetHostname, copyProvisionerFiles, configureDNS, RemoveEFIBootEntries,
CreateEFIBootEntry, setupMellanox). Either propagate the context to
`exec.CommandContext()` or remove the unused parameter to avoid confusion.

### 5.6 VLAN Creation Lacks Idempotency (P3)

**File:** `pkg/network/vlan/vlan.go`

`Setup()` calls `netlink.LinkAdd()` without checking if the VLAN interface
already exists. A retry or re-invocation fails with `EEXIST`.

**Fix:** Check for existing interface before creation:

```go
if _, err := netlink.LinkByName(vlanName); err == nil {
    return vlanName, nil // already exists
}
```

---

## 6. Documentation Gaps

### 6.1 No Build Environment Prerequisites (P1)

**File:** `README.md`

Missing from the README:

- Required Go version (1.26+)
- Docker and buildx prerequisites for initramfs builds
- ContainerLab installation for E2E tests
- Linux requirement for running unit tests (`//go:build linux`)

### 6.2 No NIC Driver Availability Per Build Flavor (P2)

**File:** `README.md`

Users choosing between `dockerx86`, `slim`, `micro`, and `gobgp` build
flavors don't know which kernel modules are included. A driver matrix would
prevent "NIC not found" surprises:

| Driver | Full | GoBGP | Slim | Micro |
|--------|------|-------|------|-------|
| mlx5_core | ✓ | ✓ | ✗ | ✗ |
| ixgbe | ✓ | ✓ | ✓ | ✗ |
| igb | ✓ | ✓ | ✓ | ✗ |
| virtio_net | ✓ | ✓ | ✓ | ✓ |

### 6.3 CONTRIBUTING.md Missing Test Requirements (P2)

**File:** `CONTRIBUTING.md`

Missing from the contribution guide:

- All tests require Linux (macOS developers need Docker or CI for `go test`)
- 40% coverage gate enforced by `make test`
- E2E tests require ContainerLab and specific build tags
- Race detector is enabled by default (`-race` flag)

### 6.4 Stale TODO Comments (P3)

**File:** `pkg/realm/networking.go`

Three TODO comments have been lingering:

- Line 43: `// TODO - make this customisable` (hardcoded `eth0`)
- Line 74: `// TODO - remove netplan output` (debug print)
- Line 90: `// ApplyNetplan - this will be done through an /etc/rc.local (TODO)`

These should either be addressed or converted to tracked issues.

### 6.5 Roadmap Doesn't Reflect Implemented Proposals (P3)

**File:** `docs/roadmap.md`

The "Existing Proposals" section at the bottom lists GoBGP as "Proposal"
even though it is implemented. Hardware Inventory (PR #18), Firmware
Reporting (PR #19), VLAN Support (PR #20), and Health Checks are also
implemented but still listed in the priority tables above.

**Fix:** Move implemented proposals to the "Existing Proposals" section and
remove from the priority groups.

---

## 7. Operational & Build Improvements

### 7.1 Makefile Coverage Reporting Lacks HTML Output (P3)

**File:** `Makefile`

`go tool cover -func=coverage.out` shows only summary text. Adding
`-html=coverage.html` would give developers visual line-by-line coverage.

### 7.2 Makefile E2E Targets Missing Dependencies (P3)

**File:** `Makefile`

`test-e2e-gobgp-vrnetlab` requires both a pre-built image and a deployed
ContainerLab topology, but the Makefile target has no dependency on
`clab-gobgp-vrnetlab-up`. Running it in isolation produces confusing errors.

**Fix:** Add explicit prerequisites:

```makefile
test-e2e-gobgp-vrnetlab: clab-gobgp-vrnetlab-up booty-gobgp-test-image
```

### 7.3 Standby Mode Context Not Chained (P2)

**File:** `main.go`

`runStandby()` creates its own context instead of inheriting the outer
signal-aware context. A SIGTERM sent to the process may not propagate to
the standby heartbeat loop promptly.

**Fix:** Pass the outer `ctx` from `signal.NotifyContext()` into
`runStandby()`.

### 7.4 Plunder Client Treats HTTP 3xx as Errors (P3)

**File:** `pkg/plunderclient/client.go`

Status codes > 300 are treated as errors, but HTTP 301/302 redirects should
be transparently followed by `http.Client`. The check should be
`res.StatusCode >= 400`.

### 7.5 BusyBox Symlinks Not Validated (P3)

**File:** `initrd.Dockerfile`

Symlinks are created for BusyBox applets (`ls`, `cat`, `mount`, etc.)
without verifying the applet exists in the BusyBox binary. A missing
applet creates a dead symlink that silently fails at provisioning time.

**Fix:** Add a validation step:

```dockerfile
RUN for cmd in ls cat mount umount; do \
      busybox $cmd --help >/dev/null 2>&1 || echo "WARNING: $cmd not in busybox"; \
    done
```

---

## Summary

| Category | P0 | P1 | P2 | P3 | Total |
|----------|----|----|----|----|-------|
| Logic Bugs | — | 2 | 3 | 3 | 8 |
| Security | — | 1 | 3 | 1 | 5 |
| Error Handling | — | 2 | 2 | 2 | 6 |
| Test Coverage | 1 | 1 | 3 | 1 | 6 |
| Refactoring | — | — | 3 | 3 | 6 |
| Documentation | — | 1 | 2 | 2 | 5 |
| Operational | — | — | 1 | 3 | 4 |
| **Total** | **1** | **7** | **17** | **15** | **40** |

## Recommended Execution Order

1. **Immediate (P0):** Add orchestrator pipeline unit tests (4.1)
2. **Next sprint (P1):** Fix secure-erase invocation (1.1), GoBGP cleanup
   leak (1.2), path traversal (2.1), silent status reporting (3.1),
   debug context (3.2), GoBGP test coverage (4.2), README prerequisites
   (6.1)
3. **Backlog (P2):** Disk wipe error aggregation (1.3), security hardening
   (2.2–2.4), error handling (3.3–3.4), test gaps (4.3–4.5), refactoring
   (5.1, 5.3–5.4), docs (6.2–6.3), standby context (7.3)
4. **Nice-to-have (P3):** Remaining items as capacity permits

## Effort Estimate

- **P0–P1 items:** 5–8 engineering days
- **P2 items:** 8–12 engineering days
- **P3 items:** 4–6 engineering days
- **Total:** 17–26 engineering days
