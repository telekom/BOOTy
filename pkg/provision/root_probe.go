//go:build linux

package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/telekom/BOOTy/pkg/disk"
)

// probeRootPartition picks the first Linux filesystem partition that holds
// a root filesystem, like select_root_partition in the CAPRF legacy
// provision.sh: /etc/os-release, or /etc and /usr directories.
func (o *Orchestrator) probeRootPartition(ctx context.Context, parts []disk.Partition, ambiguous error) (*disk.Partition, error) {
	inspect, err := os.MkdirTemp("", "booty-root-probe-")
	if err != nil {
		return nil, fmt.Errorf("create root probe mountpoint: %w", err)
	}
	defer func() { _ = os.Remove(inspect) }()

	for i := range parts {
		if !isLinuxRootPartition(parts[i]) {
			continue
		}
		if err := o.disk.MountPartitionInspect(ctx, parts[i].Node, inspect); err != nil {
			o.log.Info("root probe: cannot mount partition", "partition", parts[i].Node, "error", err)
			continue
		}
		isRoot := looksLikeRootFilesystem(inspect)
		if err := o.disk.Unmount(inspect); err != nil {
			return nil, fmt.Errorf("unmount root probe %s: %w", parts[i].Node, err)
		}
		if isRoot {
			o.log.Info("root probe: selected root partition", "partition", parts[i].Node)
			return &parts[i], nil
		}
	}
	return nil, fmt.Errorf("%w; no candidate holds a root filesystem", ambiguous)
}

func looksLikeRootFilesystem(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "etc", "os-release")); err == nil && info.Mode().IsRegular() {
		return true
	}
	etc, etcErr := os.Stat(filepath.Join(dir, "etc"))
	usr, usrErr := os.Stat(filepath.Join(dir, "usr"))
	return etcErr == nil && usrErr == nil && etc.IsDir() && usr.IsDir()
}
