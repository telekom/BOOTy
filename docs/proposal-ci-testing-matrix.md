# Proposal: CI Testing Matrix — DHCP, LACP, Static, Multi-NIC, KVM

## Status: Partially implemented — CI truth lives in current workflows

## Priority: P1

## Summary

Expand CI testing infrastructure with new ContainerLab topologies for DHCP,
LACP, static, and multi-NIC configurations. Add a dedicated KVM test matrix
(`.github/workflows/kvm-matrix.yml`) for hardware-dependent features like
SecureBoot, LUKS, TPM, kexec, BIOS settings, and bootloader management.

Source of truth as of 2026-06-27: `.github/workflows/ci.yml` keeps the unit-test
coverage gate at 40%, not 60%. The default branch includes manual Make targets
for DHCP/static/multi-NIC/bond topology smoke tests, but PR/default CI only runs
the main ContainerLab topology unless a workflow explicitly adds those targets.
`.github/workflows/kvm-matrix.yml` exists and covers specific KVM smoke
scenarios; it does not prove full BIOS settings management.

## Motivation

Current E2E test coverage has significant gaps:

| Mode | Current Test Coverage | Gap |
|------|----------------------|-----|
| DHCP | Manual `clab-dhcp-up` / `test-e2e-dhcp` smoke target | Not run by the default PR CI matrix |
| Static IP | Manual `clab-static-up` / `test-e2e-static` smoke target plus KVM network-persistence scenario | Gateway and renderer variants remain limited |
| LACP bonding | `clab-lacp-*` aliases the bond topology; topology comments say it does not validate LACP negotiation | Real LACP negotiation remains unproven |
| Multi-NIC | Manual `clab-multi-nic-up` / `test-e2e-multi-nic` smoke target | Not run by the default PR CI matrix |
| SecureBoot | KVM `secureboot-smoke` scenario | Smoke boot only, not full chain validation |
| LUKS | KVM `luks` and `luks-verify` scenarios | Full provisioning/enrollment flows remain limited |
| TPM | KVM `tpm` smoke scenario | Attestation and sealing flows remain limited |
| Kexec | KVM `kexec` smoke scenario | Full target handoff matrix remains limited |
| BIOS | KVM vendor-detection test only | BIOS settings capture/apply remains unproven in CI |

### Current Topology Coverage

| Topology | Tag | What's Tested |
|----------|-----|---------------|
| FRR ContainerLab | `e2e_integration` | FRR BGP peering + EVPN |
| Boot ContainerLab | `e2e_boot` | Full provision flow |
| GoBGP ContainerLab | `e2e_gobgp` | 3 peering modes |
| vrnetlab | `e2e_vrnetlab` | QEMU VMs + EVPN fabric |
| KVM boot | `.github/workflows/ci.yml` `kvm-boot` | BOOTy startup with QEMU/e1000 |
| KVM matrix | `.github/workflows/kvm-matrix.yml` | SecureBoot, LUKS, TPM, kexec, A/B, Flatcar-like source-root, crash artifacts, network persistence, bootloader, ISO smoke scenarios |

## Design

### New ContainerLab Topologies

#### DHCP Lab

```yaml
# test/e2e/topologies/dhcp-lab/topology.yml
name: booty-dhcp-lab
topology:
  nodes:
    dhcp-server:
      kind: linux
      image: networkboot/dhcpd:latest
      binds:
        - dhcpd.conf:/etc/dhcp/dhcpd.conf
    booty:
      kind: linux
      image: ghcr.io/telekom/booty:latest
  links:
    - endpoints: ["dhcp-server:eth1", "booty:eth1"]
```

Test scenarios:
- DHCP discover → offer → request → ack
- DHCP with multiple scopes
- DHCP relay agent
- DHCP timeout handling

#### LACP Lab

```yaml
# test/e2e/topologies/lacp-lab/topology.yml
name: booty-lacp-lab
topology:
  nodes:
    leaf01:
      kind: linux
      image: cumuluscommunity/cumulus-vx:5.7
    booty:
      kind: linux
      image: ghcr.io/telekom/booty:latest
  links:
    - endpoints: ["leaf01:swp1", "booty:eth1"]
    - endpoints: ["leaf01:swp2", "booty:eth2"]
```

Test scenarios:
- LACP 802.3ad bond formation with 2 links
- Bond failover (take one link down)
- Bond with VLAN trunking
- `balance-rr`, `active-backup`, `balance-xor` modes

#### Static Lab

```yaml
# test/e2e/topologies/static-lab/topology.yml
name: booty-static-lab
topology:
  nodes:
    router:
      kind: linux
      image: frrouting/frr:latest
    booty:
      kind: linux
      image: ghcr.io/telekom/booty:latest
  links:
    - endpoints: ["router:eth1", "booty:eth1"]
```

Test scenarios:
- Static IP assignment + default gateway
- Static with DNS configuration
- Static with multiple interfaces
- Static failover to DHCP

#### Multi-NIC Lab

```yaml
# test/e2e/topologies/multi-nic-lab/topology.yml
name: booty-multi-nic-lab
topology:
  nodes:
    spine01:
      kind: linux
      image: cumuluscommunity/cumulus-vx:5.7
    booty:
      kind: linux
      image: ghcr.io/telekom/booty:latest
  links:
    - endpoints: ["spine01:swp1", "booty:eth1"]  # management
    - endpoints: ["spine01:swp2", "booty:eth2"]  # data (bond member 1)
    - endpoints: ["spine01:swp3", "booty:eth3"]  # data (bond member 2)
    - endpoints: ["spine01:swp4", "booty:eth4"]  # storage VLAN
```

Test scenarios:
- 4+ NIC detection and enumeration
- Mixed mode: bond (eth2+eth3) + VLAN (eth4) + management (eth1)
- NIC naming consistency
- Correct interface selection per role

### KVM Test Matrix Workflow

```yaml
# .github/workflows/kvm-matrix.yml
name: KVM Test Matrix
on:
  push:
    branches: [main]
    paths:
      - 'pkg/secureboot/**'
      - 'pkg/disk/luks/**'
      - 'pkg/tpm/**'
      - 'pkg/kexec/**'
      - 'pkg/bios/**'
      - 'pkg/bootloader/**'
  pull_request:
    paths:
      - 'pkg/secureboot/**'
      - 'pkg/disk/luks/**'
      - 'pkg/tpm/**'
      - 'pkg/kexec/**'
      - 'pkg/bios/**'
      - 'pkg/bootloader/**'
  workflow_dispatch:

jobs:
  kvm-tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        scenario:
          - name: secureboot-smoke
            qemu_args: "-drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.secboot.fd"
            test_tag: e2e
            test_regex: TestUEFISecureBootSmoke
          - name: luks
            qemu_args: "-drive file=test-disk.qcow2,format=qcow2,if=virtio"
            test_tag: e2e
            test_regex: TestLUKSSmokeQEMU
          - name: tpm
            qemu_args: "-tpmdev emulator,id=tpm0,chardev=chrtpm -chardev socket,id=chrtpm,path=/tmp/swtpm.sock -device tpm-tis,tpmdev=tpm0"
            test_tag: e2e
            test_regex: TestTPMSmokeQEMU
          - name: kexec
            qemu_args: "-append 'console=ttyS0'"
            test_tag: e2e
            test_regex: TestKexecSmokeQEMU
          - name: bootloader-smoke
            qemu_args: "-drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd"
            test_tag: e2e
            test_regex: TestUEFIBootPathSmoke
      fail-fast: false

    steps:
      - uses: actions/checkout@v4

      - name: Install KVM dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y qemu-system-x86 ovmf swtpm swtpm-tools

      - name: Setup swtpm (TPM scenario)
        if: matrix.scenario.name == 'tpm'
        run: |
          mkdir -p /tmp/tpm
          swtpm socket --tpmstate dir=/tmp/tpm --ctrl type=unixio,path=/tmp/swtpm.sock --tpm2 &

      - name: Create test disk (LUKS scenario)
        if: matrix.scenario.name == 'luks'
        run: qemu-img create -f qcow2 test-disk.qcow2 10G

      - name: Run KVM tests
        run: |
          go test -v -tags ${{ matrix.scenario.test_tag }} -run ${{ matrix.scenario.test_regex }} ./test/e2e/kvm/...
        timeout-minutes: 15
        env:
          QEMU_EXTRA_ARGS: ${{ matrix.scenario.qemu_args }}
```

### Required Binaries / Tools (CI Only)

| Tool | Package | Purpose | Where Used |
|------|---------|---------|------------|
| `qemu-system-x86_64` | `qemu-system-x86` | KVM virtual machine | CI runner |
| `ovmf` | `ovmf` | UEFI firmware for QEMU | CI runner |
| `swtpm` | `swtpm` | Software TPM emulator | CI runner |
| `containerlab` | containerlab | Network topology orchestration | CI runner |

**Note**: None of these tools are needed in BOOTy's initramfs. They run
exclusively on CI runners.

### Coverage Target

| Current | Target | Strategy |
|---------|--------|----------|
| 40% | Not raised | Keep documentation aligned with the active `Makefile` and `.github/workflows/ci.yml` gate before proposing a higher threshold |

Specific coverage improvements:
- `pkg/network/` — bond, static, DHCP mode tests
- `pkg/disk/` — LUKS, partition layout tests
- `pkg/secureboot/` — chain verification tests
- `pkg/bios/` — vendor capture/diff tests
- `pkg/tpm/` — measurement, attestation tests

## Files Changed

| File | Change |
|------|--------|
| `.github/workflows/kvm-matrix.yml` | New KVM test matrix workflow |
| `test/e2e/topologies/dhcp-lab/` | DHCP ContainerLab topology |
| `test/e2e/topologies/lacp-lab/` | LACP ContainerLab topology |
| `test/e2e/topologies/static-lab/` | Static IP ContainerLab topology |
| `test/e2e/topologies/multi-nic-lab/` | Multi-NIC ContainerLab topology |
| `test/e2e/kvm/` | KVM-based E2E test files |
| `Makefile` | New make targets for each topology and KVM scenario |
| `.github/workflows/ci.yml` | Add coverage target check |

## Testing

This IS the testing infrastructure proposal. Validation:
- Each new ContainerLab topology deploys without errors
- KVM scenarios boot QEMU successfully in CI
- All E2E tests pass in new topologies
- Coverage continues to meet the active 40% gate unless a future PR raises it

## Risks

| Risk | Mitigation |
|------|------------|
| KVM not available on GitHub Actions | Use `ubuntu-latest` which has KVM support now |
| swtpm installation issues | Use official package; fallback to compilation |
| ContainerLab topology flake | Retry logic; longer timeouts |
| CI time increase | Parallel matrix; path-filtered triggers |

## Effort Estimate

12–16 engineering days (4 ContainerLab topologies + KVM matrix + test
implementations + coverage gap filling).
