# Provisioning Roadmap Proposals (Index)

## Status

Proposal. Design only. Nothing in this set is implemented.

## Summary

This index groups 16 design proposals that improve how BOOTy provisions
hosts: more OS targets, Windows and ESXi, Talos, sysext and confext, measured
boot for A/B, CAPI integration, BMC boot paths, UEFI management, firmware,
testing, image delivery, disk lifecycle, rollback, tracing and multi-arch.
Each proposal has the same structure: problem, current state with file
references, goals, options with a recommendation, interfaces (native keys
and CAPRF or CAPI impact), security, testing, phased rollout, effort and open
questions. The older proposals in [roadmap.md](roadmap.md) stay valid; the new
documents link to them where they overlap.

External facts that the authors did not check against a primary source are
marked "verify" in the text.

## Prioritized roadmap

Value: H = high, M = medium, L = low. Effort in engineer-days, BOOTy and CAPRF
together. Order is the suggested start order; independent rows can run in
parallel.

| Order | Proposal | Value | Effort (days) | Depends on | First small PR |
|-------|----------|-------|---------------|------------|----------------|
| 1 | [Better testing](proposal-testing-strategy.md) | H | 35 to 51 | none | Go fuzz targets for vars, `frr.conf`, netplan, grub.cfg, GPT, qcow2 |
| 2 | [Disk lifecycle: erase, health, burn-in](proposal-disk-lifecycle.md) | H | 32 to 49 | none | Report the erase method; `ERASE_FALLBACK=fail` |
| 3 | [CAPI integration](proposal-capi-integration.md) | H | 36 to 55 | none | Typed step events on `EVENTS_URL` |
| 4 | [Image delivery: throughput, resume, fleet, air gap](proposal-image-delivery.md) | H | 33 to 50 | 1 (benchmarks) | zstd decoder concurrency |
| 5 | [UEFI management](proposal-uefi-management.md) | H | 28 to 41 | none | Read-only UEFI audit report |
| 6 | [Rollback story](proposal-rollback.md) | H | 23 to 33 | 5 (slot entries, `BootNext`) | Document today's implicit trial boot |
| 7 | [Measured boot and attestation for A/B](proposal-measured-boot-ab.md) | H | 44 to 67 | 5, BOOTy UKI build, [LUKS](proposal-luks-encryption.md) | Fix `pkg/tpm` findings (restricted AK, PCR plan) |
| 8 | [sysext and confext](proposal-sysext-confext.md) | M to H | 19 to 29 | stub mode needs a UKI target | DDI structure and `extension-release` check |
| 9 | [More OS targets](proposal-os-targets.md) | H | 28 to 42 | none | `TargetProfile` table for today's families |
| 10 | [Firmware analysis and updates](proposal-firmware-lifecycle.md) | M to H | 40 to 61 | 3 (events and host status) | BMC version in-band via IPMI |
| 11 | [BMC preloading and booting](proposal-bmc-preload-boot.md) | M | 35 to 54 | 3 (network data), 7 (signed UKI) | Runtime seed reader |
| 12 | [Talos Linux](proposal-talos.md) | M | 17 to 25 | 9 (profiles) | Talos profile that skips Linux-root steps |
| 13 | [Provisioning run tracing](proposal-provisioning-tracing.md) | M | 9 to 13 | 3 (events) | `TRACEPARENT` in events and logs |
| 14 | [Multi-arch delta](proposal-multiarch-provisioning.md) | L to M | 9 to 13 | 9 | Secure Boot verifier paths per architecture |
| 15 | [Windows Server](proposal-windows-deployment.md) | L (demand unclear) | 18 to 28 | 9, 5 | Windows profile behind a flavor |
| 16 | [VMware ESXi](proposal-esxi-deployment.md) | L | 17 to 26 | none | Generic `MODE=prepare` |
| | **Total** | | **423 to 637** | | |

## Dependency sketch

```text
testing (1) ──> image delivery (4)
CAPI events (3) ──> firmware (10), tracing (13), BMC boot (11)
UEFI mgmt (5) ──> rollback (6) ──┐
UEFI mgmt (5) ──> measured boot (7) ──> BMC boot (11, signed generic UKI)
OS profiles (9) ──> Talos (12), multi-arch (14), Windows (15)
sysext (8) ──> measured boot (7, PCR 13 stub mode)
```

## Quick wins (one PR each, under three days)

1. Stop silent secure erase downgrades: report the method, add
   `ERASE_FALLBACK=fail` ([disk lifecycle](proposal-disk-lifecycle.md)).
2. Fix the `pkg/tpm` library before anyone wires it: restricted AK, verify
   against the quoted digest, one PCR constant
   ([measured boot](proposal-measured-boot-ab.md)).
3. Raise zstd decoder concurrency, behind a benchmark
   ([image delivery](proposal-image-delivery.md)).
4. Add Go fuzz targets for the existing parsers
   ([testing](proposal-testing-strategy.md)).
5. Make the Secure Boot verifier use per-architecture loader names
   ([multi-arch](proposal-multiarch-provisioning.md)).
6. Document the implicit trial boot of A/B preserve-existing upgrades
   ([rollback](proposal-rollback.md)).

## Cross-cutting findings from the code review

These came up while the current state was studied. They are input for the
proposals, not separate changes in this PR.

| Finding | Where | Proposal |
|---------|-------|----------|
| `pkg/tpm` assigns PCR 8, 9, 10, 14, 15 to BOOTy. These collide with GRUB, IMA, shim MokList and systemd (UAPI registry, verify). The cryptenroll proposal binds LUKS to PCR 14. | `pkg/tpm/pcr.go`, `docs/proposal-cryptenroll-tpm.md` | [measured boot](proposal-measured-boot-ab.md) |
| The quote AK is not a restricted key, and there is no EK credential activation. `VerifyQuoteSignature` uses the key in the quote. | `pkg/tpm/attest.go` | [measured boot](proposal-measured-boot-ab.md) |
| `pkg/tpm` and `pkg/disk/luks` are not wired into the provisioning flow. | `grep` of non-test importers | [measured boot](proposal-measured-boot-ab.md) |
| Secure erase falls back to `wipefs` on failure, on frozen drives and on drives without ATA security, and the run succeeds. SAS disks take the `hdparm` path. | `pkg/disk/manager.go` | [disk lifecycle](proposal-disk-lifecycle.md) |
| CAPRF disables Secure Boot before it boots BOOTy, so PCR 7 shows Secure Boot off for the provisioning boot. | CAPRF `internal/redfish/system.go` | [measured boot](proposal-measured-boot-ab.md), [UEFI](proposal-uefi-management.md) |
| CAPRF `main` has no receiver for `INVENTORY_URL`, `FIRMWARE_URL` and `HEALTH_CHECK_URL`, and derives image phases from log text. | CAPRF `internal/server/server.go`, `internal/provision/manager.go` | [CAPI](proposal-capi-integration.md) |
| kexec parses only `grub.cfg` `menuentry` blocks. BLS-based targets (RHEL-likes, Fedora) need a BLS parser. | `pkg/kexec/grub.go` | [OS targets](proposal-os-targets.md) |
| The Secure Boot verifier fallback path is `BOOTX64.EFI` only. | `pkg/secureboot/verify.go` | [multi-arch](proposal-multiarch-provisioning.md) |
| Image download retries stop once the body starts; a mid-body break restarts at byte zero. | `pkg/image/stream.go` | [image delivery](proposal-image-delivery.md) |
| No Go fuzz tests and no benchmarks exist. | `grep` of `_test.go` files | [testing](proposal-testing-strategy.md) |

## How these proposals relate to older ones

| New proposal | Older proposals it extends or narrows |
|--------------|---------------------------------------|
| Measured boot A/B | [tpm-attestation](proposal-tpm-attestation.md), [attestation-enhanced](proposal-attestation-enhanced.md), [cryptenroll-tpm](proposal-cryptenroll-tpm.md), [secureboot-full-chain](proposal-secureboot-full-chain.md) |
| UEFI management | [secureboot](proposal-secureboot.md), [bootloaders](proposal-bootloaders.md) |
| Firmware lifecycle | [firmware-reporting](proposal-firmware-reporting.md), [nic-firmware-common](proposal-nic-firmware-common.md), [bios-settings](proposal-bios-settings.md) |
| Image delivery | [resilient-provisioning](proposal-resilient-provisioning.md), [oci-advanced](proposal-oci-advanced.md) |
| Tracing | [telemetry-enhanced](proposal-telemetry-enhanced.md), [observability](proposal-observability.md) |
| Testing | [ci-testing-matrix](proposal-ci-testing-matrix.md), [e2e-test-matrix](e2e-test-matrix.md) |
| Multi-arch | [arm64-support](proposal-arm64-support.md) |
| OS targets | [cloudinit-management](proposal-cloudinit-management.md), [network-persistence](proposal-network-persistence.md) |
| Disk lifecycle | [health-checks](proposal-health-checks.md), [go-binary-replacement](proposal-go-binary-replacement.md) |

## Process

- Each proposal lists small, independent PRs. Land them one at a time.
- New vars keys need a row in [configuration.md](configuration.md)
  (`TestConfigurationDocListsEveryVarsKey`).
- Changes to the provisioning step list must keep `TestProvisionStepCount`
  consistent.
- CAPRF changes go through CAPRF merge requests. This repository holds only
  the BOOTy side and the shared contract.
