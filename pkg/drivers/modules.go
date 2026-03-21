//go:build linux

package drivers

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Module represents a kernel module with its metadata.
type Module struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	// UsedBy is parsed from the 4th /proc/modules field.
	UsedBy []string `json:"usedBy,omitempty"`
	// Deprecated: kept for compatibility; mirrors UsedBy.
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
	seen := make(map[string]struct{})
	var result []string

	for _, list := range [][]string{m.Common, m.NICs, m.Storage, m.USB, m.Custom} {
		for _, mod := range list {
			mod = strings.TrimSpace(mod)
			if mod == "" {
				continue
			}
			if _, ok := seen[mod]; !ok {
				seen[mod] = struct{}{}
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

	cacheMu      sync.Mutex
	loadedCache  map[string]struct{}
	loadedCached time.Time
}

// NewManager creates a kernel module manager.
func NewManager(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default().With("component", "drivers")
	}
	return &Manager{
		log:             log,
		modulesDir:      defaultModulesDir(),
		procModulesPath: "/proc/modules",
	}
}

func defaultModulesDir() string {
	kernel := runningKernelRelease()
	if kernel == "" {
		return "/lib/modules"
	}
	path := "/lib/modules/" + kernel
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return "/lib/modules"
}

func runningKernelRelease() string {
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err == nil {
		kernel := strings.TrimSpace(string(release))
		if kernel != "" {
			return kernel
		}
	}
	out, err := exec.CommandContext(context.Background(), "uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
		if len(fields) < 1 {
			continue
		}

		mod := Module{
			Name:   fields[0],
			Loaded: true,
		}
		if len(fields) >= 4 {
			deps := strings.TrimSuffix(strings.TrimPrefix(fields[3], "["), "]")
			deps = strings.TrimRight(deps, ",")
			if deps != "" && deps != "-" {
				var usedBy []string
				for _, d := range strings.Split(deps, ",") {
					d = strings.TrimSpace(d)
					if d != "" {
						usedBy = append(usedBy, d)
					}
				}
				mod.UsedBy = usedBy
				mod.Dependencies = append([]string(nil), usedBy...)
			}
		}
		modules = append(modules, mod)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", m.procModulesPath, err)
	}
	return modules, nil
}

// FindModule searches for a .ko file in the modules directory.
func (m *Manager) FindModule(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("invalid module name: %q", name)
	}

	roots := make([]string, 0, 2)
	if filepath.Clean(m.modulesDir) == "/lib/modules" {
		kernel := runningKernelRelease()
		if kernel != "" {
			roots = append(roots, filepath.Join(m.modulesDir, kernel))
		}
	}
	if len(roots) == 0 {
		roots = append(roots, filepath.Join(m.modulesDir, "kernel"), m.modulesDir)
	}

	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}

		found, err := findModuleInTree(root, name)
		if err != nil {
			return "", err
		}
		if found != "" {
			return found, nil
		}
	}

	return "", fmt.Errorf("module %q not found in %s", name, m.modulesDir)
}

func findModuleInTree(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, name+".ko") {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk modules dir %s: %w", root, err)
	}
	return found, nil
}

// IsLoaded checks if a module is currently loaded by reading procModulesPath.
func (m *Manager) IsLoaded(name string) bool {
	normalized := strings.ReplaceAll(name, "-", "_")

	m.cacheMu.Lock()
	if time.Since(m.loadedCached) > time.Second || m.loadedCache == nil {
		cache, err := m.readLoadedSet()
		if err != nil {
			m.cacheMu.Unlock()
			return false
		}
		m.loadedCache = cache
		m.loadedCached = time.Now()
	}
	_, ok := m.loadedCache[normalized]
	m.cacheMu.Unlock()
	return ok
}

func (m *Manager) readLoadedSet() (map[string]struct{}, error) {
	data, err := os.ReadFile(m.procModulesPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.procModulesPath, err)
	}
	loaded := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			loaded[fields[0]] = struct{}{}
		}
	}
	return loaded, nil
}
