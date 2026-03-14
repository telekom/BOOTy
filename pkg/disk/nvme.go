//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// nvmeControllerRE matches NVMe controller device names (nvme0, nvme1, etc.)
// but not namespace devices (nvme0n1, nvme0n1p1).
var nvmeControllerRE = regexp.MustCompile(`^nvme\d+$`)

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
		if nvmeControllerRE.MatchString(name) {
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

// nsidRE extracts namespace IDs from nvme list-ns output (e.g., "[   0]:0x1" → "1").
var nsidRE = regexp.MustCompile(`0x([0-9a-fA-F]+)`)

// NVMeListNamespaces lists existing namespace IDs on a controller.
func (m *Manager) NVMeListNamespaces(ctx context.Context, controller string) ([]string, error) {
	out, err := m.cmd.Run(ctx, "nvme", "list-ns", controller)
	if err != nil {
		return nil, fmt.Errorf("nvme list-ns %s: %w", controller, err)
	}

	var nsids []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := nsidRE.FindStringSubmatch(line); m != nil {
			// Parse hex NSID to decimal.
			nsid := fmt.Sprintf("%d", mustParseHex(m[1]))
			nsids = append(nsids, nsid)
		}
	}
	return nsids, nil
}

func mustParseHex(s string) uint64 {
	var val uint64
	_, _ = fmt.Sscanf(s, "%x", &val)
	return val
}

// NVMeSupportsMultiNS checks whether the controller supports multiple namespaces.
// Returns false for consumer drives (nn == 1).
func (m *Manager) NVMeSupportsMultiNS(ctx context.Context, controller string) (bool, error) {
	info, err := m.NVMeIdentifyController(ctx, controller)
	if err != nil {
		return false, err
	}
	nn, ok := info["nn"]
	if !ok {
		return false, nil
	}
	var count int
	if _, err := fmt.Sscanf(nn, "%d", &count); err != nil {
		return false, nil
	}
	return count > 1, nil
}

// CreateNVMeNamespace creates a namespace with the given size in blocks and block size.
func (m *Manager) CreateNVMeNamespace(ctx context.Context, controller string, sizeBlocks uint64, blockSize int) (string, error) {
	bs := blockSize
	if bs == 0 {
		bs = 512
	}

	slog.Info("Creating NVMe namespace", "controller", controller, "blocks", sizeBlocks, "blockSize", bs)

	out, err := m.cmd.Run(ctx, "nvme", "create-ns", controller,
		"-s", fmt.Sprintf("%d", sizeBlocks),
		"-c", fmt.Sprintf("%d", sizeBlocks),
		"-b", fmt.Sprintf("%d", bs))
	if err != nil {
		return "", fmt.Errorf("nvme create-ns %s: %s: %w", controller, string(out), err)
	}

	// Parse NSID from output (format: "create-ns: Success, created nsid:2").
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, "nsid:"); idx >= 0 {
			return strings.TrimSpace(line[idx+5:]), nil
		}
	}
	return "", fmt.Errorf("could not parse NSID from create-ns output: %s", string(out))
}

// AttachNVMeNamespace attaches a namespace to controller 0.
func (m *Manager) AttachNVMeNamespace(ctx context.Context, controller string, nsid string) error {
	out, err := m.cmd.Run(ctx, "nvme", "attach-ns", controller, "-n", nsid, "-c", "0")
	if err != nil {
		return fmt.Errorf("nvme attach-ns %s -n %s: %s: %w", controller, nsid, string(out), err)
	}
	return nil
}

// FormatNVMeNamespace performs a secure erase and reformat of a namespace.
func (m *Manager) FormatNVMeNamespace(ctx context.Context, device string, blockSize int) error {
	lbaf := "0" // 512-byte LBA format
	if blockSize == 4096 {
		lbaf = "1"
	}

	slog.Info("Formatting NVMe namespace", "device", device, "blockSize", blockSize)

	out, err := m.cmd.Run(ctx, "nvme", "format", device, "-l", lbaf, "-s", "1")
	if err != nil {
		return fmt.Errorf("nvme format %s: %s: %w", device, string(out), err)
	}
	return nil
}

// ApplyNVMeNamespaceLayout creates namespaces from config, using percentage-based sizing.
func (m *Manager) ApplyNVMeNamespaceLayout(ctx context.Context, cfgs []NVMeNamespaceConfig) error {
	for _, cfg := range cfgs {
		controller := cfg.Controller

		supported, err := m.NVMeSupportsMultiNS(ctx, controller)
		if err != nil {
			return fmt.Errorf("checking NVMe multi-namespace support: %w", err)
		}
		if !supported {
			slog.Warn("Controller does not support multiple namespaces, skipping", "controller", controller)
			continue
		}

		// Get total capacity from controller.
		info, err := m.NVMeIdentifyController(ctx, controller)
		if err != nil {
			return fmt.Errorf("identifying controller %s: %w", controller, err)
		}
		tnvmcap, ok := info["tnvmcap"]
		if !ok {
			return fmt.Errorf("controller %s missing tnvmcap field", controller)
		}
		var totalBytes uint64
		if _, err := fmt.Sscanf(tnvmcap, "%d", &totalBytes); err != nil {
			return fmt.Errorf("parsing tnvmcap %q: %w", tnvmcap, err)
		}

		// Delete existing namespaces first.
		if err := m.NVMeResetNamespaces(ctx, controller); err != nil {
			slog.Warn("Reset before layout failed, continuing", "controller", controller, "error", err)
		}
		// Delete the default NS that reset creates.
		if _, err := m.cmd.Run(ctx, "nvme", "delete-ns", controller, "-n", "1"); err != nil {
			slog.Warn("Failed to delete default namespace after reset", "error", err)
		}

		for _, ns := range cfg.Namespaces {
			bs := ns.BlockSize
			if bs == 0 {
				bs = 512
			}
			sizeBlocks := (totalBytes * uint64(ns.SizePct)) / (100 * uint64(bs))

			nsid, err := m.CreateNVMeNamespace(ctx, controller, sizeBlocks, bs)
			if err != nil {
				return err
			}
			if err := m.AttachNVMeNamespace(ctx, controller, nsid); err != nil {
				return err
			}
			slog.Info("Created NVMe namespace", "controller", controller, "nsid", nsid, "label", ns.Label, "sizePct", ns.SizePct)
		}
	}

	return nil
}

// NVMeResetNamespaces deletes all namespaces and recreates a single default namespace.
// Used during deprovisioning to return disk to factory state.
func (m *Manager) NVMeResetNamespaces(ctx context.Context, controller string) error {
	slog.Info("Resetting NVMe namespaces to factory default", "controller", controller)

	// List existing namespaces.
	nsids, err := m.NVMeListNamespaces(ctx, controller)
	if err != nil {
		return err
	}

	// Delete all namespaces.
	for _, nsid := range nsids {
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
