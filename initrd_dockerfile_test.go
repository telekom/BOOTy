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

	for _, mod := range critical {
		if !strings.Contains(wordList, mod) {
			t.Errorf("critical module %q missing from %s for-loop", mod, path)
		}
	}
}

func TestDockerfileModuleLoopSyntax(t *testing.T) {
	checkDockerfileModules(t, "initrd.Dockerfile", []string{
		"ext4", "xfs", "vfat", "scsi_mod", "sd_mod",
		"virtio_pci", "virtio_net", "virtio_blk", "vxlan",
	})
}

func TestVrnetlabDockerfileModuleLoopSyntax(t *testing.T) {
	checkDockerfileModules(t, "test/e2e/clab/vrnetlab/Dockerfile", []string{
		"ext4", "xfs", "vfat", "scsi_mod", "sd_mod",
		"virtio_pci", "virtio_net", "virtio_blk", "virtio_scsi", "vxlan",
	})
}
