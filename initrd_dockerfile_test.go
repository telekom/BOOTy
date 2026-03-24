package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDockerfileModuleLoopSyntax(t *testing.T) {
	data, err := os.ReadFile("initrd.Dockerfile")
	if err != nil {
		t.Fatalf("cannot read initrd.Dockerfile: %v", err)
	}
	content := string(data)

	forPattern := regexp.MustCompile(`(?m)^\s*for m in\b`)
	loc := forPattern.FindStringIndex(content)
	if loc == nil {
		t.Fatal("cannot find 'for m in' loop in initrd.Dockerfile")
	}

	block := content[loc[0]:]
	doneIdx := strings.Index(block, "\n    done")
	if doneIdx < 0 {
		doneIdx = strings.Index(block, "\ndone")
	}
	if doneIdx < 0 {
		t.Fatal("cannot find 'done' closing the for-loop")
	}
	forBlock := block[:doneIdx]

	// Extract just the word list (from "for m in" to "; do").
	// Inline comments here break shell parsing by swallowing the rest of the line.
	doIdx := strings.Index(forBlock, "; do")
	if doIdx < 0 {
		t.Fatal("cannot find '; do' in for-loop")
	}
	wordList := forBlock[:doIdx]

	lines := strings.Split(wordList, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "for ") {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			t.Errorf("line %d: inline comment in for-loop word list breaks shell: %q", i+1, trimmed)
		}
	}

	critical := []string{
		"ext4", "xfs", "vfat", "scsi_mod", "sd_mod",
		"virtio_pci", "virtio_net", "virtio_blk", "vxlan",
	}
	for _, mod := range critical {
		if !strings.Contains(wordList, mod) {
			t.Errorf("critical module %q missing from Dockerfile for-loop", mod)
		}
	}
}

func TestVrnetlabDockerfileModuleLoopSyntax(t *testing.T) {
	data, err := os.ReadFile("test/e2e/clab/vrnetlab/Dockerfile")
	if err != nil {
		t.Fatalf("cannot read vrnetlab Dockerfile: %v", err)
	}
	content := string(data)

	forPattern := regexp.MustCompile(`(?m)^\s*for m in\b`)
	loc := forPattern.FindStringIndex(content)
	if loc == nil {
		t.Fatal("cannot find 'for m in' loop in vrnetlab Dockerfile")
	}

	block := content[loc[0]:]
	doIdx := strings.Index(block, "; do")
	if doIdx < 0 {
		t.Fatal("cannot find '; do' in for-loop")
	}
	wordList := block[:doIdx]

	lines := strings.Split(wordList, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "for ") {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			t.Errorf("line %d: inline comment in vrnetlab Dockerfile for-loop word list: %q", i+1, trimmed)
		}
	}

	critical := []string{
		"ext4", "xfs", "vfat", "scsi_mod", "sd_mod",
		"virtio_pci", "virtio_net", "virtio_blk", "virtio_scsi", "vxlan",
	}
	for _, mod := range critical {
		if !strings.Contains(wordList, mod) {
			t.Errorf("critical module %q missing from vrnetlab Dockerfile for-loop", mod)
		}
	}
}
