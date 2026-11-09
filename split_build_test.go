package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/payload"
)

// The split script, the Dockerfile and pkg/payload name the same payloads,
// and every payload file of the script is a file of the default tree.
func TestSplitBuildPayloadsMatchTheDefaultTree(t *testing.T) {
	script, err := os.ReadFile("hack/split-initramfs.sh")
	if err != nil {
		t.Fatal(err)
	}
	want := payload.FRR
	if !strings.Contains(string(script), `payloads="`+want+`"`) {
		t.Fatalf("hack/split-initramfs.sh does not split %q", want)
	}
	dockerfile, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defaultTree := dockerfileStageBlock(t, string(dockerfile), "busybox")
	files := regexp.MustCompile(`(?m)^\t(?:frr)\) echo (.+) ;;$`).FindAllStringSubmatch(string(script), -1)
	if len(files) != 1 {
		t.Fatalf("found %d payload file lists in the script, want 1", len(files))
	}
	for _, m := range files {
		for f := range strings.FieldsSeq(m[1]) {
			if !strings.Contains(defaultTree, " "+f+"\n") {
				t.Errorf("payload file %s is not copied into the default tree", f)
			}
		}
	}
	split := dockerfileStageBlock(t, string(dockerfile), "split-builder")
	for _, want := range []string{
		`RUN split-initramfs.sh /build/initramfs /split "$BOOTY_BUILD"`,
		"COPY --from=kernel /vmlinuz /split/vmlinuz",
		"sha256sum base.cpio.zst frr.cpio.zst vmlinuz > SHA256SUMS",
	} {
		if !strings.Contains(split, want) {
			t.Errorf("split-builder lacks %q:\n%s", want, split)
		}
	}
}

// PR artifacts and releases publish the split archives.
func TestWorkflowsPublishSplitRamdisk(t *testing.T) {
	pr := loadWorkflow(t, ".github/workflows/pr-artifacts.yml")
	job := pr.Jobs["publish-pr-iso"]
	build := requireWorkflowStepRunContains(t, job, "Build ISO artifacts", "--target split")
	if !strings.Contains(build.Run, "sha256sum -c --quiet SHA256SUMS") {
		t.Error("PR artifacts do not check the SHA256SUMS of the split archives")
	}
	upload := requireWorkflowStep(t, job, "Publish split ramdisk artifact")
	assertWorkflowWith(t, upload, "name", "booty-pr-split")
	for _, f := range []string{"base.cpio.zst", "frr.cpio.zst", "vmlinuz", "SHA256SUMS"} {
		assertStepWithContains(t, upload, "path", "out-split/"+f)
	}

	release, err := os.ReadFile(".github/workflows/release-v2.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"flavor: split",
		`hack/release-split-assets.sh out-split "${{ matrix.arch }}"`,
		"split-${{ matrix.arch }}-*.cpio.zst",
		"split-${{ matrix.arch }}-vmlinuz",
		"split-${{ matrix.arch }}-SHA256SUMS\n",
		"split-${{ matrix.arch }}-SHA256SUMS.sha256",
		`-o -name "split-*"`,
	} {
		if !strings.Contains(string(release), want) {
			t.Errorf("release workflow lacks %q", want)
		}
	}
}

// The release manifest names the released split-<arch>-* files, so
// `sha256sum -c split-<arch>-SHA256SUMS` works next to the downloads, and it
// has a .sha256 that the sign job covers.
func TestReleaseSplitAssetsNameTheReleasedFiles(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not installed")
	}
	script, err := filepath.Abs("hack/release-split-assets.sh")
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	var sums strings.Builder
	for _, f := range []string{"base.cpio.zst", "frr.cpio.zst", "vmlinuz"} {
		if err := os.WriteFile(filepath.Join(out, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(f))
		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + f + "\n")
	}
	if err := os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	rel := t.TempDir()
	cmd := exec.Command(script, out, "amd64")
	cmd.Dir = rel
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release-split-assets.sh: %v\n%s", err, b)
	}
	entries, err := os.ReadDir(rel)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := []string{
		"split-amd64-SHA256SUMS", "split-amd64-SHA256SUMS.sha256",
		"split-amd64-base.cpio.zst", "split-amd64-base.cpio.zst.sha256",
		"split-amd64-frr.cpio.zst", "split-amd64-frr.cpio.zst.sha256",
		"split-amd64-vmlinuz", "split-amd64-vmlinuz.sha256",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("released files = %v, want %v", got, want)
	}
	manifest, err := os.ReadFile(filepath.Join(rel, "split-amd64-SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for line := range strings.Lines(string(manifest)) {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("bad manifest line %q", line)
		}
		listed = append(listed, f[1])
	}
	if want := []string{"split-amd64-base.cpio.zst", "split-amd64-frr.cpio.zst", "split-amd64-vmlinuz"}; !slices.Equal(listed, want) {
		t.Fatalf("manifest lists %v, want %v", listed, want)
	}
	for _, sums := range []string{"split-amd64-SHA256SUMS", "split-amd64-SHA256SUMS.sha256"} {
		check := exec.Command("sha256sum", "-c", "--quiet", sums)
		check.Dir = rel
		if b, err := check.CombinedOutput(); err != nil {
			t.Errorf("sha256sum -c %s next to the released files: %v\n%s", sums, err, b)
		}
	}

	// A build output that does not match its SHA256SUMS is not released.
	if err := os.WriteFile(filepath.Join(out, "frr.cpio.zst"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(script, out, "amd64")
	cmd.Dir = t.TempDir()
	if err := cmd.Run(); err == nil {
		t.Error("release-split-assets.sh accepted a build output that fails its SHA256SUMS")
	}
}
