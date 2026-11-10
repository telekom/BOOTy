//go:build linux

package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
)

// TestStreamOCIBlobTamperedUnderDigest serves a partitioned image whose
// last byte, after the root partition, differs from the blob that the
// pinned (and cosign-signed) manifest names. A/B and root streaming copy
// only the partitions; they must still read the blob to its end, where
// go-containerregistry compares it with its digest, and fail.
func TestStreamOCIBlobTamperedUnderDigest(t *testing.T) {
	reg := registry.New()
	var tamper atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tamper.Load() || r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/blobs/") {
			reg.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		if len(body) > 0 {
			body[len(body)-1] ^= 0xff
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	img, err := mutate.Append(empty.Image, mutate.Addendum{Layer: static.NewLayer(testGPTImage(t), "application/vnd.example.disk+raw")})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(strings.TrimPrefix(srv.URL, "http://") + "/os/partitioned:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
	digest, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	url := "oci://" + tag.Context().Digest(digest.String()).String()

	stream := map[string]func(root string) error{
		"ab": func(root string) error {
			return StreamAB(context.Background(), url, ABTargets{RootPartition: root})
		},
		"root": func(root string) error {
			return StreamRoot(context.Background(), url, RootTarget{RootPartition: root})
		},
	}
	for _, mode := range []string{"ab", "root"} {
		for _, tampered := range []bool{false, true} {
			tamper.Store(tampered)
			root := filepath.Join(t.TempDir(), "root.img")
			createTargetFile(t, root)
			err := stream[mode](root)
			if !tampered && err != nil {
				t.Fatalf("%s: stream of the untampered blob = %v", mode, err)
			}
			if tampered && (err == nil || !strings.Contains(err.Error(), "error verifying sha256")) {
				t.Fatalf("%s: stream of a tampered blob = %v, want a digest error", mode, err)
			}
		}
	}
}
