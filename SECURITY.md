# Security Policy

## Supported Versions

Security fixes are provided for the current `main` branch and for the latest
release (including release candidates) published on
[GitHub Releases](https://github.com/telekom/BOOTy/releases). Older releases
do not receive fixes; upgrade to the latest release.

## Reporting a Vulnerability

**Do not** open a public GitHub issue, pull request, or discussion for
security vulnerabilities.

Report suspected vulnerabilities privately:

- Preferred: GitHub private vulnerability reporting, via the repository's
  **Security** tab → **Report a vulnerability**
  (https://github.com/telekom/BOOTy/security/advisories/new).
- If that option is not available to you, email
  [opensource@telekom.de](mailto:opensource@telekom.de) with "BOOTy security"
  in the subject.

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

Release checksum files are signed with keyless Cosign (`*.sha256.bundle`), and
release container images are signed with Cosign.
