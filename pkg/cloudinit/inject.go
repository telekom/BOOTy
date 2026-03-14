package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
)

// InjectNoCloud writes cloud-init seed files to the NoCloud datasource directory.
func InjectNoCloud(rootPath string, ud *UserData, md *MetaData, nc *NetworkConfig) error {
	seedDir := filepath.Join(rootPath, "var", "lib", "cloud", "seed", "nocloud")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		return fmt.Errorf("create nocloud seed dir: %w", err)
	}

	userData, err := ud.Render()
	if err != nil {
		return err
	}

	metaData, err := md.Render()
	if err != nil {
		return err
	}

	networkConfig, err := nc.Render()
	if err != nil {
		return err
	}

	files := map[string][]byte{
		"user-data":      userData,
		"meta-data":      metaData,
		"network-config": networkConfig,
	}

	for name, data := range files {
		fpath := filepath.Join(seedDir, name)
		if err := os.WriteFile(fpath, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}
