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

// FetchOCILayer pulls the payload layer of an OCI image (selectDefaultOCILayer)
// and returns its content as a streaming io.ReadCloser, decompressed by
// go-containerregistry. The reference should be a standard image ref
// (e.g. "ghcr.io/org/image:tag"). Auth uses the default Docker keychain
// (~/.docker/config.json).
func FetchOCILayer(ctx context.Context, reference string) (io.ReadCloser, error) {
	layer, err := selectOCIImageLayer(ctx, reference)
	if err != nil {
		return nil, err
	}
	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("uncompress layer for %q: %w", RedactOCIRef(reference), wrapRedactedOCIRefError(err, reference))
	}
	return rc, nil
}

// FetchOCIImageBlob returns the image in the payload layer of an OCI image.
// A container layer type (types.MediaType.IsLayer, for example
// application/vnd.oci.image.layer.v1.tar+gzip) compresses the content for
// transport: go-containerregistry removes that compression, as before. Any
// other type is an artifact pushed as a file (oras push
// image.raw.zst:application/vnd.tcaas.node-image+raw.zst): the blob is
// returned as pushed, checked against the layer digest while it is read, and
// BOOTy decompresses it itself, so a download-scope IMAGE_CHECKSUM covers the
// pushed file like for an HTTP source.
func FetchOCIImageBlob(ctx context.Context, reference string) (io.ReadCloser, error) {
	layer, err := selectOCIImageLayer(ctx, reference)
	if err != nil {
		return nil, err
	}
	mediaType, err := layer.MediaType()
	if err != nil {
		return nil, fmt.Errorf("read layer media type for %q: %w", RedactOCIRef(reference), wrapRedactedOCIRefError(err, reference))
	}
	if mediaType.IsLayer() {
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, fmt.Errorf("uncompress layer for %q: %w", RedactOCIRef(reference), wrapRedactedOCIRefError(err, reference))
		}
		return rc, nil
	}
	rc, err := layer.Compressed()
	if err != nil {
		return nil, fmt.Errorf("fetch layer blob for %q: %w", RedactOCIRef(reference), wrapRedactedOCIRefError(err, reference))
	}
	return rc, nil
}

// ProbeOCIReference verifies that an OCI image reference resolves and has a
// usable payload layer without downloading the layer content.
func ProbeOCIReference(ctx context.Context, reference string) error {
	_, err := selectOCIImageLayer(ctx, reference)
	return err
}

// selectOCIImageLayer resolves an OCI image and selects its payload layer.
func selectOCIImageLayer(ctx context.Context, reference string) (v1.Layer, error) {
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
	return layer, nil
}

// tcaasRawImageMediaType prefixes the media type of the raw image layer that
// the T-CaaS image builder pushes (+raw.lz4, +raw.zst) next to its
// ansible facts and an optional +qcow2 layer.
const tcaasRawImageMediaType = "application/vnd.tcaas.node-image+raw"

func selectDefaultOCILayer(layers []v1.Layer) (v1.Layer, error) {
	var payloadLayer v1.Layer
	var rawImageLayers []v1.Layer
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
		if strings.HasPrefix(string(mediaType), tcaasRawImageMediaType) {
			rawImageLayers = append(rawImageLayers, layer)
		}
	}

	switch {
	case payloadCount == 0:
		return nil, fmt.Errorf("oci image has no non-text layers")
	case payloadCount == 1:
		return payloadLayer, nil
	case len(rawImageLayers) == 1:
		return rawImageLayers[0], nil
	default:
		return nil, fmt.Errorf("oci image has %d non-text layers; expected exactly one payload layer or one %s* layer",
			payloadCount, tcaasRawImageMediaType)
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
