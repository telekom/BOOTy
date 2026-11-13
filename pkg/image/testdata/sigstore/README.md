# Recorded Sigstore fixtures

Copied unchanged from `pkg/testing/data` of
[sigstore-go](https://github.com/sigstore/sigstore-go) v1.4.0 (Apache-2.0,
Copyright The Sigstore Authors):

- `public-good-trusted-root.json`: `trusted-roots/public-good.json`, the
  trusted root of the public-good Sigstore instance.
- `sigstore-js-provenance.sigstore.json`:
  `bundles/sigstore.js@2.0.0-provenance.sigstore.json`, a bundle v0.1 that
  the public-good instance signed in August 2023 (Fulcio certificate with an
  embedded SCT, Rekor `intoto` 0.0.2 entry with SET, inclusion proof and
  checkpoint).

`TestVerifySigstoreBundleRecorded` checks BOOTy's verifier against them. The
keyless tests for container images generate their own CA and logs with
`pkg/image/sigstoretest` at test time, so they need no recorded files.
