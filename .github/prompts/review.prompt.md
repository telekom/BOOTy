---
description: "Run a structured code review with optional persona selection — security, networking, provisioning, or general"
---

# Code Review: ${input:scope}

Review the specified files or changes with a ${input:persona:general|security|networking|provisioning} focus.

## Instructions

1. Read the files or diff indicated by `${input:scope}`
2. Apply the **${input:persona}** review persona from `.github/instructions/review.instructions.md`
3. For each issue found, format as:

```
🔴 BLOCKER: [description]
File: [path]#L[line]
Fix: [suggested code change]
Why: [convention or principle violated]
```

```
🟡 WARNING: [description]
File: [path]#L[line]
Fix: [suggested code change]
Why: [convention or principle violated]
```

```
🔵 NIT: [description]
File: [path]#L[line]
Fix: [suggested code change]
```

4. Group findings by file, then by severity (blockers first)
5. End with a summary:
   - Total issues: X blockers, Y warnings, Z nits
   - Overall assessment: approve, request changes, or comment

## Persona Details

- **general**: slog usage, error wrapping, imports, tests, function complexity, naming
- **security**: vulns, auth, crypto, file permissions, exec.Command, path traversal
- **networking**: BGP, EVPN, netlink, VRF, PeerMode exhaustiveness, build tags
- **provisioning**: disk safety, image checksums, step ordering, mount/unmount, kexec

## Project Conventions

- `log/slog` only — no fmt.Print or logrus
- `fmt.Errorf("lowercase: %w", err)` — always wrap errors
- Imports: stdlib → external → internal (blank-line separated)
- Max 80 lines / 50 statements per function
- Linux-specific code requires `//go:build linux`
- gosec exceptions: G115, G204, G301, G304, G306, G706 (documented in `.golangci.yml`)
