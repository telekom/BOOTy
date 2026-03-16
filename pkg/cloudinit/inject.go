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

	// Two-phase write: first write all temp files, then rename atomically.
	// This prevents partial seed state — if any write fails the final files
	// remain untouched until all temps have been written successfully.
	type entry struct{ name, tmp, final string }
	var entries []entry
	for name, data := range files {
		fpath := filepath.Join(seedDir, name)
		tmp := fpath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			// Clean up any temp files already written before returning.
			for _, e := range entries {
				_ = os.Remove(e.tmp)
			}
			_ = os.Remove(tmp)
			return fmt.Errorf("write %s: %w", name, err)
		}
		entries = append(entries, entry{name: name, tmp: tmp, final: fpath})
	}
	// All temp files written — now rename to final paths.
	for _, e := range entries {
		if err := os.Rename(e.tmp, e.final); err != nil {
			return fmt.Errorf("rename %s: %w", e.name, err)
		}
	}
	return nil
}
