package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// validatePartitionLayoutSources checks PartitionLayoutURL and
// PartitionLayoutImagePath and trims them.
func validatePartitionLayoutSources(disk *DiskConfig, img *ImageConfig) []string {
	disk.PartitionLayoutURL = strings.TrimSpace(disk.PartitionLayoutURL)
	disk.PartitionLayoutImagePath = strings.TrimSpace(disk.PartitionLayoutImagePath)
	var errs []string
	if disk.PartitionLayoutURL != "" {
		if err := validatePartitionLayoutURL(disk.PartitionLayoutURL, BoolOr(img.AllowInsecureHTTP, false)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if disk.PartitionLayoutImagePath != "" {
		if err := validatePartitionLayoutImagePath(disk.PartitionLayoutImagePath); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if disk.PartitionLayoutURL == "" && disk.PartitionLayoutImagePath == "" {
		return errs
	}
	switch mode := strings.ToLower(strings.TrimSpace(img.Mode)); mode {
	case "", ImageModeWholeDisk:
	default:
		errs = append(errs, fmt.Sprintf("provision.disk.partitionLayoutURL and partitionLayoutImagePath need image mode whole-disk, got %q", mode))
	}
	return errs
}

func validatePartitionLayoutURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("provision.disk.partitionLayoutURL: invalid URL")
	}
	switch {
	case u.Host == "":
		return fmt.Errorf("provision.disk.partitionLayoutURL must be an absolute HTTP(S) URL")
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && allowHTTP:
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("provision.disk.partitionLayoutURL uses plain HTTP; set provision.image.allowInsecureHTTP (IMAGE_ALLOW_INSECURE_HTTP) to allow it")
	default:
		return fmt.Errorf("provision.disk.partitionLayoutURL scheme %q is not supported, use https", u.Scheme)
	}
}

func validatePartitionLayoutImagePath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || containsPathTraversal(p) {
		return fmt.Errorf("provision.disk.partitionLayoutImagePath %q must be an absolute, clean path", p)
	}
	if _, err := filepath.Match(p, ""); err != nil {
		return fmt.Errorf("provision.disk.partitionLayoutImagePath %q: %w", p, err)
	}
	return nil
}
