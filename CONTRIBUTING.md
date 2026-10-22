# Contributing to BOOTy

Thank you for your interest in contributing! This document covers the development workflow and coding standards.

## Development Setup

1. Install Go 1.27+
2. Clone the repository:
   ```bash
   git clone https://github.com/telekom/BOOTy.git
   cd BOOTy
   ```
3. Install dependencies:
   ```bash
   go mod download
   ```

## Dependency Updates

Dependabot checks daily for direct and indirect Go module, workflow action, and
base image updates in all eight Dockerfiles through the four configured Docker
directories. Minor/patch Go updates and action updates are grouped; Docker
updates are grouped by dependency, with major updates separate.
ContainerLab `*.clab.yml` topologies are not Kubernetes manifests and are not
updated by Dependabot's Docker ecosystem.

The hosted Renovate companion in `.github/renovate.json` tracks annotated tools,
inline external images (including floating tags), Debian EFI package versions
and the util-linux source ref. It does not duplicate native Dependabot Docker,
Actions or ordinary Go updates. It also patches the Go source floor and workflow
compiler pins within Go 1.27; changing the supported minor is a human decision.
There is no OPA frameworks/constraint dependency in this repository.

**Activation prerequisite:** install/authorize the hosted Renovate GitHub App
for this repository and verify its dashboard and first extraction before relying
on this coverage. The configuration alone does not activate a bot. No new bot
secret, self-hosted privileged workflow or automatic merge is introduced.

Annotations immediately precede live pins; repeated datasource/package pairs
are grouped. The distinct FRR Type-5 fixture requires approval and a coordinated
update of its exact-version assertions (`gobgp_type5_e2e_test.go`); 10.3.2 is
the minimum with the encapsulation relay fix. The signed Debian EFI packages
are grouped behind dashboard approval and require Secure Boot proof.

The LVM manager tracks GitHub **tags**, not nonexistent GitHub releases, and
converts tag underscores to source-version dots. Its independent `LVM2_SHA256`
must be verified and updated with the tarball version **before approving any
update**. Renovate cannot atomically derive that independent checksum.
Util-linux ref updates also require dashboard approval and boot validation.
Do not fabricate digests or remove integrity verification.

Deliberate manual pins are the KVM Ubuntu cloud-image URL/checksum pairs, the
6.8 kernel package URL/checksum pairs (required for old md superblocks), FRR GPG
trust fingerprints, and the four maintained Go fork replacements documented in
`docs/dependency-forks.md`. Ubuntu Docker LTS matrix tags remain fixed while
Dependabot updates their digests. These are compatibility/integrity decisions,
not uncovered ordinary version strings. Locally built `booty-*` images,
release-owned `BOOTY_VERSION`/`VERSION`, reproducibility timestamps and example
registry/checksum fixtures are not external dependencies.

Validate updates with the existing CI and KVM boot matrices before merging.
PR CI validates the companion with Renovate's pinned real CLI; locally run:

```bash
npx --yes --package renovate@44.176.3 renovate-config-validator --strict .github/renovate.json
npx --yes --package renovate@44.176.3 -c 'node --test .github/scripts/renovate-extraction.test.cjs'
```

The extraction contract invokes the real local-platform CLI and exercises real
replacement code for image digest pinning, complete Debian revisions, LVM tag
normalization and raw source refs. It uses project-local scratch directories
and removes them after each run. Stage a newly added `.github/renovate.json`
before local extraction: Renovate discovers repository files through Git.

## Security Reports

The CI govulncheck and nightly/release Trivy steps are advisory: findings and scanner failures remain in
the logs and emit a `Security scan advisory` warning. Build, test and lint
failures remain blocking; the scanner script still returns failures when run
directly.

After PR CI completes, the default-branch `Security report` workflow updates
one bot comment for the current PR head. It reads check metadata only, never
executes PR code or downloads PR artifacts with its write-enabled token, and
ignores stale, closed or mismatched PRs. It starts commenting once this workflow
is on the default branch. Run its tests locally with:

```bash
node --test .github/scripts/security-report.test.cjs
```

## Building

```bash
# Build the binary
make build

# Build the initramfs Docker image (default: full FRR+tools)
docker build -t booty -f initrd.Dockerfile .

# Build bootable ISO (for Redfish virtual media)
docker build --target=iso -f initrd.Dockerfile -o type=local,dest=. .

# Build slim initramfs (DHCP-only, no FRR)
docker build --target=slim -f initrd.Dockerfile -o type=local,dest=. .
```

## Testing

```bash
# Run all tests
make test

# Run tests with coverage
go test -cover ./...

# Run a specific package's tests
go test ./pkg/image/...

# Run E2E integration tests (requires ContainerLab, Linux only)
make clab-test-image
make clab-up && make test-e2e-integration
make clab-gobgp-up && make test-e2e-gobgp
make clab-type5-up && make test-e2e-type5
make clab-boot-up && make test-e2e-boot
make clab-dhcp-up && make test-e2e-dhcp
make clab-bond-up && make test-e2e-bond
make clab-lacp-up && make test-e2e-lacp
make clab-static-up && make test-e2e-static
make clab-multi-nic-up && make test-e2e-multi-nic
make clab-vrnetlab-up && make test-e2e-vrnetlab
make clab-gobgp-vrnetlab-up && make test-e2e-gobgp-vrnetlab
```

### Test Requirements

- File-backed QEMU serial checks use `-display none -monitor none`, not
  `-nographic`: the latter enables a stdio monitor that leaves the Linux runner's
  shared stdout pipe nonblocking and can make later log writes fail with
  `Resource temporarily unavailable`. The workflow asserts blocking stdout
  after QEMU exits, then evaluates the unchanged serial-log boot assertions.
- **Linux only**: All tests in `pkg/provision/`, `pkg/disk/`, `pkg/network/`, and `pkg/realm/` use `//go:build linux`. Use `GOOS=linux` for compilation on macOS/Windows, but execution requires Linux.
- **Coverage gate**: `make test` enforces a **40% coverage** minimum. New code should include tests to maintain or increase coverage.
- **Race detector**: `-race` is enabled by default in all test targets.
- **Table-driven tests**: Prefer `tests := []struct{...}` with subtests over individual test functions.
- **Test helpers**: Use `t.Helper()` in all helper functions so failures report the caller's line.

### Test Levels

Every feature or bug fix **must** include tests at the appropriate level:

| Level | Build Tag | When Required | Command |
|-------|-----------|---------------|----------|
| **Unit** | *(none)* | All code changes | `make test` |
| **Linux E2E** | `linux_e2e` | Disk, mount, loop device, partition code | `go test -tags linux_e2e` (root) |
| **ContainerLab** | `e2e_integration` | Network modes, FRR, DHCP, bonds, static | `make clab-up && make test-e2e-integration` |
| **GoBGP** | `e2e_gobgp` | GoBGP peering, tiers, PeerMode changes | `make clab-gobgp-up && make test-e2e-gobgp` |
| **Boot** | `e2e_boot` | Provisioning orchestrator, step ordering | `make clab-boot-up && make test-e2e-boot` |
| **vrnetlab (QEMU)** | `e2e_vrnetlab` | Full boot flow, kexec, EVPN fabric, ISO | `make clab-vrnetlab-up && make test-e2e-vrnetlab` |
| **GoBGP vrnetlab** | `e2e_gobgp_vrnetlab` | GoBGP with real switch VMs | `make clab-gobgp-vrnetlab-up && make test-e2e-gobgp-vrnetlab` |
| **Production** | `e2e_production` | VRF, DCGW, BFD, production-like topology | `make clab-production-up && make test-e2e-production` |

### When to Use KVM / QEMU Tests

Use vrnetlab (QEMU-backed) E2E tests when the change:
- Affects the **boot sequence** (PID 1 init, kexec, reboot)
- Modifies **image streaming** to block devices
- Changes **EVPN overlay** behavior requiring real switch control planes
- Alters **bootloader installation** (GRUB, systemd-boot, ESP)
- Touches **Redfish integration** or ISO boot paths

vrnetlab tests require a Linux host with KVM support. CI runs these on
`ubuntu-latest` GitHub Actions runners.

## Linting

```bash
make lint
```

This runs [golangci-lint](https://golangci-lint.run/) with the configuration in `.golangci.yml`.
The first run installs the golangci-lint version that CI uses into `bin/`.
Like CI, it builds and runs the linter with the Go version from `go.mod`. The
`go` command downloads that toolchain when your local Go is different.
Do not run the linter with a different local Go. A newer Go than the
golangci-lint release supports can crash its analyzers (golangci-lint v2.12.2
panicked in the staticcheck `fact_purity` analyzer with Go 1.27), and a
golangci-lint built with an older Go fails with "file requires newer Go
version".
The lint always checks the Linux build, also on macOS.

Key lint rules:
- **cyclop**: Maximum function complexity of 15
- **funlen**: Maximum 80 lines / 50 statements per function

## Coding Standards

- **Logging**: Use `log/slog` — never `fmt.Print` for operational logs or `logrus`.
- **Errors**: Use `%w` in `fmt.Errorf` for error wrapping. Start error messages with a lowercase letter.
- **Imports**: Group into stdlib, external, and internal blocks separated by blank lines.
- **Build tags**: Linux-specific code must have `//go:build linux` at the top of the file.
- **Naming**: Use `ctx` for `context.Context`, `cfg` for config structs, `mgr` for managers.
- **Tests**: Prefer table-driven tests. Use `t.Helper()` in test helpers. E2E tests use build tags (`e2e`, `e2e_integration`).

## Pull Request Process

1. Fork the repository and create a feature branch from `main`.
2. Make your changes with clear, focused commits.
3. Ensure `make lint` and `make test` pass.
4. Include tests at the appropriate level (see Test Levels above).
5. Update documentation if your change:
   - Adds a new package, feature gate, or CLI flag
   - Changes the provisioning step count
   - Adds a new Make target or build flavor
   - Creates a new E2E topology or build tag
6. Do not edit [CHANGELOG.md](CHANGELOG.md). Give the PR a Conventional
   Commit title (see [Pull request titles](#pull-request-titles)). For upgrade
   notes, a breaking change or a behaviour change, also add a fragment in
   [`changes/`](changes/README.md).
7. Open a PR with a description of what changed and why.
8. A maintainer will review and merge once CI is green.

### Pull request titles

PRs are squash-merged, and the PR title becomes the commit title on `main`.
The changelog line comes from that title, so write it for the reader of the
changelog: `<type>(<scope>): <what an operator sees>`, for example
`fix(gobgp): flood BUM traffic to a VTEP only for ingress replication`.

The `PR Title` check in `.github/workflows/pr-title.yml` requires one of these
types. The type selects the changelog section:

| Type | Section |
|------|---------|
| `feat` | Features |
| `fix` | Fixes |
| `perf` | Performance |
| `docs` | Docs |
| `build`, `ci`, `test` | CI, build and tests |
| any type with a `(deps...)` scope, also `chore(deps)` | Dependencies |
| `refactor` | Maintenance |
| `revert` | Reverts |
| `chore`, `style` | not in the changelog |

Use `chore` or `style` only for changes that no user sees. A change that a user
or operator can see needs one of the other types, or it is missing from the
changelog.

Add `!` after the type or scope (`feat(config)!: ...`) for a breaking change:
the line goes to "Breaking changes".

GitHub titles a revert PR `Revert "..."`, which the check rejects. Retitle it
`revert: <title of the reverted PR>`, for example
`revert: fix(gobgp): flood BUM traffic only for ingress replication`. A commit
that is reverted in the same release is left out together with its revert; a
revert of a change from an earlier release is listed under "Reverts".

### Changelog

[CHANGELOG.md](CHANGELOG.md) is generated by `hack/changelog.py` with
[git-cliff](https://git-cliff.org/) (`cliff.toml`). Do not edit it in a PR. The
section of a release is:

1. the text in `## [Unreleased]` of `CHANGELOG.md`. It holds the text written
   by hand before the changelog was generated, and is normally empty;
2. one line per commit since the previous tag, grouped by type, with a link
   to the PR. A line is left out when the Unreleased text already names its PR;
3. the fragments in `changes/*.md`, each in the section that its front matter
   names (see [changes/README.md](changes/README.md)).

`make changelog` prints the next release section. It needs git-cliff
(`pipx install git-cliff==2.14.2`, or `GIT_CLIFF="uvx git-cliff@2.14.2" make
changelog`). The `Changelog Preview` CI job runs it on every PR and on `main`.

### Releases

1. Open a release PR titled `chore(release): vX.Y.Z` with
   `make changelog-release RELEASE=X.Y.Z`. It writes the section as
   `## [X.Y.Z] - YYYY-MM-DD`, empties `## [Unreleased]`, updates the link
   references and deletes the fragments in `changes/`. Edit the section if
   needed, for example to add highlights.
2. Merge the release PR and tag the merge commit `vX.Y.Z`.
3. `.github/workflows/release-v2.yml` uses the `## [X.Y.Z]` section of
   `CHANGELOG.md` as the GitHub release body. If the tag has no such section,
   it generates the section from the commits since the previous tag.

### Documentation Checklist

When your PR introduces new functionality, verify:

- [ ] New packages are listed in `README.md` Project Structure
- [ ] New feature gates are in the `README.md` Feature Gates table
- [ ] New Make targets are documented in `README.md` and this file
- [ ] New build tags are added to `.github/instructions/e2e-tests.instructions.md`
- [ ] `.github/copilot-instructions.md` architecture section is up to date
- [ ] Step count references are consistent with the source-of-truth
      `TestProvisionStepCount` regression in `pkg/provision/orchestrator_test.go`
      across `README.md`, `copilot-instructions.md`, and `CONTRIBUTING.md`
- [ ] New agents/prompts/instructions are listed in `.github/AGENTS.md`

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
