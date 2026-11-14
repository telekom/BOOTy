//go:build e2e

package kvm

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestProvisionImageRedirectOptionsQEMU serves the image URL as a 302 to a
// second server. By default BOOTy follows the redirect and provisions;
// with IMAGE_FOLLOW_REDIRECTS=false the download fails with the redirect
// error and the second server gets no request (PSM-14.46 "no redirects").
func TestProvisionImageRedirectOptionsQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	image := compressGzip(t, createTestDiskImage(t, 512))
	var targetHits atomic.Int32
	target := startImageHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		http.ServeFile(w, r, image)
	}))
	redirector := startImageHTTPServer(t, http.RedirectHandler(target+"/image.gz", http.StatusFound))

	for _, tt := range []struct {
		name, follow string
		wantOK       bool
	}{
		{name: "default follows", wantOK: true},
		{name: "refused", follow: "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targetHits.Store(0)
			hostname := "image-redirect"
			targetDisk := filepath.Join(t.TempDir(), "target.qcow2")
			run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
			vars := map[string]string{
				"HOSTNAME":                  hostname,
				"IMAGE_ALLOW_INSECURE_HTTP": "true",
				"IMAGE":                     redirector + "/image.gz",
				"MODE":                      "provision",
				"DISK_DEVICE":               "/dev/vda",
			}
			if tt.follow != "" {
				vars["IMAGE_FOLLOW_REDIRECTS"] = tt.follow
			}
			output := runQEMUProvision(t, findKernel(t), buildProvisionInitramfs(t, vars), targetDisk, 6*time.Minute)
			t.Logf("output tail:\n%s", tail(output, 3000))
			if !tt.wantOK {
				if !strings.Contains(string(output), "IMAGE_FOLLOW_REDIRECTS=false") {
					t.Fatal("console does not show the refused redirect")
				}
				if n := targetHits.Load(); n != 0 {
					t.Fatalf("redirect target got %d requests, want 0", n)
				}
				return
			}
			if targetHits.Load() == 0 {
				t.Fatal("redirect target got no request")
			}
			rootMount, cleanup := mountQcow2(t, targetDisk)
			defer cleanup()
			if got := strings.TrimSpace(readProvisionedFile(t, rootMount, "etc/hostname")); got != hostname {
				t.Errorf("hostname: got %q, want %q", got, hostname)
			}
		})
	}
}

// startImageHTTPServer serves handler on all interfaces and returns its URL
// as the QEMU guest sees it.
func startImageHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	return fmt.Sprintf("http://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port)
}
