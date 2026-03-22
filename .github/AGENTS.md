# BOOTy — GitHub Copilot Agents

Agent definitions, review personas, and prompt files for Copilot-assisted
development and code review in this workspace.

## Agents

| Agent | File | Purpose |
|-------|------|---------|
| **caprf** | [agents/caprf.agent.md](agents/caprf.agent.md) | Cross-repo coordination between BOOTy and cluster-api-provider-redfish |
| **security-reviewer** | [agents/security-reviewer.agent.md](agents/security-reviewer.agent.md) | Security-focused code review (gosec, OWASP, crypto, auth) |
| **networking-reviewer** | [agents/networking-reviewer.agent.md](agents/networking-reviewer.agent.md) | BGP / EVPN / netlink / Linux networking review |
| **provisioning-reviewer** | [agents/provisioning-reviewer.agent.md](agents/provisioning-reviewer.agent.md) | Disk, image, and orchestrator review |

## Review Personas

Copilot code review uses **review personas** — specialized viewpoints that
focus on different aspects of a pull request. Each persona catches issues that
generalist review would miss.

### How Reviews Work

When Copilot reviews a PR in this repository, it applies the persona-specific
instructions from the `.github/instructions/review.instructions.md` file.
The instructions tell Copilot:

1. **What to look for** — each persona has a focused checklist
2. **What to ignore** — suppresses noise outside the persona's domain
3. **Severity levels** — `blocker`, `warning`, `nit` for triaging comments
4. **Project conventions** — BOOTy-specific patterns (slog, error wrapping, build tags)

### Persona Summary

| Persona | Focus | Key Checks |
|---------|-------|------------|
| **Security** | Vulns, auth, crypto, input validation | gosec rules, OWASP patterns, credential handling, file permissions |
| **Networking** | BGP, EVPN, netlink, VRF, NIC detection | PeerMode exhaustiveness, build tags, VRF isolation, retry loops |
| **Provisioning** | Disk ops, image streaming, orchestrator steps | Step ordering, error propagation, partition safety, compression |
| **General** | Style, conventions, test quality, docs | slog usage, error wrapping, import order, table-driven tests, funlen |

### Documentation Completeness

Copilot reviews **must** flag PRs that add features without updating
corresponding documentation. The review instructions
(`instructions/review.instructions.md`) include a documentation checklist that
applies to every PR:

- New packages → update `README.md` project structure + `copilot-instructions.md`
- New feature gates → update `README.md` Feature Gates table
- New Make targets → update `README.md` + `CONTRIBUTING.md`
- New build tags → update `instructions/e2e-tests.instructions.md`
- Step count changes → update all references (currently 31 steps)
- New agents/prompts/instructions → update this file (`AGENTS.md`)

Documentation gaps are flagged as `🟡 WARNING` — PRs should not merge without
a plan to address them.

### Requesting a Focused Review

In a PR comment, ask Copilot to review with a specific persona:

```
@copilot review this PR with a security focus
@copilot review the networking changes
@copilot review the provisioning logic
```

Or request a full multi-persona review:

```
@copilot review this PR
```

## Prompts

| Prompt | File | Purpose |
|--------|------|---------|
| **proposal** | [prompts/proposal.prompt.md](prompts/proposal.prompt.md) | Scaffold a new design proposal document |
| **review** | [prompts/review.prompt.md](prompts/review.prompt.md) | Run a structured code review with persona selection |

## Instructions

| Instruction | File | Applies To |
|-------------|------|------------|
| **E2E tests** | [instructions/e2e-tests.instructions.md](instructions/e2e-tests.instructions.md) | `test/e2e/**` |
| **Network code** | [instructions/network.instructions.md](instructions/network.instructions.md) | `pkg/network/**` |
| **Code review** | [instructions/review.instructions.md](instructions/review.instructions.md) | All Go files (PR reviews) |

## Directory Structure

```
.github/
├── AGENTS.md                          ← this file
├── copilot-instructions.md            ← project-wide guidelines
├── agents/
│   ├── caprf.agent.md                 ← cross-repo coordination
│   ├── security-reviewer.agent.md     ← security review persona
│   ├── networking-reviewer.agent.md   ← networking review persona
│   └── provisioning-reviewer.agent.md ← provisioning review persona
├── instructions/
│   ├── e2e-tests.instructions.md      ← E2E test conventions
│   ├── network.instructions.md        ← networking code patterns
│   └── review.instructions.md         ← review checklist & personas
└── prompts/
    ├── proposal.prompt.md             ← design proposal scaffold
    └── review.prompt.md               ← structured code review
```
