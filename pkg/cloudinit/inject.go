package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
)

// InjectNoCloud writes cloud-init seed files to the NoCloud datasource directory.
func InjectNoCloud(rootPath string, ud *UserData, md *MetaData, nc *NetworkConfig) error {
	if rootPath == "" || !filepath.IsAbs(rootPath) {
		return fmt.Errorf("rootPath must be a non-empty absolute path, got %q", rootPath)
	}
	if ud == nil || md == nil || nc == nil {
		return fmt.Errorf("user-data, meta-data, and network-config must not be nil")
	}
	seedDir := filepath.Join(rootPath, "var", "lib", "cloud", "seed", "nocloud")
	if err := os.MkdirAll(seedDir, 0o700); err != nil {
		return fmt.Errorf("create nocloud seed dir: %w", err)
	}

	userData, err := ud.Render()
	if err != nil {
		return fmt.Errorf("render user-data: %w", err)
	}

	metaData, err := md.Render()
	if err != nil {
		return fmt.Errorf("render meta-data: %w", err)
	}

	networkConfig, err := nc.Render()
	if err != nil {
		return fmt.Errorf("render network-config: %w", err)
	}

	files := map[string][]byte{
		"user-data":      userData,
		"meta-data":      metaData,
		"network-config": networkConfig,
	}

	// Two-phase write: write all files to a temp directory, then rename each
	// to its final path. If any write fails, the temp dir is cleaned up and
	// existing seed files are untouched.
	tmpDir, err := os.MkdirTemp(filepath.Dir(seedDir), ".nocloud-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	for name, data := range files {
		tmp := filepath.Join(tmpDir, name)
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	// All temp files written — now rename to final paths.
	for name := range files {
		src := filepath.Join(tmpDir, name)
		dst := filepath.Join(seedDir, name)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %s: %w", name, err)
		}
	}
	return nil
}
