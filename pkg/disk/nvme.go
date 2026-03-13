//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// NVMeNamespaceConfig defines the desired namespace layout for an NVMe controller.
type NVMeNamespaceConfig struct {
	Controller string          `json:"controller"` // e.g. "/dev/nvme0"
	Namespaces []NVMeNamespace `json:"namespaces"`
}

// NVMeNamespace defines a single namespace to create.
type NVMeNamespace struct {
	Label     string `json:"label"`               // Human-readable label
	SizePct   int    `json:"sizePct"`             // Percentage of total capacity
	BlockSize int    `json:"blockSize,omitempty"` // 512 or 4096 (default: 512)
}

// ParseNVMeConfig parses a JSON NVMe namespace configuration string.
func ParseNVMeConfig(data string) ([]NVMeNamespaceConfig, error) {
	var configs []NVMeNamespaceConfig
	if err := json.Unmarshal([]byte(data), &configs); err != nil {
		return nil, fmt.Errorf("parsing NVMe namespace config: %w", err)
	}
	return configs, nil
}

// DetectNVMeControllers lists NVMe controllers in /dev/.
func DetectNVMeControllers() []string {
	entries, err := os.ReadDir("/dev/")
	if err != nil {
		return nil
	}
	var controllers []string
	for _, e := range entries {
		name := e.Name()
		// Match /dev/nvme0, /dev/nvme1, etc. (not nvme0n1 which is a namespace)
		if strings.HasPrefix(name, "nvme") && !strings.Contains(name, "n") {
			controllers = append(controllers, "/dev/"+name)
		}
	}
	return controllers
}

// NVMeIdentifyController returns basic controller info via nvme id-ctrl.
func (m *Manager) NVMeIdentifyController(ctx context.Context, controller string) (map[string]string, error) {
	out, err := m.cmd.Run(ctx, "nvme", "id-ctrl", controller, "-o", "normal")
	if err != nil {
		return nil, fmt.Errorf("nvme id-ctrl %s: %w", controller, err)
	}

	info := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			info[key] = val
		}
	}
	return info, nil
}

// NVMeListNamespaces lists existing namespaces on a controller.
func (m *Manager) NVMeListNamespaces(ctx context.Context, controller string) ([]string, error) {
	out, err := m.cmd.Run(ctx, "nvme", "list-ns", controller)
	if err != nil {
		return nil, fmt.Errorf("nvme list-ns %s: %w", controller, err)
	}

	var namespaces []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, "[") {
			namespaces = append(namespaces, line)
		}
	}
	return namespaces, nil
}

// NVMeResetNamespaces deletes all namespaces and recreates a single default namespace.
// Used during deprovisioning to return disk to factory state.
func (m *Manager) NVMeResetNamespaces(ctx context.Context, controller string) error {
	slog.Info("Resetting NVMe namespaces to factory default", "controller", controller)

	// List existing namespaces.
	namespaces, err := m.NVMeListNamespaces(ctx, controller)
	if err != nil {
		return err
	}

	// Delete all namespaces.
	for i := range namespaces {
		nsid := fmt.Sprintf("%d", i+1)
		if _, err := m.cmd.Run(ctx, "nvme", "delete-ns", controller, "-n", nsid); err != nil {
			slog.Warn("Failed to delete namespace", "controller", controller, "nsid", nsid, "error", err)
		}
	}

	// Create single namespace using full capacity.
	if _, err := m.cmd.Run(ctx, "nvme", "create-ns", controller, "-s", "0", "-c", "0", "-b", "512"); err != nil {
		return fmt.Errorf("creating default namespace: %w", err)
	}

	// Attach namespace 1 to controller 0.
	if _, err := m.cmd.Run(ctx, "nvme", "attach-ns", controller, "-n", "1", "-c", "0"); err != nil {
		return fmt.Errorf("attaching default namespace: %w", err)
	}

	slog.Info("NVMe namespaces reset to factory default", "controller", controller)
	return nil
}
