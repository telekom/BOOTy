//go:build linux

package drivers

import (
	"bufio"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Module represents a kernel module with its metadata.
type Module struct {
	Name         string   `json:"name"`
	Path         string   `json:"path,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Loaded       bool     `json:"loaded"`
}

// Manifest defines which modules to load per flavor.
type Manifest struct {
	Common  []string `yaml:"common" json:"common"`
	NICs    []string `yaml:"nics" json:"nics,omitempty"`
	Storage []string `yaml:"storage" json:"storage,omitempty"`
	USB     []string `yaml:"usb" json:"usb,omitempty"`
	Custom  []string `yaml:"custom" json:"custom,omitempty"`
}

// AllModules returns all modules from the manifest in load order.
func (m *Manifest) AllModules() []string {
	seen := make(map[string]bool)
	var result []string

	for _, list := range [][]string{m.Common, m.NICs, m.Storage, m.USB, m.Custom} {
		for _, mod := range list {
			if !seen[mod] {
				seen[mod] = true
				result = append(result, mod)
			}
		}
	}
	return result
}

// Manager handles kernel module operations.
type Manager struct {
	log             *slog.Logger
	modulesDir      string
	procModulesPath string
}

// NewManager creates a kernel module manager.
func NewManager(log *slog.Logger) *Manager {
	return &Manager{
		log:             log,
		modulesDir:      "/lib/modules",
		procModulesPath: "/proc/modules",
	}
}

// ListLoaded returns currently loaded kernel modules from /proc/modules.
func (m *Manager) ListLoaded() ([]Module, error) {
	f, err := os.Open(m.procModulesPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", m.procModulesPath, err)
	}
	defer func() { _ = f.Close() }()

	var modules []Module
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 1 {
			mod := Module{
				Name:   fields[0],
				Loaded: true,
			}
			if len(fields) >= 4 {
				deps := strings.TrimSuffix(strings.TrimPrefix(fields[3], "["), "]")
				if deps != "" && deps != "-" {
					mod.Dependencies = strings.Split(deps, ",")
				}
			}
			modules = append(modules, mod)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", m.procModulesPath, err)
	}
	return modules, nil
}

// FindModule searches for a .ko file in the modules directory.
func (m *Manager) FindModule(name string) (string, error) {
	var found string
	err := filepath.WalkDir(m.modulesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		// Match name.ko, name.ko.xz, name.ko.zst, etc.
		if strings.HasPrefix(base, name+".ko") {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk modules dir: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("module %q not found in %s", name, m.modulesDir)
	}
	return found, nil
}

// IsLoaded checks if a module is currently loaded.
func IsLoaded(name string) bool {
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	// Module names in /proc/modules use underscores
	normalized := strings.ReplaceAll(name, "-", "_")
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 && fields[0] == normalized {
			return true
		}
	}
	return false
}
