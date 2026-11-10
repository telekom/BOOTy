//go:build linux

package image

import (
	"bytes"
	"compress/gzip"
	"context"
	"maps"
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
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/klauspost/compress/zstd"
)

// TestStreamOCIBlobTamperedUnderDigest serves partitioned images whose
// payload blob differs in its last byte from the blob that the pinned (and
// cosign-signed) manifest names: a raw artifact layer and gzip and zstd
// container layers, which go-containerregistry decompresses. Whole-disk, A/B
// and root streaming must all fail. A/B and root streaming copy only the
// partitions; they must still read the blob to its end, where
// go-containerregistry compares it with its digest.
func TestStreamOCIBlobTamperedUnderDigest(t *testing.T) {
	reg := registry.New()
	var tamper atomic.Value // digest of the blob to tamper with
	tamper.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, _ := tamper.Load().(string)
		if d == "" || r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/blobs/"+d) {
			reg.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		if len(body) > 0 {
			body[len(body)-1] ^= 0xff
		}
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	raw := testGPTImage(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	zst := enc.EncodeAll(raw, nil)
	_ = enc.Close()

	layers := []struct {
		name  string
		data  []byte
		media types.MediaType
		// digestErr: the raw artifact blob fails its digest check. A
		// compressed layer may fail in the decompressor first.
		digestErr bool
	}{
		{"raw", raw, "application/vnd.example.disk+raw", true},
		{"gzip", gz.Bytes(), types.OCILayer, false},
		{"zstd", zst, types.OCILayerZStd, false},
	}
	stream := map[string]func(url, root string) error{
		"whole-disk": func(url, root string) error {
			return Stream(context.Background(), url, root)
		},
		"ab": func(url, root string) error {
			return StreamAB(context.Background(), url, ABTargets{RootPartition: root})
		},
		"root": func(url, root string) error {
			return StreamRoot(context.Background(), url, RootTarget{RootPartition: root})
		},
	}
	for _, l := range layers {
		layer := static.NewLayer(l.data, l.media)
		img, err := mutate.Append(empty.Image, mutate.Addendum{Layer: layer})
		if err != nil {
			t.Fatal(err)
		}
		tag, err := name.NewTag(strings.TrimPrefix(srv.URL, "http://") + "/os/" + l.name + ":v1")
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
		layerDigest, err := layer.Digest()
		if err != nil {
			t.Fatal(err)
		}
		url := "oci://" + tag.Context().Digest(digest.String()).String()
		for _, mode := range []string{"whole-disk", "ab", "root"} {
			for _, tampered := range []bool{false, true} {
				tamper.Store("")
				if tampered {
					tamper.Store(layerDigest.String())
				}
				root := filepath.Join(t.TempDir(), "root.img")
				createTargetFile(t, root)
				err := stream[mode](url, root)
				if !tampered && err != nil {
					t.Fatalf("%s %s: stream of the untampered blob = %v", l.name, mode, err)
				}
				if tampered && (err == nil || (l.digestErr && !strings.Contains(err.Error(), "error verifying sha256"))) {
					t.Fatalf("%s %s: stream of a tampered blob = %v, want an error", l.name, mode, err)
				}
				if tampered {
					t.Logf("%s %s: %v", l.name, mode, err)
				}
			}
		}
	}
}
