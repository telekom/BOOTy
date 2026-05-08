//go:build linux_e2e

package linux

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/crash"
	"github.com/telekom/BOOTy/pkg/disk"
)

func TestCrashArtifactsPreWipeUploadFromExistingRoot(t *testing.T) {
	loopDev, rootNode := createCrashArtifactRoot(t)
	seedCrashFiles(t, rootNode)

	server := newCrashArtifactE2EServer(t, http.StatusOK)
	cfg := &config.MachineConfig{
		CrashArtifactsEnabled:    true,
		CrashArtifactsPrepareURL: server.URL + "/crash/prepare",
		DiskDevice:               loopDev,
		Hostname:                 "linux-e2e-node",
		Mode:                     "provision",
		Token:                    "test-token",
	}
	client := caprf.NewFromConfig(cfg)

	result, err := crash.InspectStartup(context.Background(), cfg, disk.NewManager(nil), client, crash.InspectOptions{
		MountPoint: filepath.Join(t.TempDir(), "crash-root"),
		OutputDir:  t.TempDir(),
		PstorePath: filepath.Join(t.TempDir(), "pstore-missing"),
	})
	if err != nil {
		t.Fatalf("InspectStartup() error: %v", err)
	}
	if result == nil || !result.Uploaded {
		t.Fatalf("expected upload, got %#v", result)
	}
	if server.prepareCount() != 1 || server.uploadCount() != 1 {
		t.Fatalf("prepare/upload counts = %d/%d, want 1/1", server.prepareCount(), server.uploadCount())
	}

	entries := readTarGzBytes(t, server.uploadedBody())
	for _, name := range []string{
		"manifest.json",
		"metadata.json",
		"target-root/var/crash/vmcore",
		"target-root/var/log/kern.log",
	} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("uploaded archive missing %s; entries=%v", name, mapKeys(entries))
		}
	}
	var metadata crash.HostMetadata
	if err := json.Unmarshal(entries["metadata.json"], &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if metadata.Machine.Hostname != "linux-e2e-node" {
		t.Fatalf("metadata hostname = %q, want linux-e2e-node", metadata.Machine.Hostname)
	}
	if len(metadata.Inventory) == 0 || len(metadata.Firmware) == 0 || len(metadata.BuildInfo) == 0 {
		t.Fatalf("metadata missing inventory/firmware/build info: %+v", metadata)
	}
}

func TestCrashArtifactsReadOnlyMountDoesNotModifyRoot(t *testing.T) {
	_, rootNode := createCrashArtifactRoot(t)
	seedCrashFiles(t, rootNode)

	mountPoint := filepath.Join(t.TempDir(), "readonly-root")
	mgr := disk.NewManager(nil)
	if err := mgr.MountPartitionReadOnly(context.Background(), rootNode, mountPoint); err != nil {
		t.Fatalf("MountPartitionReadOnly: %v", err)
	}
	defer func() {
		if err := mgr.Unmount(mountPoint); err != nil {
			t.Fatalf("Unmount: %v", err)
		}
	}()
	if err := os.WriteFile(filepath.Join(mountPoint, "should-not-write"), []byte("nope"), 0o644); err == nil {
		t.Fatal("expected write through read-only mount to fail")
	}
	data, err := os.ReadFile(filepath.Join(mountPoint, "var", "crash", "vmcore"))
	if err != nil {
		t.Fatalf("read seeded vmcore: %v", err)
	}
	if string(data) != "vmcore-data" {
		t.Fatalf("vmcore content changed: %q", data)
	}
}

func TestCrashArtifactsNoEvidenceSkipsUpload(t *testing.T) {
	loopDev, rootNode := createCrashArtifactRoot(t)
	mountWritableRoot(t, rootNode, func(root string) {
		writeLinuxE2EFile(t, filepath.Join(root, "var", "log", "kern.log"), "normal boot")
	})

	server := newCrashArtifactE2EServer(t, http.StatusOK)
	cfg := &config.MachineConfig{
		CrashArtifactsEnabled:    true,
		CrashArtifactsPrepareURL: server.URL + "/crash/prepare",
		DiskDevice:               loopDev,
		Mode:                     "provision",
	}
	client := caprf.NewFromConfig(cfg)
	result, err := crash.InspectStartup(context.Background(), cfg, disk.NewManager(nil), client, crash.InspectOptions{
		MountPoint: filepath.Join(t.TempDir(), "crash-root"),
		OutputDir:  t.TempDir(),
		PstorePath: filepath.Join(t.TempDir(), "pstore-missing"),
	})
	if err != nil {
		t.Fatalf("InspectStartup() error: %v", err)
	}
	if result == nil || result.Uploaded || result.SkipReason != "no-evidence" {
		t.Fatalf("expected no-evidence skip, got %#v", result)
	}
	if server.prepareCount() != 0 || server.uploadCount() != 0 {
		t.Fatalf("expected no HTTP calls, got prepare/upload %d/%d", server.prepareCount(), server.uploadCount())
	}
}

func TestCrashArtifactsUploadFailureIsNonFatal(t *testing.T) {
	loopDev, rootNode := createCrashArtifactRoot(t)
	seedCrashFiles(t, rootNode)

	server := newCrashArtifactE2EServer(t, http.StatusInternalServerError)
	cfg := &config.MachineConfig{
		CrashArtifactsEnabled:          true,
		CrashArtifactsPrepareURL:       server.URL + "/crash/prepare",
		CrashArtifactsUploadTimeoutSec: 10,
		DiskDevice:                     loopDev,
		Mode:                           "provision",
	}
	client := caprf.NewFromConfig(cfg)
	result, err := crash.InspectStartup(context.Background(), cfg, disk.NewManager(nil), client, crash.InspectOptions{
		MountPoint: filepath.Join(t.TempDir(), "crash-root"),
		OutputDir:  t.TempDir(),
		PstorePath: filepath.Join(t.TempDir(), "pstore-missing"),
	})
	if err != nil {
		t.Fatalf("InspectStartup() should be nonfatal, got error: %v", err)
	}
	if result == nil || result.UploadError == nil || result.Uploaded {
		t.Fatalf("expected nonfatal upload error, got %#v", result)
	}
}

type crashArtifactE2EServer struct {
	URL string

	mu           sync.Mutex
	prepared     int
	uploaded     int
	uploadBody   []byte
	uploadStatus int
}

func newCrashArtifactE2EServer(t *testing.T, uploadStatus int) *crashArtifactE2EServer {
	t.Helper()
	state := &crashArtifactE2EServer{uploadStatus: uploadStatus}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	state.URL = srv.URL
	mux.HandleFunc("POST /crash/prepare", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.prepared++
		state.mu.Unlock()
		var req crash.PrepareRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode prepare: %v", err)
		}
		if req.Manifest.Metadata.Machine.Mode == "" {
			t.Fatalf("prepare request missing metadata: %+v", req.Manifest.Metadata)
		}
		_ = json.NewEncoder(w).Encode(crash.PrepareResponse{
			UploadMode: crash.UploadModePresignedPUT,
			AuthMode:   crash.AuthModeNone,
			Method:     http.MethodPut,
			UploadURL:  srv.URL + "/crash/upload?X-Amz-Signature=test-secret",
		})
	})
	mux.HandleFunc("PUT /crash/upload", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		state.mu.Lock()
		state.uploaded++
		state.uploadBody = body
		state.mu.Unlock()
		w.WriteHeader(uploadStatus)
	})
	return state
}

func (s *crashArtifactE2EServer) prepareCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepared
}

func (s *crashArtifactE2EServer) uploadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploaded
}

func (s *crashArtifactE2EServer) uploadedBody() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.uploadBody...)
}

func createCrashArtifactRoot(t *testing.T) (string, string) {
	t.Helper()
	loopDev := createLoopDevice(t, 1)
	mgr := disk.NewManager(nil)
	parts, err := mgr.ParsePartitions(context.Background(), loopDev)
	if err != nil {
		t.Fatalf("ParsePartitions: %v", err)
	}
	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("FindRootPartition: %v", err)
	}
	runCmd(t, "mkfs.ext4", "-F", root.Node)
	return loopDev, root.Node
}

func seedCrashFiles(t *testing.T, rootNode string) {
	t.Helper()
	mountWritableRoot(t, rootNode, func(root string) {
		writeLinuxE2EFile(t, filepath.Join(root, "var", "crash", "vmcore"), "vmcore-data")
		writeLinuxE2EFile(t, filepath.Join(root, "var", "log", "kern.log"), "kernel panic - not syncing: e2e")
	})
}

func mountWritableRoot(t *testing.T, rootNode string, fn func(root string)) {
	t.Helper()
	mountPoint := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		t.Fatalf("mkdir mount point: %v", err)
	}
	runCmd(t, "mount", rootNode, mountPoint)
	defer runCmd(t, "umount", mountPoint)
	fn(mountPoint)
}

func writeLinuxE2EFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTarGzBytes(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	entries := make(map[string][]byte)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", header.Name, err)
		}
		entries[header.Name] = body
	}
	return entries
}

func mapKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
