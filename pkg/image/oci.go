package image

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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
		return nil, fmt.Errorf("parse OCI reference %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("pull OCI image %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("get layers for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("OCI image %q has no layers", redactedRef)
	}

	layer, err := selectDefaultOCILayer(layers)
	if err != nil {
		return nil, fmt.Errorf("select content layer for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("uncompress layer for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}
	return rc, nil
}

// ProbeOCIReference verifies that an OCI image reference resolves and has a
// usable payload layer without downloading the layer content.
func ProbeOCIReference(ctx context.Context, reference string) error {
	redactedRef := RedactOCIRef(reference)
	ref, err := name.ParseReference(reference)
	if err != nil {
		return fmt.Errorf("parse OCI reference %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("resolve OCI image %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("get layers for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}
	if len(layers) == 0 {
		return fmt.Errorf("OCI image %q has no layers", redactedRef)
	}

	if _, err := selectDefaultOCILayer(layers); err != nil {
		return fmt.Errorf("select content layer for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}
	return nil
}

func selectDefaultOCILayer(layers []v1.Layer) (v1.Layer, error) {
	var payloadLayer v1.Layer
	payloadCount := 0
	for _, layer := range layers {
		mediaType, err := layer.MediaType()
		if err != nil {
			return nil, fmt.Errorf("read layer media type: %w", err)
		}
		if isTextPlainMediaType(mediaType) {
			continue
		}
		payloadCount++
		if payloadLayer == nil {
			payloadLayer = layer
		}
	}

	switch payloadCount {
	case 0:
		return nil, fmt.Errorf("oci image has no non-text layers")
	case 1:
		return payloadLayer, nil
	default:
		return nil, fmt.Errorf("oci image has %d non-text layers; expected exactly one payload layer", payloadCount)
	}
}

func isTextPlainMediaType(mediaType types.MediaType) bool {
	value := strings.ToLower(strings.TrimSpace(string(mediaType)))
	return value == "text/plain" || strings.HasPrefix(value, "text/plain;")
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
		return nil, fmt.Errorf("parse OCI reference %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("pull OCI image %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("get layers for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("OCI image %q has no layers", redactedRef)
	}

	for _, want := range mediaTypes {
		for _, layer := range layers {
			got, err := layer.MediaType()
			if err != nil {
				return nil, fmt.Errorf("read layer media type for %q: %w", redactedRef, wrapRedactedOCIRefError(err, reference))
			}
			if got != want {
				continue
			}
			rc, err := layer.Uncompressed()
			if err != nil {
				return nil, fmt.Errorf("uncompress %s layer for %q: %w", want, redactedRef, wrapRedactedOCIRefError(err, reference))
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

const ociSchemePrefix = "oci://"

// IsOCIReference returns true if the URL uses the oci:// scheme.
func IsOCIReference(url string) bool {
	return len(url) >= len(ociSchemePrefix) && strings.EqualFold(url[:len(ociSchemePrefix)], ociSchemePrefix)
}

// IsOCIDigestReference returns true when an oci:// URL is pinned by digest.
func IsOCIDigestReference(url string) bool {
	ok, err := OCIDigestReference(url)
	return err == nil && ok
}

// OCIDigestReference reports whether an oci:// URL is pinned by digest.
func OCIDigestReference(url string) (bool, error) {
	if !IsOCIReference(url) {
		return false, fmt.Errorf("not an OCI reference")
	}
	ref, err := name.ParseReference(TrimOCIScheme(url))
	if err != nil {
		return false, fmt.Errorf("parse OCI reference: %w", err)
	}
	_, ok := ref.(name.Digest)
	return ok, nil
}

// TrimOCIScheme removes the oci:// prefix from a URL.
func TrimOCIScheme(url string) string {
	if IsOCIReference(url) {
		return url[len(ociSchemePrefix):]
	}
	return url
}
