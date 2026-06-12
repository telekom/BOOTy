package image

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// SystemdSysextMediaType is the OCI layer media type expected for raw
// systemd-sysext images.
const SystemdSysextMediaType types.MediaType = "application/vnd.systemd.sysext.image.v1+raw"

// FetchOCILayer pulls a single-layer OCI image and returns its content as
// a streaming io.ReadCloser. The reference should be a standard image ref
// (e.g. "ghcr.io/org/image:tag"). Auth uses the default Docker keychain
// (~/.docker/config.json).
func FetchOCILayer(ctx context.Context, reference string) (io.ReadCloser, error) {
	redactedRef := RedactOCIRef(reference)
	ref, err := name.ParseReference(reference)
	if err != nil {
		return nil, fmt.Errorf("parse OCI reference %q: %s", redactedRef, redactOCIRefError(err, reference))
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("pull OCI image %q: %s", redactedRef, redactOCIRefError(err, reference))
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("get layers for %q: %s", redactedRef, redactOCIRefError(err, reference))
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("OCI image %q has no layers", redactedRef)
	}

	// Use the last (topmost) layer as the image content.
	layer := layers[len(layers)-1]
	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("uncompress layer for %q: %s", redactedRef, redactOCIRefError(err, reference))
	}
	return rc, nil
}

// FetchOCILayerByMediaType pulls the first layer matching one of the requested
// media types and returns its uncompressed content.
func FetchOCILayerByMediaType(ctx context.Context, reference string, mediaTypes ...types.MediaType) (io.ReadCloser, error) {
	if len(mediaTypes) == 0 {
		return FetchOCILayer(ctx, reference)
	}

	redactedRef := RedactOCIRef(reference)
	ref, err := name.ParseReference(reference)
	if err != nil {
		return nil, fmt.Errorf("parse OCI reference %q: %s", redactedRef, redactOCIRefError(err, reference))
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("pull OCI image %q: %s", redactedRef, redactOCIRefError(err, reference))
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("get layers for %q: %s", redactedRef, redactOCIRefError(err, reference))
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("OCI image %q has no layers", redactedRef)
	}

	for _, want := range mediaTypes {
		for _, layer := range layers {
			got, err := layer.MediaType()
			if err != nil {
				return nil, fmt.Errorf("read layer media type for %q: %s", redactedRef, redactOCIRefError(err, reference))
			}
			if got != want {
				continue
			}
			rc, err := layer.Uncompressed()
			if err != nil {
				return nil, fmt.Errorf("uncompress %s layer for %q: %s", want, redactedRef, redactOCIRefError(err, reference))
			}
			return rc, nil
		}
	}

	return nil, fmt.Errorf("OCI image %q has no layer with media type %s", redactedRef, formatMediaTypes(mediaTypes))
}

func formatMediaTypes(mediaTypes []types.MediaType) string {
	values := make([]string, len(mediaTypes))
	for i, mediaType := range mediaTypes {
		values[i] = string(mediaType)
	}
	return strings.Join(values, ", ")
}

// IsOCIReference returns true if the URL uses the oci:// scheme.
func IsOCIReference(url string) bool {
	return strings.HasPrefix(url, "oci://")
}

// TrimOCIScheme removes the oci:// prefix from a URL.
func TrimOCIScheme(url string) string {
	return strings.TrimPrefix(url, "oci://")
}
