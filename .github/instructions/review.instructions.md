---
applyTo: "**/*.go"
description: "Code review guidelines for GitHub Copilot PR reviews — personas, severity levels, BOOTy conventions. Applied to all Go files during Copilot code review."
---

# Code Review Guidelines

Instructions for GitHub Copilot when reviewing pull requests in the BOOTy
repository. Apply these rules to every Go file in the diff.

## Severity Levels

Use consistent severity prefixes in review comments:

| Prefix | Meaning | Merge Impact |
|--------|---------|--------------|
| `🔴 BLOCKER` | Must fix — security vuln, data loss, broken logic | Blocks merge |
| `🟡 WARNING` | Should fix — edge case, missing validation, fragile pattern | Strongly recommended |
| `🔵 NIT` | Nice to have — style, naming, minor improvement | Optional |

## General Checklist (All Go Code)

### Logging

- [ ] Uses `log/slog` — never `fmt.Print`, `log.Print`, or `logrus`
- [ ] Log messages start with lowercase
- [ ] Structured fields via `slog.String()`, `slog.Int()`, `slog.Any()` — not
      string formatting
- [ ] No sensitive values (tokens, passwords) in log output

### Error Handling

- [ ] Errors wrapped with `fmt.Errorf("lowercase context: %w", err)`
- [ ] Error messages start with lowercase
- [ ] Fatal errors returned, not swallowed — optional features may log and
      continue
- [ ] No bare `err.Error()` in user-facing output without context

### Imports

- [ ] Three groups separated by blank lines: stdlib → external → internal
- [ ] No dot imports except in test files using gomega/ginkgo
- [ ] GoBGP protobuf API aliased as `apipb`

### Functions

- [ ] Max 80 lines / 50 statements (exceptions: `main.go`, `pkg/ux/captain.go`)
- [ ] Cognitive complexity ≤ 25, cyclomatic complexity ≤ 15
- [ ] Nested if depth ≤ 5

### Testing

- [ ] Table-driven tests with descriptive subtest names
- [ ] Test helpers call `t.Helper()` as first line
- [ ] E2E tests have correct build tags (`e2e`, `e2e_integration`, etc.)
- [ ] No `time.Sleep` in unit tests — use channels, tickers, or mocks
- [ ] New features include tests at the appropriate level (unit, E2E, KVM/QEMU)
- [ ] Boot flow, disk imaging, kexec, or EVPN changes include vrnetlab E2E
      tests (`e2e_vrnetlab` or `e2e_gobgp_vrnetlab` build tag)
- [ ] Disk/partition/mount changes include `linux_e2e` tests with loop devices
- [ ] Network mode changes include ContainerLab E2E tests
- [ ] No untested code paths — PRs without adequate test coverage should be
      flagged as `🟡 WARNING`

### Concurrency

- [ ] `atomic` operations for simple counters — not mutexes
- [ ] Channels for signaling and coordination
- [ ] Context propagation — functions accept `ctx context.Context` as first arg
- [ ] No goroutine leaks — ensure goroutines have shutdown paths

### Build Tags

- [ ] Linux-specific code (netlink, raw sockets, mount, mknod) has
      `//go:build linux`
- [ ] Cross-platform logic in untagged files

## Domain-Specific Reviews

### Network Code (`pkg/network/**`)

Apply the networking review persona when the diff touches:
- GoBGP peering, tier setup, or BGP server configuration
- FRR template rendering
- Netlink operations (link, address, route, VRF)
- NIC detection or bond creation
- DHCP or static mode setup

Key checks:
- PeerMode switch exhaustiveness (all three modes)
- VRF cleanup in teardown paths
- Build tags on all netlink-using files
- Config defaults applied via `ApplyDefaults()`

### Provisioning Code (`pkg/provision/**`, `pkg/disk/**`, `pkg/image/**`)

Apply the provisioning review persona when the diff touches:
- Orchestrator step sequence
- Disk detection, partitioning, or formatting
- Image streaming or decompression
- Bootloader installation
- Kexec or reboot logic

Key checks:
- Correct disk target validation before writes
- Step ordering consistency with orchestrator
- Checksum verification on image writes
- Unmount before destructive disk operations

### Security-Sensitive Code

Apply the security review persona when the diff touches:
- Authentication or authorization logic
- File I/O with paths from external input
- `exec.Command` with variable arguments
- HTTP endpoints or client requests
- Cryptographic operations

Key checks:
- No credential logging
- Path traversal prevention
- Command injection prevention
- TLS configuration correctness

### Documentation

Flag documentation issues when the diff:
- Adds a new package without updating `README.md` project structure or
  `copilot-instructions.md` architecture list
- Adds a new feature gate, environment variable, or CLI flag without documenting
  it in the README Feature Gates table
- Changes the provisioning step count without updating references
  (`README.md`, `copilot-instructions.md`, `CONTRIBUTING.md`)
- Adds a new Make target without documenting it in Build and Test sections
- Introduces a new build flavor or Docker target without updating the
  Build Flavors table in `README.md`
- Creates a new E2E topology or build tag without adding it to
  `CONTRIBUTING.md` and `.github/instructions/e2e-tests.instructions.md`
- Adds a new agent, prompt, or instruction file without updating
  `.github/AGENTS.md`

Key checks:
- [ ] New packages listed in `README.md` project structure
- [ ] New feature gates documented in README Feature Gates table
- [ ] New Make targets documented in `README.md` and `CONTRIBUTING.md`
- [ ] New build tags documented in E2E test instructions
- [ ] `copilot-instructions.md` architecture section matches actual packages
- [ ] Step count references consistent across all docs (currently 33 steps)
- [ ] Code comments on exported types and functions are accurate
- [ ] Proposal files in `docs/` have correct Status field (Proposal, Accepted,
      Implemented, Rejected)

Documentation gaps should be flagged as `🟡 WARNING` — PRs that add features
without corresponding doc updates should not be merged without a plan to fix.

## Comment Quality Guidelines

When writing review comments:

1. **Be specific** — point to the exact line and explain what's wrong
2. **Suggest a fix** — include a code snippet showing the corrected version
3. **Explain why** — reference the project convention or security principle
4. **One issue per comment** — don't bundle unrelated issues
5. **Acknowledge good patterns** — briefly note well-written code when relevant

## Suppressing False Positives

Don't flag these as issues:
- gosec G115/G204/G301/G304/G306/G706 — intentionally excluded in
  `.golangci.yml` for device operations
- `funlen` in `main.go` and `pkg/ux/captain.go` — excluded by config
- `exec.Command` in `pkg/realm/` — expected for system operations
- File permissions > 0644 for device nodes and mount points
