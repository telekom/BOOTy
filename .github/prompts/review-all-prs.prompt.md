---
description: "Iterate over every open PR in this repo, review with all personas, fix all findings, ensure CI passes, and drive each PR to merge-ready state"
---

# Review All Open PRs

Iterate over every open PR in this repository. **Process each PR independently in a dedicated fresh subagent.**

## Per-PR Workflow

For each open PR, spawn a **fresh subagent** with the PR number and branch name. The subagent executes the full cycle below. Do NOT batch PRs — each PR is analyzed, fixed, and validated in isolation.

### 1. Mandatory Multi-Persona Code Review (Inner Loop)

This step is **NON-NEGOTIABLE**. Every PR MUST receive a **thorough, deep** structured review from **all** of the following personas. This is not a surface-level scan — each persona must read every changed line, trace data flow, check edge cases, and reason about failure modes.

| Persona | Focus |
|---|---|
| **Security** | OWASP Top 10, credential handling, input validation, gosec findings, path traversal, TLS config |
| **Networking** | netlink correctness, BGP state machines, VRF isolation, LACP bonds, interface lifecycle |
| **Provisioning** | Orchestrator step ordering, disk/image/mount safety, idempotency, rollback on failure |
| **General/Go** | Style, error wrapping, slog usage, concurrency, function length, API design |
| **Test Quality** | E2E coverage for every behavior change, real verification in VMs, no mock-only coverage for critical paths |

**Dynamic persona expansion:** After the initial review, analyze the PR's changed files and determine if any domain is NOT adequately covered by the four base personas. If the PR touches any of the following (non-exhaustive), **add a dedicated ad-hoc persona**:
- Cloud-init / user-data generation → **Cloud-Init Persona**
- BIOS / firmware / Redfish → **Firmware Persona**
- CI / GitHub Actions / Makefile / build → **Build/CI Persona**
- Bootloader / kexec / GRUB / iPXE → **Bootloader Persona**
- OCI / container image / registry → **OCI Persona**
- Auth / TLS / certificates → **Auth Persona**
- Disk / RAID / LVM / NVMe → **Storage Persona**

#### Review-Fix Inner Cycle

The review is **not a single pass**. It is a cycle that repeats until convergence:

1. **All personas review** the current state of the code — every changed file, every new test.
2. Categorize every finding as `BLOCKER`, `WARNING`, or `NIT`.
3. **Fix every finding** — all three severities, not just blockers.
4. **All personas re-review** the fixes and any new code introduced by the fixes.
5. If any persona finds new issues (including issues introduced by the fixes themselves), go back to step 3.
6. **Terminate only when all personas independently report ZERO findings** in the same review pass.

Do NOT skip re-review after fixes. Fixes can introduce new bugs, style violations, or uncovered edge cases. The inner loop must converge to zero findings across all personas simultaneously.

### 2. Proposal Compliance

- Identify the corresponding proposal in `docs/` (if one exists).
- Verify **every feature described in the proposal is fully implemented** — no partial or stub implementations.
- Update the proposal's `Status` field to match reality (`Proposal` → `Accepted` → `Implemented`).

### 3. Documentation Completeness

- All new features must be documented in `README.md`, `copilot-instructions.md`, and/or `CONTRIBUTING.md`:
  - Config variables / env vars / CLI flags with accepted values
  - Usage examples and workflows
  - Feature gate table entries
  - Architecture / project structure updates
  - New Make targets or build tags
- Update instruction files (`.github/instructions/`) if the PR changes conventions.

### 4. Test Coverage (enforced by Test Quality persona)

- Every new/changed code path must have **unit tests** (table-driven, `t.Helper()` in helpers).
- Add **edge-case and failure-recovery tests**: invalid input, timeouts, partial failures, retry/backoff, nil/empty values.
- **E2E tests are mandatory for behavioral changes** — unit tests and mocks alone are NOT sufficient for features that interact with real system resources. The Test Quality persona will reject any PR that relies solely on mocks for critical paths.

#### E2E Test Requirements by Domain

| Domain | Required E2E Verification |
|---|---|
| Disk wiping | Wipe a virtual disk in a KVM VM, then read back and verify all bytes are zero |
| Partitioning | Create partitions on a virtual disk, verify partition table with `lsblk`/`sfdisk` |
| Image streaming | Stream a test image (gzip/lz4/zstd) to a virtual disk, mount and verify file contents |
| RAID/LVM | Create RAID arrays or LVM volumes in a VM, verify with `mdadm`/`lvs` |
| Network config | Apply network config via ContainerLab topology, verify connectivity with `ping`/`ip route` |
| BGP peering | Establish BGP sessions in ContainerLab/vrnetlab, verify routes propagated with `gobgp`/`vtysh` |
| Mount/unmount | Mount filesystems in a VM, write files, unmount, remount, verify persistence |
| Kexec/boot | Kexec into a test kernel in a VM, verify the new kernel is running |
| Cloud-init | Generate cloud-init user-data, boot a VM with it, verify the config was applied |
| BIOS/firmware | Apply BIOS settings via mock Redfish endpoint, verify settings read back correctly |
| Auth/TLS | Establish mTLS connections with test certs, verify rejection of invalid certs |
| NVMe namespaces | Create NVMe namespaces on virtual NVMe device, verify with `nvme list` |

- Use the appropriate build tag and test tier:
  - `e2e_integration` (ContainerLab) for network mode changes
  - `linux_e2e` for disk/partition/mount changes requiring root on Linux
  - `e2e_vrnetlab` / `e2e_gobgp_vrnetlab` (KVM/QEMU) for full boot flow, kexec, disk imaging, or EVPN changes
- **The test must verify the outcome, not just that the code ran without error.** For example: after wiping a disk, read sectors and assert they are zeroed. After creating a partition, parse `lsblk` output and assert the partition exists with the correct size.
- Verify `make test` passes with the 40% coverage gate.

### 5. CI MUST PASS — Hard Gate

Before pushing, **all of the following must pass locally with exit code 0**:

```bash
make lint    # golangci-lint v2, zero findings
make test    # unit tests, 40% coverage gate
make fmt     # gofmt + goimports, no diff
```

**Do NOT push if any of these fail.** Fix the failures first, then re-run. This is a blocking gate — no exceptions, no `--no-verify`, no skipping.

After pushing, **download and analyze all CI job logs and artifacts** for:

- Hidden errors (non-zero exit codes masked by `|| true`, swallowed panics, ignored `err` returns)
- Logic bugs (wrong branch taken, off-by-one, race conditions in goroutines)
- Flaky test patterns (`time.Sleep` without synchronization, unprotected shared state)
- Build warnings, deprecation notices, or dependency vulnerabilities

If CI fails after push, fix and re-push. Repeat until **every CI check is green**.

### 6. PR Comment Resolution

- Fix **all review comments** on the PR — human reviewers and Copilot alike.
- **Resolve** every addressed conversation thread via `gh api graphql` using the `resolveReviewThread` mutation.
- Rebase onto the target branch if there are merge conflicts (`git fetch origin && git rebase origin/main`).

### 7. Iteration Loop

After each push, wait for new reviews and CI results. **Repeat the full cycle** (review → fix → test → lint → push → analyze CI → resolve comments) until ALL of the following are true simultaneously:

- [ ] All reviewer personas report **zero findings**
- [ ] **All CI checks are green** (lint, unit, E2E — no exceptions)
- [ ] All PR review conversations are **resolved**
- [ ] No merge conflicts remain
- [ ] Proposal status is accurate

Only then move to the next PR.

## Orchestration Rules

- Process PRs in **dependency order** — if PR B depends on PR A's branch, process A first.
- Each PR subagent must **checkout the PR branch**, not work on `main`.
- If a fix in one PR affects another open PR, note it but do not cross-contaminate — each subagent owns exactly one PR.
