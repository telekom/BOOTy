package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every stage that packs a ramdisk declares SOURCE_DATE_EPOCH, so the
// release build can pass the commit time and pack-initramfs.sh sees it.
func TestDockerfilePackStagesDeclareSourceDateEpoch(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"busybox", "slim-builder", "gobgp-builder", "micro-builder", "split-builder"} {
		instructions := dockerfileInstructions(dockerfileStageBlock(t, string(data), stage))
		arg, pack := -1, -1
		for i, in := range instructions {
			switch {
			case in == "ARG SOURCE_DATE_EPOCH=315532800":
				arg = i
			case strings.HasPrefix(in, "RUN pack-initramfs.sh") || strings.HasPrefix(in, "RUN split-initramfs.sh"):
				pack = i
			}
		}
		if pack < 0 {
			t.Errorf("stage %s does not pack a ramdisk", stage)
			continue
		}
		if arg < 0 || arg > pack {
			t.Errorf("stage %s: ARG SOURCE_DATE_EPOCH=315532800 must come before the packing", stage)
		}
	}
}

// pack-initramfs.sh gives the same bytes for the same tree, whatever the
// file times of the build: a tree with other mtimes packs to the same
// archive, and the archive holds the SOURCE_DATE_EPOCH time.
func TestPackInitramfsIsReproducible(t *testing.T) {
	for _, tool := range []string{"cpio", "zstd", "gzip", "touch"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	if out, err := exec.CommandContext(t.Context(), "cpio", "--help").CombinedOutput(); err != nil || !bytes.Contains(out, []byte("--reproducible")) {
		t.Skip("cpio has no --reproducible (needs GNU cpio 2.13 or later)")
	}
	script, err := filepath.Abs("hack/pack-initramfs.sh")
	if err != nil {
		t.Fatal(err)
	}

	pack := func(mtime time.Time, out string) []byte {
		t.Helper()
		root := t.TempDir()
		for _, d := range []string{"bin", "etc", "modules"} {
			if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		files := map[string]os.FileMode{"init": 0o755, "etc/passwd": 0o644, "modules/a.ko": 0o644}
		for f, mode := range files {
			if err := os.WriteFile(filepath.Join(root, f), []byte(f), mode); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("../init", filepath.Join(root, "bin/booty")); err != nil {
			t.Fatal(err)
		}
		for f := range files {
			if err := os.Chtimes(filepath.Join(root, f), mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		dest := filepath.Join(t.TempDir(), out)
		cmd := exec.CommandContext(t.Context(), "sh", script, root, dest)
		cmd.Env = append(os.Environ(), "SOURCE_DATE_EPOCH=1700000000")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pack-initramfs.sh: %v\n%s", err, b)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	for _, out := range []string{"initramfs.cpio.zst", "initramfs.cpio.gz"} {
		t.Run(out, func(t *testing.T) {
			a := pack(time.Unix(1, 0), out)
			b := pack(time.Now(), out)
			if !bytes.Equal(a, b) {
				t.Fatal("the same tree with other file times packed to different bytes")
			}
			decompress := "zstd"
			if strings.HasSuffix(out, ".gz") {
				decompress = "gzip"
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-c", decompress+" -dc | cpio -itv --quiet")
			cmd.Stdin = bytes.NewReader(a)
			cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
			listing, err := cmd.Output()
			if err != nil {
				t.Fatalf("list archive: %v", err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
				if !strings.Contains(line, "Nov 14  2023") || !strings.Contains(line, " root ") {
					t.Errorf("entry not stamped root at SOURCE_DATE_EPOCH: %q", line)
				}
			}
		})
	}
}

// CI builds the released ramdisk targets twice and compares them.
func TestCIWorkflowChecksReproducibleInitramfs(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/ci.yml")
	job, ok := wf.Jobs["reproducible-initramfs"]
	if !ok {
		t.Fatal("missing reproducible-initramfs job")
	}
	if job.If != "needs.detect-initramfs-flavor-changes.outputs.should-build == 'true'" {
		t.Fatalf("reproducible-initramfs if = %q", job.If)
	}
	assertWorkflowActionPinned(t, requireWorkflowAction(t, job, "actions/checkout"), "actions/checkout")
	assertWorkflowActionPinned(t, requireWorkflowAction(t, job, "docker/setup-buildx-action"), "docker/setup-buildx-action")
	requireWorkflowStepRunContains(t, job, "Build twice and compare", "hack/check-reproducible.sh linux/amd64 default split slim gobgp micro")
}

// The release passes the commit time, so a rebuild of a tag reproduces it.
func TestReleaseWorkflowBuildsWithSourceDateEpoch(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/release-v2.yml")
	build := wf.Jobs["build"]
	if build == nil {
		t.Fatal("missing build job")
	}
	requireWorkflowStepRunContains(t, build, "Build initramfs (${{ matrix.flavor }})", `SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)`)
}
