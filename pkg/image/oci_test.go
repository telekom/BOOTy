package image

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
)

func startTestRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(registry.New())
}

func pushTestImageToRegistry(t *testing.T, srv *httptest.Server, repoTag string, data []byte) {
	t.Helper()

	layer := stream.NewLayer(io.NopCloser(strings.NewReader(string(data))))
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/%s", strings.TrimPrefix(srv.URL, "http://"), repoTag))
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}
}

func TestFetchOCILayer(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	payload := []byte("hello from OCI layer")
	pushTestImageToRegistry(t, srv, "test/layer:v1", payload)

	ref := fmt.Sprintf("%s/test/layer:v1", strings.TrimPrefix(srv.URL, "http://"))
	rc, err := FetchOCILayer(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchOCILayer: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestFetchOCILayerMultiLayer(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	layer1 := stream.NewLayer(io.NopCloser(strings.NewReader("layer-1")))
	layer2 := stream.NewLayer(io.NopCloser(strings.NewReader("layer-2-latest")))
	img, err := mutate.AppendLayers(empty.Image, layer1, layer2)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/test/multi:v1", strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}

	rc, err := FetchOCILayer(context.Background(), ref.String())
	if err != nil {
		t.Fatalf("FetchOCILayer: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "layer-2-latest" {
		t.Errorf("got %q, want last layer content", got)
	}
}

func TestFetchOCILayerNotFound(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	ref := fmt.Sprintf("%s/does/not-exist:latest", strings.TrimPrefix(srv.URL, "http://"))
	_, err := FetchOCILayer(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error for non-existent image")
	}
}

func TestFetchOCILayerInvalidRef(t *testing.T) {
	_, err := FetchOCILayer(context.Background(), ":::invalid")
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}
}

func TestFetchOCILayerNoLayers(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	// Push an image with no layers
	ref, err := name.ParseReference(fmt.Sprintf("%s/test/empty:v1", strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	if err := remote.Write(ref, empty.Image); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}

	_, err = FetchOCILayer(context.Background(), ref.String())
	if err == nil {
		t.Fatal("expected error for image with no layers")
	}
	if !strings.Contains(err.Error(), "no layers") {
		t.Errorf("error = %q, want to contain 'no layers'", err.Error())
	}
}

func TestFetchOCILayerContextCancelled(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	pushTestImageToRegistry(t, srv, "test/cancel:v1", []byte("data"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	ref := fmt.Sprintf("%s/test/cancel:v1", strings.TrimPrefix(srv.URL, "http://"))
	_, err := FetchOCILayer(ctx, ref)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// Ensure stream.NewLayer satisfies v1.Layer interface (compile-time check).
var _ v1.Layer = (*stream.Layer)(nil)
