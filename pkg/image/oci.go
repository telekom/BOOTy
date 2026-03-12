package image

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// FetchOCILayer pulls a single-layer OCI image and returns its content as
// a streaming io.ReadCloser. The reference should be a standard image ref
// (e.g. "ghcr.io/org/image:tag"). Auth uses the default Docker keychain
// (~/.docker/config.json).
func FetchOCILayer(ctx context.Context, reference string) (io.ReadCloser, error) {
	ref, err := name.ParseReference(reference)
	if err != nil {
		return nil, fmt.Errorf("parse OCI reference %q: %w", reference, err)
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("pull OCI image %q: %w", reference, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("get layers for %q: %w", reference, err)
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("OCI image %q has no layers", reference)
	}

	// Use the last (topmost) layer as the image content.
	layer := layers[len(layers)-1]
	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("uncompress layer for %q: %w", reference, err)
	}
	return rc, nil
}

// IsOCIReference returns true if the URL uses the oci:// scheme.
func IsOCIReference(url string) bool {
	return strings.HasPrefix(url, "oci://")
}

// TrimOCIScheme removes the oci:// prefix from a URL.
func TrimOCIScheme(url string) string {
	return strings.TrimPrefix(url, "oci://")
}
