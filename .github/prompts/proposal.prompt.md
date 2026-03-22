---
description: "Scaffold a new BOOTy design proposal following the established format in docs/proposal-*.md"
---

# New Proposal: ${input:title}

Generate a design proposal document at `docs/proposal-${input:slug}.md` for BOOTy.

## Format

Use this structure (include only relevant sections):

```markdown
# ${input:title}

## Status

Proposal

## Priority

${input:priority:P1|P2|P3}

## Summary

{2-3 sentence overview of what this proposes and why}

## Motivation

{Problem statement — what pain point does this solve?}
{Include comparison table if replacing an existing approach}

## Design

{Architecture and implementation approach}
{ASCII diagrams for system interactions}
{Go interface definitions or key type signatures}
{Code samples showing usage patterns}

## Files Changed

| File | Change |
|------|--------|
| | |

## Testing

{Unit test approach}
{E2E test topology if needed (ContainerLab, vrnetlab)}
{New build tags if applicable}

## Alternatives Considered

{Other approaches evaluated and why they were rejected}
```

## Guidelines

- Follow existing proposal style from `docs/proposal-gobgp.md` and `docs/proposal-health-checks.md`
- Use Go code samples that match project conventions (`log/slog`, `fmt.Errorf("lowercase: %w", err)`)
- Reference existing packages when extending them (e.g., `pkg/network/`, `pkg/provision/`)
- Include a "Files Changed" table listing every file that would be added or modified
- For network features: describe interaction with the three-tier GoBGP architecture
- For provisioning features: identify which of the 31 steps are affected
