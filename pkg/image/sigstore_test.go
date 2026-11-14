package image

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/image/sigstoretest"
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
		{name: "checkpoint of another log", wantErr: `origin "rekor.example.dev - 2605736670972794746" is not the log`,
			edit: func(b *sigstoreBundle) {
				p := b.VerificationMaterial.TlogEntries[0].InclusionProof
				p.Checkpoint.Envelope = strings.Replace(p.Checkpoint.Envelope, "rekor.sigstore.dev - ", "rekor.example.dev - ", 1)
			}},
		{name: "checkpoint key hint", wantErr: "no signature by the log key",
			edit: func(b *sigstoreBundle) {
				p := b.VerificationMaterial.TlogEntries[0].InclusionProof
				p.Checkpoint.Envelope = strings.Replace(p.Checkpoint.Envelope, "— rekor.sigstore.dev wNI9", "— rekor.sigstore.dev wNI8", 1)
			}},
		{name: "duplicate tlog entry", wantErr: "duplicate tlog entry",
			edit: func(b *sigstoreBundle) {
				vm := &b.VerificationMaterial
				vm.TlogEntries = append(vm.TlogEntries, vm.TlogEntries[0])
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

// trustedRootWith returns the trusted root of ss changed by edit, which
// gets the JSON object.
func trustedRootWith(t *testing.T, ss *sigstoretest.Sigstore, edit func(map[string]any)) *sigstoreRoot {
	t.Helper()
	data, err := ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	if data, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	root, err := parseSigstoreRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// withValidity returns a copy of the log or authority JSON object l with
// the validFor window [start, end] of its public key.
func withValidity(l any, start, end time.Time) map[string]any {
	data, _ := json.Marshal(l) //nolint:errchkjson // a JSON object read from JSON
	var c map[string]any
	_ = json.Unmarshal(data, &c) //nolint:errchkjson // as above
	c["publicKey"].(map[string]any)["validFor"] = map[string]string{
		"start": start.UTC().Format(time.RFC3339), "end": end.UTC().Format(time.RFC3339),
	}
	return c
}

// The #933 review: a log listed twice under one ID (e.g. with two validity
// windows) must not shadow the other entry; every Rekor v1 entry's key must
// be valid at every verified time, also without a SET; a Rekor v1
// checkpoint needs the log origin of the base URL.
func TestSigstoreLogKeys(t *testing.T) {
	ss, err := sigstoretest.New()
	if err != nil {
		t.Fatal(err)
	}
	signedAt := time.Now().Add(-time.Hour)
	retired := func(l any) map[string]any {
		return withValidity(l, signedAt.Add(-48*time.Hour), signedAt.Add(-24*time.Hour))
	}
	id := sigstoreIdentity{san: regexp.MustCompile(`^` + regexp.QuoteMeta(testIdentity) + `$`), issuer: testIssuer}
	for _, tc := range []struct {
		name    string
		opts    sigstoretest.Options
		edit    func(map[string]any)
		wantErr string
	}{
		{name: "rekor and CT log listed twice, the retired copy last", edit: func(m map[string]any) {
			for _, k := range []string{"tlogs", "ctlogs"} {
				logs := m[k].([]any)
				m[k] = append(logs, retired(logs[0]))
			}
		}},
		{name: "rekor key retired", wantErr: "log key not valid", edit: func(m map[string]any) {
			logs := m["tlogs"].([]any)
			logs[0] = retired(logs[0])
		}},
		{name: "CT key retired", wantErr: "no SCT verifies", edit: func(m map[string]any) {
			logs := m["ctlogs"].([]any)
			logs[0] = retired(logs[0])
		}},
		{name: "no SET, proof and timestamp", opts: sigstoretest.Options{NoSET: true, Timestamp: true}, edit: func(map[string]any) {}},
		{name: "no SET, rekor key retired before the timestamp", wantErr: "log key not valid at every signing time",
			opts: sigstoretest.Options{NoSET: true, Timestamp: true}, edit: func(m map[string]any) {
				logs := m["tlogs"].([]any)
				logs[0] = retired(logs[0])
			}},
		{name: "rekor log without base URL", wantErr: "no checkpoint origin", edit: func(m map[string]any) {
			delete(m["tlogs"].([]any)[0].(map[string]any), "baseUrl")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.opts
			o.Identity, o.Issuer, o.SignedAt = testIdentity, testIssuer, signedAt
			data, err := ss.Bundle("sha256:"+strings.Repeat("ab", 32), o)
			if err != nil {
				t.Fatal(err)
			}
			var b sigstoreBundle
			if err := json.Unmarshal(data, &b); err != nil {
				t.Fatal(err)
			}
			_, err = verifySigstoreBundle(trustedRootWith(t, ss, tc.edit), id, &b, nil)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verifySigstoreBundle() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("verifySigstoreBundle() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
