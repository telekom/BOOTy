package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// checkDockerfileModules verifies that a Dockerfile's "for m in ..." word list
// contains no inline comments (which break shell parsing) and includes all
// critical modules.
func checkDockerfileModules(t *testing.T, path string, critical []string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}

	forPattern := regexp.MustCompile(`(?m)^\s*for m in\b`)
	loc := forPattern.FindStringIndex(string(data))
	if loc == nil {
		t.Fatalf("cannot find 'for m in' loop in %s", path)
	}

	block := string(data)[loc[0]:]
	doIdx := strings.Index(block, "; do")
	if doIdx < 0 {
		t.Fatal("cannot find '; do' in for-loop")
	}
	wordList := block[:doIdx]

	for i, line := range strings.Split(wordList, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "for ") {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			t.Errorf("line %d: inline comment in for-loop word list breaks shell: %q", i+1, trimmed)
		}
	}

	// Build a set of tokens to avoid substring false positives
	// (e.g. "virtio_pci" matching "virtio_pci_modern_dev").
	tokens := make(map[string]struct{})
	for _, line := range strings.Split(wordList, "\n") {
		for _, field := range strings.Fields(line) {
			// Strip shell continuation characters.
			field = strings.TrimRight(field, "\\")
			tokens[field] = struct{}{}
		}
	}

	for _, mod := range critical {
		if _, ok := tokens[mod]; !ok {
			t.Errorf("critical module %q missing from %s for-loop", mod, path)
		}
	}
}

func TestDockerfileModuleLoopSyntax(t *testing.T) {
	checkDockerfileModules(t, "initrd.Dockerfile", []string{
		"ext4", "xfs", "fat", "vfat", "nls_cp437", "nls_iso8859-1", "scsi_mod", "sd_mod",
		"virtio_pci", "virtio_net", "virtio_blk", "vxlan",
	})
}

func TestVrnetlabDockerfileModuleLoopSyntax(t *testing.T) {
	checkDockerfileModules(t, "test/e2e/clab/vrnetlab/Dockerfile", []string{
		"ext4", "xfs", "fat", "vfat", "nls_cp437", "nls_iso8859-1", "scsi_mod", "sd_mod",
		"virtio_pci", "virtio_net", "virtio_blk", "virtio_scsi", "vxlan",
	})
}

func TestDockerfileRemovesBusyboxMkfsVfatCollisions(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatalf("cannot read initrd.Dockerfile: %v", err)
	}
	text := string(data)
	for _, path := range []string{"bin/mkfs.vfat", "bin/mkfs.fat", "bin/mkdosfs"} {
		if strings.Count(text, path) < 2 {
			t.Errorf("%s must be removed in both full initramfs builders so dosfstools sbin/mkfs.vfat is used", path)
		}
	}
	lines := strings.Split(text, "\n")
	foundSafeGrowpartCopy := false
	for i, line := range lines {
		if strings.TrimSpace(line) != "COPY --from=busybox /build/initramfs/bin/growpart bin/growpart" || i == 0 {
			continue
		}
		previous := strings.TrimSpace(lines[i-1])
		if strings.HasPrefix(previous, "RUN rm -f ") && strings.Contains(previous, "bin/lsblk") {
			foundSafeGrowpartCopy = true
			break
		}
	}
	if !foundSafeGrowpartCopy {
		t.Error("bin/lsblk must be removed in the GoBGP builder before copying real lsblk, otherwise COPY corrupts bin/busybox")
	}
}

func TestDockerfileStampsBuildInfo(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatalf("cannot read initrd.Dockerfile: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"ARG BOOTY_VERSION=dev",
		"ARG BOOTY_BUILD=unknown",
		"ARG BOOTY_FLAVOR=full",
		"ARG BOOTY_FLAVOR=micro",
		"-X main.Version=${BOOTY_VERSION}",
		"-X main.Build=${BOOTY_BUILD}",
		"-X github.com/telekom/BOOTy/pkg/buildinfo.version=${BOOTY_VERSION}",
		"-X github.com/telekom/BOOTy/pkg/buildinfo.commit=${BOOTY_BUILD}",
		"-X github.com/telekom/BOOTy/pkg/buildinfo.flavor=${BOOTY_FLAVOR}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("initrd.Dockerfile missing build metadata stamp %q", want)
		}
	}
}

func TestReleaseAndNightlyPassInitramfsBuildInfoArgs(t *testing.T) {
	for _, path := range []string{
		".github/workflows/release-v2.yml",
		".github/workflows/nightly.yml",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("cannot read %s: %v", path, err)
		}
		text := string(data)
		for _, want := range []string{
			`--build-arg "BOOTY_VERSION=`,
			`--build-arg "BOOTY_BUILD=`,
			`--build-arg "BOOTY_FLAVOR=`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing build metadata argument %q", path, want)
			}
		}
		if strings.Contains(text, `BOOTY_FLAVOR=default`) {
			t.Fatalf("%s must stamp default release artifacts as build-info flavor full, not default", path)
		}
		if strings.Contains(text, `BOOTY_FLAVOR=${{ matrix.flavor }}`) {
			t.Fatalf("%s must normalize matrix flavor before stamping build-info flavor", path)
		}
	}
}

func TestDockerfileUsesUtilLinuxLsblk(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatalf("cannot read initrd.Dockerfile: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	lsblkCopies := 0
	lsblkRemovals := 0
	inRmCommand := false
	rmCommandHasLsblk := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "COPY --from=tools /bin/lsblk bin/lsblk" ||
			trimmed == "COPY --from=tools /usr/bin/lsblk bin/lsblk" {
			lsblkCopies++
		}
		if strings.HasPrefix(trimmed, "RUN rm -f ") {
			inRmCommand = true
			rmCommandHasLsblk = false
		}
		if inRmCommand && strings.Contains(trimmed, "bin/lsblk") {
			rmCommandHasLsblk = true
		}
		if inRmCommand && !strings.HasSuffix(trimmed, "\\") {
			if rmCommandHasLsblk {
				lsblkRemovals++
			}
			inRmCommand = false
		}
	}
	if lsblkCopies != 3 {
		t.Fatalf("expected default, slim, and GoBGP builders to copy util-linux lsblk, got %d copies", lsblkCopies)
	}
	if lsblkRemovals != 3 {
		t.Fatalf("expected default, slim, and GoBGP builders to remove BusyBox lsblk before COPY, got %d removals", lsblkRemovals)
	}
}

func TestSlimDockerfileIncludesResizeAndRepairTools(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatalf("cannot read initrd.Dockerfile: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "FROM debian:bookworm-slim AS slim-builder")
	if start < 0 {
		t.Fatal("cannot find slim-builder stage")
	}
	end := strings.Index(text[start+1:], "\nFROM ")
	if end < 0 {
		t.Fatal("cannot find end of slim-builder stage")
	}
	slimStage := text[start : start+1+end]
	for _, want := range []string{
		"COPY --from=tools /sbin/resize2fs sbin/resize2fs",
		"COPY --from=tools /usr/sbin/xfs_growfs sbin/xfs_growfs",
		"COPY --from=tools /usr/sbin/xfs_repair sbin/xfs_repair",
		"COPY --from=tools /usr/bin/btrfs bin/btrfs",
	} {
		if !strings.Contains(slimStage, want) {
			t.Fatalf("slim-builder missing filesystem tool copy %q", want)
		}
	}
}

func TestDockerfileUsesUtilLinuxDiskProbeTools(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatalf("cannot read initrd.Dockerfile: %v", err)
	}
	text := string(data)
	for _, tool := range []struct {
		name string
		copy string
		path string
	}{
		{
			name: "blkid",
			copy: "COPY --from=tools /sbin/blkid sbin/blkid",
			path: "bin/blkid",
		},
		{
			name: "losetup",
			copy: "COPY --from=tools /usr/sbin/losetup bin/losetup",
			path: "bin/losetup",
		},
	} {
		if got := strings.Count(text, tool.copy); got != 3 {
			t.Fatalf("util-linux %s must be copied into default, slim, and GoBGP builders; got %d copies", tool.name, got)
		}
		for _, stage := range []string{"busybox", "slim-builder", "gobgp-builder"} {
			t.Run(tool.name+"/"+stage, func(t *testing.T) {
				requireStageReplacesBusyboxTool(t, text, stage, tool.name, tool.copy, tool.path)
			})
		}
	}
}

func requireStageReplacesBusyboxTool(t *testing.T, text, stage, toolName, copyCommand, busyboxPath string) {
	t.Helper()

	block := dockerfileStageBlock(t, text, stage)
	copyIndex := strings.Index(block, copyCommand)
	if copyIndex < 0 {
		t.Fatalf("%s stage must copy util-linux %s", stage, toolName)
	}

	beforeCopy := block[:copyIndex]
	removeIndex := strings.LastIndex(beforeCopy, "RUN rm -f")
	if removeIndex >= 0 && strings.Contains(beforeCopy[removeIndex:], busyboxPath) {
		return
	}
	t.Fatalf("%s stage must remove BusyBox %s before copying util-linux %s", stage, busyboxPath, toolName)
}

func dockerfileStageBlock(t *testing.T, text, stage string) string {
	t.Helper()

	stagePattern := regexp.MustCompile(`(?m)^FROM\s+.*\s+AS\s+` + regexp.QuoteMeta(stage) + `\s*$`)
	loc := stagePattern.FindStringIndex(text)
	if loc == nil {
		t.Fatalf("cannot find Dockerfile stage %q", stage)
	}
	block := text[loc[0]:]
	fromPattern := regexp.MustCompile(`(?m)^FROM\s+`)
	matches := fromPattern.FindAllStringIndex(block, 2)
	if len(matches) == 2 {
		return block[:matches[1][0]]
	}
	return block
}
