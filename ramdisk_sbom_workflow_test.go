package main

import (
	"os"
	"strings"
	"testing"
)

// The sbom-inputs target carries what hack/ramdisk_sbom.py needs: the
// package databases of the stages whose files the ramdisks ship, the kernel
// packages and the source-built components.
func TestDockerfileBuildsRamdiskSBOMInputs(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	block := dockerfileStageBlock(t, text, "sbom-inputs")
	for _, want := range []string{
		"COPY --from=tools /usr/lib/os-release os-release",
		"COPY --from=tools /var/lib/dpkg/status dpkg/tools/status",
		"COPY --from=frr /var/lib/dpkg/status dpkg/frr/status",
		"COPY --from=kernel /sbom-dpkg/ dpkg/kernel/",
		"COPY --from=lvm /sbom-source.json source/lvm2.json",
		"COPY --from=sfdisk /sbom-source.json source/util-linux.json",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("sbom-inputs lacks %q", want)
		}
	}
	// The source components name the ramdisk paths their binaries get.
	for stage, want := range map[string]string{
		"lvm":     `"files":["sbin/lvm"]`,
		"sfdisk":  `"files":["bin/sfdisk"]`,
		"busybox": "COPY --from=sfdisk /util-linux/sfdisk.static bin/sfdisk",
	} {
		if !strings.Contains(dockerfileStageBlock(t, text, stage), want) {
			t.Errorf("stage %s lacks %q", stage, want)
		}
	}
	if !strings.Contains(dockerfileStageBlock(t, text, "busybox"), "COPY --from=lvm /LVM2.2.03.27/tools/lvm sbin/") {
		t.Error("the busybox stage no longer copies lvm to sbin/lvm")
	}
}

// The release writes the ramdisk SBOMs next to each ramdisk, checksums them
// (so the sign job signs and attests them) and publishes them.
func TestReleaseWorkflowWritesRamdiskSBOMs(t *testing.T) {
	wf := loadWorkflow(t, ".github/workflows/release-v2.yml")
	build := wf.Jobs["build"]
	if build == nil {
		t.Fatal("missing build job")
	}
	assertWorkflowActionPinned(t, requireWorkflowStep(t, build, "Download Syft"), "anchore/sbom-action/download-syft")
	for _, want := range []string{
		"--target=sbom-inputs",
		"python3 -I hack/ramdisk_sbom.py",
		`sha256sum "${sbom}" > "${sbom}.sha256"`,
	} {
		requireWorkflowStepRunContains(t, build, "Generate ramdisk SBOMs", want)
	}
	upload := requireWorkflowStep(t, build, "Upload artifacts")
	for _, want := range []string{"initramfs.cdx.json*", "initramfs.spdx.json*", "split-${{ matrix.arch }}-*.cdx.json*"} {
		assertStepWithContains(t, upload, "path", want)
	}

	release := wf.Jobs["release"]
	if release == nil {
		t.Fatal("missing release job")
	}
	requireWorkflowStepRunContains(t, release, "Collect release files", `-name "*.cdx.json"`)

	ci := loadWorkflow(t, ".github/workflows/ci.yml")
	job := ci.Jobs["build-flavours"] //nolint:misspell // the job name in ci.yml
	if job == nil {
		t.Fatal("missing the amd64 flavor build job")
	}
	requireWorkflowStepRunContains(t, job, "Test the ramdisk SBOM generator", "hack/ramdisk_sbom_test.py")
	requireWorkflowStepRunContains(t, job, "Verify ramdisk SBOMs", "SBOM file list differs from the ramdisk")
}
