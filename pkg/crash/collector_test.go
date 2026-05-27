//go:build linux

package crash

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestCollectIncludesArtifactsAndMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "var", "crash", "vmcore"), "vmcore data")
	writeTestFile(t, filepath.Join(root, "var", "log", "kern.log"), "kernel panic - not syncing: test")

	outDir := t.TempDir()
	result, err := Collect(context.Background(), &CollectOptions{
		RootPath:      root,
		PstorePath:    filepath.Join(t.TempDir(), "missing-pstore"),
		OutputDir:     outDir,
		TargetDisk:    "/dev/sda",
		RootPartition: "/dev/sda2",
		MountPoint:    root,
		MaxBytes:      1024 * 1024,
		Config:        &config.MachineConfig{Hostname: "node-1", Provision: config.ProvisionConfig{ProviderID: "redfish://node-1"}, Mode: "provision"},
	})
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}
	if !result.EvidenceFound {
		t.Fatal("expected evidence")
	}
	entries := readArchive(t, result.ArchivePath)
	for _, name := range []string{"manifest.json", "metadata.json", "target-root/var/crash/vmcore", "target-root/var/log/kern.log"} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("archive missing %s; entries=%v", name, keys(entries))
		}
	}

	var manifest Manifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.Metadata.Machine.Hostname != "node-1" {
		t.Fatalf("hostname = %q, want node-1", manifest.Metadata.Machine.Hostname)
	}
	if len(manifest.Metadata.BuildInfo) == 0 {
		t.Fatal("expected build info metadata")
	}
	if manifest.Scan.TargetDisk != "/dev/sda" || manifest.Scan.RootPartition != "/dev/sda2" {
		t.Fatalf("unexpected scan metadata: %+v", manifest.Scan)
	}
}

func TestCollectNoEvidenceSkipsArchive(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "var", "log", "kern.log"), "normal boot log")

	result, err := Collect(context.Background(), &CollectOptions{
		RootPath:   root,
		PstorePath: filepath.Join(t.TempDir(), "missing-pstore"),
		OutputDir:  t.TempDir(),
		MaxBytes:   1024 * 1024,
	})
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}
	if result.EvidenceFound {
		t.Fatal("expected no evidence")
	}
	if result.ArchivePath != "" {
		t.Fatalf("ArchivePath = %q, want empty", result.ArchivePath)
	}
}

func TestCollectSkipsSymlinksAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "var", "log", "kern.log"), "kernel panic with call trace")
	writeTestFile(t, filepath.Join(root, "var", "crash", "large-vmcore"), "0123456789abcdef")
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "var", "crash", "passwd-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result, err := Collect(context.Background(), &CollectOptions{
		RootPath:   root,
		PstorePath: filepath.Join(t.TempDir(), "missing-pstore"),
		OutputDir:  t.TempDir(),
		MaxBytes:   8,
	})
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}
	if !result.EvidenceFound {
		t.Fatal("expected evidence")
	}
	reasons := make([]string, 0, len(result.Manifest.Skipped))
	for _, skipped := range result.Manifest.Skipped {
		reasons = append(reasons, skipped.Reason)
	}
	if !contains(reasons, "symlink_skipped") {
		t.Fatalf("expected symlink skip, got %v", reasons)
	}
	if !contains(reasons, "size_limit") {
		t.Fatalf("expected size limit skip, got %v", reasons)
	}
}

func TestShouldInspectModeGuards(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *config.MachineConfig
		wantOK bool
		reason string
	}{
		{name: "disabled", cfg: &config.MachineConfig{}, reason: "disabled"},
		{name: "default provision", cfg: &config.MachineConfig{Provision: config.ProvisionConfig{CrashArtifacts: config.CrashArtifactsConfig{Enabled: true}}}, wantOK: true},
		{name: "deprovision", cfg: &config.MachineConfig{Provision: config.ProvisionConfig{CrashArtifacts: config.CrashArtifactsConfig{Enabled: true}}, Mode: "deprovision"}, wantOK: true},
		{name: "dry-run", cfg: &config.MachineConfig{Provision: config.ProvisionConfig{CrashArtifacts: config.CrashArtifactsConfig{Enabled: true}}, Mode: "dry-run"}, reason: "dry-run"},
		{name: "standby", cfg: &config.MachineConfig{Provision: config.ProvisionConfig{CrashArtifacts: config.CrashArtifactsConfig{Enabled: true}}, Mode: "standby"}, reason: "standby"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotReason := ShouldInspect(tt.cfg)
			if gotOK != tt.wantOK || gotReason != tt.reason {
				t.Fatalf("ShouldInspect() = (%v, %q), want (%v, %q)", gotOK, gotReason, tt.wantOK, tt.reason)
			}
		})
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	entries := make(map[string][]byte)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", header.Name, err)
		}
		entries[header.Name] = data
	}
	return entries
}

func keys(entries map[string][]byte) []string {
	out := make([]string, 0, len(entries))
	for key := range entries {
		out = append(out, key)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
