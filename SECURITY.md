# Security Policy

## Supported Versions

Only the latest release published on
[GitHub Releases](https://github.com/telekom/BOOTy/releases) and the `main`
branch are supported. Security fixes land on `main` and ship in the next
release; older releases are not patched, so upgrade to the latest release.

## Reporting a Vulnerability

**Do not** open a public GitHub issue, pull request, or discussion for
security vulnerabilities.

Report suspected vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/telekom/BOOTy/security/advisories/new),
or by email to [opensource@telekom.de](mailto:opensource@telekom.de) with
"BOOTy security" in the subject.

## What to Include

- Affected component (binary, initramfs flavor, container image, ISO), version,
  branch, or commit.
- Steps to reproduce or a proof of concept.
- Impact assessment and any known mitigations.
- Whether the issue is already public.

## Response Expectations

The maintainers acknowledge and triage private vulnerability reports as soon
as practical and keep the reporter informed of progress. Confirmed
vulnerabilities are handled privately until a fix, mitigation, or advisory is
ready. Public disclosure is coordinated with the reporter when possible, and
reporters are credited unless they ask not to be.

## Verifying Releases

Release checksum files and container images are signed with keyless Cosign.
See [Release Artifact Verification](README.md#release-artifact-verification)
for the verification commands.
