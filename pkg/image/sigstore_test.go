package image

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVerifySigstoreBundleRecorded checks the verifier against a bundle that
// the public-good Sigstore instance signed: certificate chain, embedded SCT,
// SET, inclusion proof with 10 hashes and checkpoint (testdata/sigstore).
func TestVerifySigstoreBundleRecorded(t *testing.T) {
	root, err := loadSigstoreRoot("testdata/sigstore/public-good-trusted-root.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/sigstore/sigstore-js-provenance.sigstore.json")
	if err != nil {
		t.Fatal(err)
	}
	id := sigstoreIdentity{
		san:    regexp.MustCompile(`^https://github\.com/sigstore/sigstore-js/\.github/workflows/release\.yml@refs/heads/main$`),
		issuer: "https://token.actions.githubusercontent.com",
	}
	load := func(t *testing.T, edit func(*sigstoreBundle)) *sigstoreBundle {
		t.Helper()
		var b sigstoreBundle
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatal(err)
		}
		if edit != nil {
			edit(&b)
		}
		return &b
	}

	payload, err := verifySigstoreBundle(root, id, load(t, nil), nil)
	if err != nil {
		t.Fatalf("verifySigstoreBundle() = %v", err)
	}
	if !strings.Contains(string(payload), "pkg:npm/sigstore@2.0.0") {
		t.Fatalf("payload = %.200s", payload)
	}

	flip := func(b []byte) { b[len(b)/2] ^= 1 }
	for _, tc := range []struct {
		name, wantErr string
		id            *sigstoreIdentity
		edit          func(*sigstoreBundle)
	}{
		{name: "other identity", wantErr: "does not match the identity regexp",
			id: &sigstoreIdentity{san: regexp.MustCompile(`^https://github\.com/sigstore/sigstore-js/\.github/workflows/other\.yml@refs/heads/main$`), issuer: id.issuer}},
		{name: "other issuer", wantErr: "OIDC issuer",
			id: &sigstoreIdentity{san: id.san, issuer: "https://token.actions.githubusercontent.com/"}},
		{name: "tampered payload", wantErr: "signature does not verify",
			edit: func(b *sigstoreBundle) { flip(b.DSSEEnvelope.Payload) }},
		{name: "tampered SET", wantErr: "signed entry timestamp does not verify",
			edit: func(b *sigstoreBundle) {
				flip(b.VerificationMaterial.TlogEntries[0].InclusionPromise.SignedEntryTimestamp)
			}},
		{name: "tampered integrated time", wantErr: "signed entry timestamp does not verify",
			edit: func(b *sigstoreBundle) { b.VerificationMaterial.TlogEntries[0].IntegratedTime++ }},
		{name: "tampered proof hash", wantErr: "computed root hash does not match",
			edit: func(b *sigstoreBundle) { flip(b.VerificationMaterial.TlogEntries[0].InclusionProof.Hashes[3]) }},
		{name: "tampered checkpoint", wantErr: "checkpoint",
			edit: func(b *sigstoreBundle) {
				p := b.VerificationMaterial.TlogEntries[0].InclusionProof
				p.Checkpoint.Envelope = strings.Replace(p.Checkpoint.Envelope, "27657875", "27657876", 1)
			}},
		{name: "missing tlog entry", wantErr: "no transparency log entry",
			edit: func(b *sigstoreBundle) { b.VerificationMaterial.TlogEntries = nil }},
		{name: "missing SET", wantErr: "no signed entry timestamp",
			edit: func(b *sigstoreBundle) { b.VerificationMaterial.TlogEntries[0].InclusionPromise = nil }},
		{name: "unknown media type", wantErr: "unsupported bundle media type",
			edit: func(b *sigstoreBundle) { b.MediaType = "application/vnd.dev.sigstore.bundle.v0.4+json" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useID := id
			if tc.id != nil {
				useID = *tc.id
			}
			_, err := verifySigstoreBundle(root, useID, load(t, tc.edit), nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("verifySigstoreBundle() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestMerkleRootFromProofBounds(t *testing.T) {
	leaf := make([]byte, 32)
	for _, tc := range []struct {
		index, size int64
		proof       [][]byte
	}{
		{index: 1, size: 1},
		{index: -1, size: 1},
		{index: 0, size: 2},                        // proof too short
		{index: 0, size: 1, proof: [][]byte{leaf}}, // proof too long
		{index: 0, size: 0},
	} {
		if _, err := merkleRootFromProof(tc.index, tc.size, leaf, tc.proof); err == nil {
			t.Errorf("merkleRootFromProof(%d, %d, %d hashes) succeeded", tc.index, tc.size, len(tc.proof))
		}
	}
}
