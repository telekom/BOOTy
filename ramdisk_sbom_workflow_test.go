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
	// The built components carry the SHA-256 of the file the ramdisk ships.
	for stage, want := range map[string]string{
		"lvm":          "$(sha256sum lvm | cut -d' ' -f1)",
		"sfdisk":       "$(sha256sum sfdisk.static | cut -d' ' -f1)",
		"efi-fallback": "$(sha256sum /out/*.EFI | cut -d' ' -f1)",
		"frr":          "> /frrouting-packages",
	} {
		if !strings.Contains(dockerfileStageBlock(t, text, stage), want) {
			t.Errorf("stage %s lacks %q", stage, want)
		}
	}
	for _, want := range []string{
		"COPY --from=efi-fallback /sbom-source.json source/grub-efi.json",
		"COPY --from=frr /frrouting-packages dpkg/frr/frrouting-packages",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("sbom-inputs lacks %q", want)
		}
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

// Every released ramdisk archive gets a CycloneDX and an SPDX attestation
// of its SBOM, the release waits for them, and the OCI initramfs artifacts
// carry a cosign CycloneDX attestation.
func TestReleaseWorkflowAttestsRamdiskSBOMs(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release-v2.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := loadWorkflow(t, ".github/workflows/release-v2.yml")
	job := wf.Jobs["sbom-attest"]
	if job == nil {
		t.Fatal("missing sbom-attest job")
	}
	if got := needsSet(job.Needs); !hasNeeds(got, "build") {
		t.Fatalf("sbom-attest needs = %v, want build", got)
	}
	if !strings.Contains(string(data), "part: [default, gobgp, slim, micro, split-base, split-frr]") {
		t.Error("sbom-attest does not cover every released ramdisk archive")
	}
	requireWorkflowStepRunContains(t, job, "Locate the ramdisk and its SBOMs", `sha256sum -c --quiet "${f}.sha256"`)
	for name, sbom := range map[string]string{
		"Attest CycloneDX SBOM": "${{ steps.files.outputs.cdx }}",
		"Attest SPDX SBOM":      "${{ steps.files.outputs.spdx }}",
	} {
		step := requireWorkflowStep(t, job, name)
		assertWorkflowActionPinned(t, step, "actions/attest")
		assertStepWith(t, step, "subject-path", "${{ steps.files.outputs.ramdisk }}")
		assertStepWith(t, step, "sbom-path", sbom)
	}

	release := wf.Jobs["release"]
	if release == nil {
		t.Fatal("missing release job")
	}
	if !needsSet(release.Needs)["sbom-attest"] {
		t.Fatalf("release needs = %v, want to include sbom-attest", release.Needs)
	}
	oci := wf.Jobs["oci-artifacts"]
	if oci == nil {
		t.Fatal("missing oci-artifacts job")
	}
	requireWorkflowStepRunContains(t, oci, "Publish initramfs artifacts", `cosign attest --yes --type cyclonedx --predicate "${SBOM}" "${REF}"`)
}
