//go:build linux

package kexec

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// KernelInfo describes a discovered kernel and its initramfs.
type KernelInfo struct {
	Version    string
	KernelPath string
	InitrdPath string
	IsRescue   bool
}

// KexecConfig holds configuration for an enhanced kexec boot.
type KexecConfig struct {
	KernelPath string
	InitrdPath string
	Cmdline    string
}

// EnhancedManager provides kernel discovery and version-aware kexec loading.
type EnhancedManager struct {
	rootPath string
}

// NewEnhancedManager creates an EnhancedManager for the given root filesystem.
func NewEnhancedManager(rootPath string) *EnhancedManager {
	return &EnhancedManager{rootPath: rootPath}
}

// DiscoverKernels finds all installed kernels under /boot in the root filesystem.
func (m *EnhancedManager) DiscoverKernels() ([]KernelInfo, error) {
	bootDir := filepath.Join(m.rootPath, "boot")
	entries, err := os.ReadDir(bootDir)
	if err != nil {
		return nil, fmt.Errorf("reading boot directory: %w", err)
	}

	var kernels []KernelInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "vmlinuz-") {
			continue
		}
		version := strings.TrimPrefix(name, "vmlinuz-")
		ki := KernelInfo{
			Version:    version,
			KernelPath: filepath.Join(bootDir, name),
			IsRescue:   strings.Contains(version, "rescue"),
		}
		// Look for matching initrd
		for _, initName := range []string{
			"initrd.img-" + version,
			"initramfs-" + version + ".img",
		} {
			initPath := filepath.Join(bootDir, initName)
			if _, err := os.Stat(initPath); err == nil {
				ki.InitrdPath = initPath
				break
			}
		}
		kernels = append(kernels, ki)
	}

	// Sort by version descending (newest first), rescue kernels last.
	sort.Slice(kernels, func(i, j int) bool {
		if kernels[i].IsRescue != kernels[j].IsRescue {
			return !kernels[i].IsRescue
		}
		return compareVersions(kernels[i].Version, kernels[j].Version) > 0
	})

	return kernels, nil
}

// LatestKernel returns the newest non-rescue kernel, or an error if none found.
func (m *EnhancedManager) LatestKernel() (*KernelInfo, error) {
	kernels, err := m.DiscoverKernels()
	if err != nil {
		return nil, err
	}
	for i := range kernels {
		if !kernels[i].IsRescue {
			return &kernels[i], nil
		}
	}
	return nil, fmt.Errorf("no non-rescue kernel found under %s/boot", m.rootPath)
}

// compareVersions compares two version strings numerically.
// Returns >0 if a > b, <0 if a < b, 0 if equal.
func compareVersions(a, b string) int {
	ap := splitVersion(a)
	bp := splitVersion(b)
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// splitVersion splits a version string into numeric parts.
func splitVersion(v string) []int {
	var parts []int
	for _, s := range strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	}) {
		n, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		parts = append(parts, n)
	}
	return parts
}

// RemoveCmdlineArgs returns a modified kernel cmdline with specified arguments removed.
func RemoveCmdlineArgs(cmdline string, remove ...string) string {
	removeSet := make(map[string]bool, len(remove))
	for _, r := range remove {
		removeSet[r] = true
	}
	var kept []string
	for _, arg := range strings.Fields(cmdline) {
		key := strings.SplitN(arg, "=", 2)[0]
		if !removeSet[key] {
			kept = append(kept, arg)
		}
	}
	return strings.Join(kept, " ")
}

// BuildRescueCmdline takes a normal cmdline and adds single-user mode args.
func BuildRescueCmdline(cmdline string) string {
	cmdline = RemoveCmdlineArgs(cmdline, "quiet", "splash")
	return cmdline + " single"
}
