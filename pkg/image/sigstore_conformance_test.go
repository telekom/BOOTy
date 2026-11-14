package image

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSigstoreConformance runs the bundle-verify cases of
// sigstore-conformance copied to testdata/sigstore/conformance (see the
// README there): a case verifies unless its name ends in _fail. Cases with
// a key.pub (public-key bundles) are not BOOTy's concern and are not
// copied. SIGSTORE_CONFORMANCE_DIR points at a full checkout's
// test/assets/bundle-verify instead.
func TestSigstoreConformance(t *testing.T) {
	dir := os.Getenv("SIGSTORE_CONFORMANCE_DIR")
	if dir == "" {
		dir = "testdata/sigstore/conformance"
	}
	cases, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defaultArtifact, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, c := range cases {
		caseDir := filepath.Join(dir, c.Name())
		if !c.IsDir() || fileExists(filepath.Join(caseDir, "key.pub")) {
			continue
		}
		ran++
		t.Run(c.Name(), func(t *testing.T) {
			err := verifyConformanceCase(caseDir, defaultArtifact)
			if strings.HasSuffix(c.Name(), "_fail") {
				if err == nil {
					t.Fatal("verified, want a failure")
				}
				t.Logf("refused: %v", err)
				return
			}
			if err != nil {
				t.Fatalf("verify = %v", err)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no conformance cases")
	}
}

func verifyConformanceCase(dir string, defaultArtifact []byte) error {
	read := func(name, def string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return def
		}
		return strings.TrimSpace(string(data))
	}
	rootPath := filepath.Join(dir, "trusted_root.json")
	if !fileExists(rootPath) {
		rootPath = "testdata/sigstore/public-good-trusted-root.json"
	}
	root, err := loadSigstoreRoot(rootPath)
	if err != nil {
		return err
	}
	artifact := defaultArtifact
	if data, err := os.ReadFile(filepath.Join(dir, "artifact")); err == nil {
		artifact = data
	}
	id := sigstoreIdentity{
		san: regexp.MustCompile("^" + regexp.QuoteMeta(read("identity",
			"https://github.com/sigstore-conformance/extremely-dangerous-public-oidc-beacon/.github/workflows/extremely-dangerous-oidc-beacon.yml@refs/heads/main")) + "$"),
		issuer: read("issuer", "https://token.actions.githubusercontent.com"),
	}
	data, err := os.ReadFile(filepath.Join(dir, "bundle.sigstore.json"))
	if err != nil {
		return err
	}
	var b sigstoreBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return err
	}
	digest := sha256.Sum256(artifact)
	if b.MessageSignature != nil {
		_, err := verifySigstoreBundle(root, id, &b, digest[:])
		return err
	}
	payload, err := verifySigstoreBundle(root, id, &b, nil)
	if err != nil {
		return err
	}
	var st struct {
		Subject []struct {
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return err
	}
	for _, s := range st.Subject {
		if s.Digest["sha256"] == hex.EncodeToString(digest[:]) {
			return nil
		}
	}
	return errors.New("statement does not name the artifact")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
