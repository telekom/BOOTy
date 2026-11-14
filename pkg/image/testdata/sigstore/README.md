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

## sigstore-conformance

`conformance/` holds the `test/assets/bundle-verify` cases of
[sigstore-conformance](https://github.com/sigstore/sigstore-conformance)
(Apache-2.0, Copyright The Sigstore Authors) at commit
`006d1d9e395b3d8e840612eb846a54b33e36eac0`, copied unchanged except that the
cases with a `key.pub` (public-key bundles, which BOOTy does not verify) are
left out. Each case has its `README`; a name ending in `_fail` must be
refused. They include bundles v0.1 to v0.3, DSSE and message signatures,
Rekor v2 (`hashedrekord` 0.0.2, also over DSSE) entries of the Sigstore
staging instance with RFC 3161 timestamps of its timestamp authority, and
broken checkpoints, proofs, timestamps and trusted roots. A case without a
`trusted_root.json` uses `public-good-trusted-root.json`.

`TestSigstoreConformance` runs them all; `SIGSTORE_CONFORMANCE_DIR` points it
at a newer checkout's `test/assets/bundle-verify`.
