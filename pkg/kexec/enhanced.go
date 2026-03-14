package kexec

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KexecMode specifies the kexec boot mode.
type KexecMode string

const (
	// ModeDirect boots directly into the target kernel.
	ModeDirect KexecMode = "direct"
	// ModeChain stages through rescue before production.
	ModeChain KexecMode = "chain"
	// ModeRescue boots into a rescue environment.
	ModeRescue KexecMode = "rescue"
)

// KexecConfig specifies kernel selection and boot parameters.
type KexecConfig struct {
	KernelVersion string    `json:"kernelVersion,omitempty"`
	KernelPath    string    `json:"kernelPath,omitempty"`
	InitrdPath    string    `json:"initrdPath,omitempty"`
	Cmdline       string    `json:"cmdline,omitempty"`
	CmdlineAppend string    `json:"cmdlineAppend,omitempty"`
	CmdlineRemove []string  `json:"cmdlineRemove,omitempty"`
	Mode          KexecMode `json:"mode,omitempty"`
}

// KernelInfo holds information about a discovered kernel.
type KernelInfo struct {
	Version    string `json:"version"`
	KernelPath string `json:"kernelPath"`
	InitrdPath string `json:"initrdPath"`
	Cmdline    string `json:"cmdline"`
}

// EnhancedManager handles kernel selection and kexec chain loading.
type EnhancedManager struct {
	log *slog.Logger
}

// NewEnhancedManager creates a new enhanced kexec manager.
func NewEnhancedManager(log *slog.Logger) *EnhancedManager {
	return &EnhancedManager{log: log}
}

// SelectKernel finds the appropriate kernel based on config.
func (m *EnhancedManager) SelectKernel(rootPath string, cfg *KexecConfig) (*KernelInfo, error) {
	if cfg.KernelPath != "" {
		return &KernelInfo{
			KernelPath: cfg.KernelPath,
			InitrdPath: cfg.InitrdPath,
			Cmdline:    cfg.Cmdline,
		}, nil
	}

	kernels, err := DiscoverKernels(rootPath)
	if err != nil {
		return nil, fmt.Errorf("discover kernels: %w", err)
	}
	if len(kernels) == 0 {
		return nil, fmt.Errorf("no kernels found in %s", rootPath)
	}

	if cfg.KernelVersion != "" {
		for i := range kernels {
			if kernels[i].Version == cfg.KernelVersion {
				return applyOverrides(&kernels[i], cfg), nil
			}
		}
		return nil, fmt.Errorf("kernel version %s not found", cfg.KernelVersion)
	}

	// Return latest version (sorted descending).
	return applyOverrides(&kernels[0], cfg), nil
}

// DiscoverKernels finds all installed kernels under rootPath/boot.
func DiscoverKernels(rootPath string) ([]KernelInfo, error) {
	bootDir := filepath.Join(rootPath, "boot")
	matches, err := filepath.Glob(filepath.Join(bootDir, "vmlinuz-*"))
	if err != nil {
		return nil, fmt.Errorf("glob kernels: %w", err)
	}

	kernels := make([]KernelInfo, 0, len(matches))
	for _, kpath := range matches {
		base := filepath.Base(kpath)
		version := strings.TrimPrefix(base, "vmlinuz-")

		info := KernelInfo{
			Version:    version,
			KernelPath: kpath,
		}

		// Look for matching initrd.
		for _, prefix := range []string{"initrd.img-", "initramfs-"} {
			initrdPath := filepath.Join(bootDir, prefix+version)
			if _, statErr := os.Stat(initrdPath); statErr == nil {
				info.InitrdPath = initrdPath
				break
			}
			// Try with .img suffix.
			initrdImg := filepath.Join(bootDir, prefix+version+".img")
			if _, statErr := os.Stat(initrdImg); statErr == nil {
				info.InitrdPath = initrdImg
				break
			}
		}

		kernels = append(kernels, info)
	}

	// Sort by version descending (latest first).
	sort.Slice(kernels, func(i, j int) bool {
		return kernels[i].Version > kernels[j].Version
	})

	return kernels, nil
}

func applyOverrides(ki *KernelInfo, cfg *KexecConfig) *KernelInfo {
	result := *ki
	if cfg.InitrdPath != "" {
		result.InitrdPath = cfg.InitrdPath
	}
	if cfg.Cmdline != "" {
		result.Cmdline = cfg.Cmdline
	}
	if cfg.CmdlineAppend != "" {
		if result.Cmdline == "" {
			result.Cmdline = cfg.CmdlineAppend
		} else {
			result.Cmdline = result.Cmdline + " " + cfg.CmdlineAppend
		}
	}
	if len(cfg.CmdlineRemove) > 0 {
		result.Cmdline = RemoveCmdlineArgs(result.Cmdline, cfg.CmdlineRemove)
	}
	return &result
}

// RemoveCmdlineArgs removes specified arguments from a kernel command line.
func RemoveCmdlineArgs(cmdline string, remove []string) string {
	removeSet := make(map[string]bool, len(remove))
	for _, r := range remove {
		removeSet[r] = true
	}

	parts := strings.Fields(cmdline)
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		key := part
		if idx := strings.IndexByte(part, '='); idx >= 0 {
			key = part[:idx]
		}
		if !removeSet[key] {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}

// BuildRescueCmdline creates a kernel cmdline for rescue mode.
func BuildRescueCmdline(baseCmdline string) string {
	rescue := RemoveCmdlineArgs(baseCmdline, []string{"quiet", "splash"})
	rescue += " systemd.unit=rescue.target rd.shell=1"
	return strings.TrimSpace(rescue)
}
