package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
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

func TestProbeOCIReference(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	pushTestImageToRegistry(t, srv, "test/probe:v1", []byte("probe payload"))

	ref := fmt.Sprintf("%s/test/probe:v1", strings.TrimPrefix(srv.URL, "http://"))
	if err := ProbeOCIReference(context.Background(), ref); err != nil {
		t.Fatalf("ProbeOCIReference: %v", err)
	}
}

func TestProbeOCIReferenceRejectsMissingImage(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	ref := fmt.Sprintf("%s/test/missing:v1", strings.TrimPrefix(srv.URL, "http://"))
	if err := ProbeOCIReference(context.Background(), ref); err == nil {
		t.Fatal("expected missing image error")
	}
}

func TestIsOCIDigestReference(t *testing.T) {
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "digest",
			url:  "oci://registry.example.com/org/image@sha256:" + digest,
			want: true,
		},
		{
			name: "tag",
			url:  "oci://registry.example.com/org/image:latest",
		},
		{
			name: "http",
			url:  "https://registry.example.com/org/image@sha256:" + digest,
		},
		{
			name: "invalid",
			url:  "oci://registry.example.com/%zz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOCIDigestReference(tt.url); got != tt.want {
				t.Fatalf("IsOCIDigestReference(%q) = %t, want %t", tt.url, got, tt.want)
			}
		})
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

func TestFetchOCILayerSkipsChecksumTextLayer(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	payloadLayer := stream.NewLayer(
		io.NopCloser(strings.NewReader("initramfs-payload")),
		stream.WithMediaType(types.OCILayer),
	)
	checksumLayer := stream.NewLayer(
		io.NopCloser(strings.NewReader("sha256 deadbeef  initramfs.cpio.zst")),
		stream.WithMediaType(types.MediaType("text/plain")),
	)
	img, err := mutate.AppendLayers(empty.Image, payloadLayer, checksumLayer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/test/checksum-sidecar:v1", strings.TrimPrefix(srv.URL, "http://")))
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
	if string(got) != "initramfs-payload" {
		t.Errorf("got %q, want payload layer content", got)
	}
}

func TestFetchOCILayerRejectsOnlyTextLayers(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	checksumLayer := stream.NewLayer(
		io.NopCloser(strings.NewReader("sha256 deadbeef  initramfs.cpio.zst")),
		stream.WithMediaType(types.MediaType("text/plain")),
	)
	img, err := mutate.AppendLayers(empty.Image, checksumLayer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/test/checksum-only:v1", strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}

	_, err = FetchOCILayer(context.Background(), ref.String())
	if err == nil {
		t.Fatal("expected text-only layer error")
	}
	if !strings.Contains(err.Error(), "no non-text layers") {
		t.Fatalf("error = %q, want no non-text layers", err.Error())
	}
}

func TestFetchOCILayerByMediaType(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	sysextLayer := stream.NewLayer(
		io.NopCloser(strings.NewReader("sysext-layer")),
		stream.WithMediaType(SystemdSysextMediaType),
	)
	topLayer := stream.NewLayer(
		io.NopCloser(strings.NewReader("ordinary-top-layer")),
		stream.WithMediaType(types.OCILayer),
	)
	img, err := mutate.AppendLayers(empty.Image, sysextLayer, topLayer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	ref, err := name.ParseReference(fmt.Sprintf("%s/test/typed:v1", strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}

	rc, err := FetchOCILayerByMediaType(context.Background(), ref.String(), SystemdSysextMediaType)
	if err != nil {
		t.Fatalf("FetchOCILayerByMediaType: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "sysext-layer" {
		t.Errorf("got %q, want sysext media-type layer", got)
	}
}

func TestFetchOCILayerByMediaTypeRejectsMissingLayer(t *testing.T) {
	srv := startTestRegistry(t)
	defer srv.Close()

	pushTestImageToRegistry(t, srv, "test/no-sysext:v1", []byte("ordinary-layer"))

	ref := fmt.Sprintf("%s/test/no-sysext:v1", strings.TrimPrefix(srv.URL, "http://"))
	_, err := FetchOCILayerByMediaType(context.Background(), ref, SystemdSysextMediaType)
	if err == nil {
		t.Fatal("expected missing sysext media type error")
	}
	if !strings.Contains(err.Error(), string(SystemdSysextMediaType)) {
		t.Fatalf("error = %q, want sysext media type", err.Error())
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

func TestFetchOCILayerByMediaTypeWithRetryRedactsSensitiveRefParts(t *testing.T) {
	retryBackoffBase = 0
	t.Cleanup(func() { retryBackoffBase = time.Second })

	ref := "user:secret@registry.example.invalid/repo/sysext:dev?token=abc"
	_, err := FetchOCILayerByMediaTypeWithRetry(context.Background(), ref, SystemdSysextMediaType)
	if err == nil {
		t.Fatal("expected OCI pull error")
	}
	for _, leaked := range []string{"user", "secret", "token=abc"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error leaked sensitive OCI ref part %q: %q", leaked, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "registry.example.invalid/repo/sysext:dev") {
		t.Fatalf("error = %q, want redacted ref context", err.Error())
	}
}

func TestRedactedOCIRefErrorPreservesUnwrapAndRedacts(t *testing.T) {
	sentinel := errors.New("temporary OCI failure")
	ref := "user:secret@registry.example.invalid/repo/sysext:dev?token=abc"
	err := fmt.Errorf("OCI pull %s: %w", RedactOCIRef(ref), wrapRedactedOCIRefError(&ociRefTestError{ref: ref, err: sentinel}, ref))

	if !errors.Is(err, sentinel) {
		t.Fatalf("error does not preserve wrapped OCI error: %v", err)
	}
	for _, leaked := range []string{"user", "secret", "token=abc"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error leaked sensitive OCI ref part %q: %q", leaked, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "registry.example.invalid/repo/sysext:dev") {
		t.Fatalf("error = %q, want redacted ref context", err.Error())
	}
}

type ociRefTestError struct {
	ref string
	err error
}

func (e *ociRefTestError) Error() string {
	return "failed pulling oci://" + e.ref + ": " + e.err.Error()
}

func (e *ociRefTestError) Unwrap() error {
	return e.err
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
