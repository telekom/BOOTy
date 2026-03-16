package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
)

// InjectNoCloud writes cloud-init seed files to the NoCloud datasource directory.
func InjectNoCloud(rootPath string, ud *UserData, md *MetaData, nc *NetworkConfig) error {
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

	// Write to temp files first, then rename atomically to prevent
	// partial seed state on failure.
	for name, data := range files {
		fpath := filepath.Join(seedDir, name)
		tmp := fpath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		if err := os.Rename(tmp, fpath); err != nil {
			return fmt.Errorf("rename %s: %w", name, err)
		}
	}
	return nil
}
