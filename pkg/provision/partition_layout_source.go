//go:build linux

package provision

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/network"
)

// maxPartitionLayoutSize bounds a layout document from a URL or the image.
const maxPartitionLayoutSize = 1 << 20

var (
	partitionLayoutHTTPClient = network.NewHTTPClient(2 * time.Minute)
	fetchPartitionLayoutFn    = fetchPartitionLayoutURL
)

// fetchPartitionLayout fetches PARTITION_LAYOUT_URL before any disk is
// changed. PARTITION_LAYOUT, set in the host vars, overrides it.
func (o *Orchestrator) fetchPartitionLayout(ctx context.Context) error {
	d := &o.cfg.Provision.Disk
	if d.PartitionLayout != nil || d.PartitionLayoutURL == "" {
		return nil
	}
	data, err := fetchPartitionLayoutFn(ctx, d.PartitionLayoutURL)
	if err != nil {
		return err
	}
	if err := o.useSourcePartitionLayout(data, "PARTITION_LAYOUT_URL"); err != nil {
		return &PermanentError{Err: err}
	}
	o.log.Info("partition layout from URL", "url", image.RedactURL(d.PartitionLayoutURL),
		"partitions", len(d.PartitionLayout.Partitions))
	return nil
}

func fetchPartitionLayoutURL(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, &PermanentError{Err: fmt.Errorf("partition layout URL %s: invalid request", image.RedactURL(rawURL))}
	}
	resp, err := partitionLayoutHTTPClient.Do(req) //nolint:gosec // configured layout URL
	if err != nil {
		return nil, &TransientError{Err: fmt.Errorf("fetch partition layout %s: %s", image.RedactURL(rawURL), image.RedactSourceError(err, rawURL))}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("fetch partition layout %s: %s", image.RedactURL(rawURL), resp.Status)
		if isRetryableSysextHTTPStatus(resp.StatusCode) {
			return nil, &TransientError{Err: err}
		}
		return nil, &PermanentError{Err: err}
	}
	data, err := readBounded(resp.Body, maxPartitionLayoutSize)
	if err != nil {
		return nil, &TransientError{Err: fmt.Errorf("read partition layout %s: %w", image.RedactURL(rawURL), err)}
	}
	return data, nil
}

// loadImagePartitionLayout reads PARTITION_LAYOUT_IMAGE_PATH from the root
// of the whole-disk image that stream-image wrote, then creates that layout
// and writes the image root into it, like a PARTITION_LAYOUT from the vars.
// PARTITION_LAYOUT and PARTITION_LAYOUT_URL override the image layout.
//
// ponytail: the image is downloaded and written twice, once whole-disk to
// read the layout and once into the layout root. Read the layout from the
// image stream instead if the second download matters.
func (o *Orchestrator) loadImagePartitionLayout(ctx context.Context) error {
	d := &o.cfg.Provision.Disk
	if d.PartitionLayout != nil || d.PartitionLayoutImagePath == "" {
		return nil
	}
	data, err := o.readImagePartitionLayout(ctx, d.PartitionLayoutImagePath)
	if err != nil {
		return &PermanentError{Err: err}
	}
	if err := o.useSourcePartitionLayout(data, "PARTITION_LAYOUT_IMAGE_PATH"); err != nil {
		return &PermanentError{Err: err}
	}
	o.log.Info("partition layout from image", "path", d.PartitionLayoutImagePath, "partitions", len(d.PartitionLayout.Partitions))
	if err := o.disk.RequireLVMTools(ctx, d.PartitionLayout); err != nil {
		return err
	}
	if err := o.validatePartitionLayoutConfig(); err != nil {
		return &PermanentError{Err: err}
	}
	for _, step := range []Step{
		{"apply-partition-layout", o.applyPartitionLayout},
		{"stream-image", o.streamImage},
		{"partprobe", o.partprobe},
		{"parse-partitions", o.parsePartitions},
	} {
		err := step.Fn(ctx)
		if policy, ok := DefaultPolicies[step.Name]; ok && err != nil {
			err = WithRetry(ctx, step.Name, policy, step.Fn)
		}
		if err != nil {
			return fmt.Errorf("%s with the image partition layout: %w", step.Name, err)
		}
	}
	return nil
}

// readImagePartitionLayout mounts the written root read-only and reads the
// one file that pattern matches. The file and its parent directories must
// not be symlinks, so the read stays in the image root.
func (o *Orchestrator) readImagePartitionLayout(ctx context.Context, pattern string) ([]byte, error) {
	mnt, err := os.MkdirTemp("", "booty-image-layout-*")
	if err != nil {
		return nil, fmt.Errorf("create image layout mountpoint: %w", err)
	}
	defer func() { _ = os.Remove(mnt) }()
	if err := mountReadOnlyPart(ctx, o.disk, o.rootPartition, mnt); err != nil {
		return nil, fmt.Errorf("mount image root %s: %w", o.rootPartition, err)
	}
	defer func() {
		if err := o.disk.Unmount(mnt); err != nil {
			o.log.Warn("failed to unmount image root after reading the layout", "mountpoint", mnt, "error", err)
		}
	}()
	return readPartitionLayoutFile(mnt, pattern)
}

func readPartitionLayoutFile(root, pattern string) ([]byte, error) {
	matches, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		return nil, fmt.Errorf("partition layout path %s: %w", pattern, err)
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("partition layout path %s matches %d files in the image, want exactly 1", pattern, len(matches))
	}
	file := matches[0]
	if err := ensureMountpointWithinRoot(root, file); err != nil {
		return nil, fmt.Errorf("partition layout file: %w", err)
	}
	info, err := os.Lstat(file)
	if err != nil {
		return nil, fmt.Errorf("partition layout file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("partition layout file %s in the image is not a regular file", pattern)
	}
	f, err := os.Open(file) //nolint:gosec // checked to stay below root
	if err != nil {
		return nil, fmt.Errorf("open partition layout file: %w", err)
	}
	defer func() { _ = f.Close() }()
	return readBounded(f, maxPartitionLayoutSize)
}

// useSourcePartitionLayout parses a layout from a URL or the image and makes
// it the layout of this run. The target disk comes from the host selection,
// so such a layout must not name a device.
func (o *Orchestrator) useSourcePartitionLayout(data []byte, source string) error {
	layout, err := config.ParsePartitionLayout(string(data))
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if layout.Device != "" {
		return fmt.Errorf("%s: the layout must not set device; select the disk with DISK_DEVICE or DISK_SERIAL_NUMBER", source)
	}
	o.cfg.Provision.Disk.PartitionLayout = layout
	o.sourcePartitionLayout = layout
	return nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}
