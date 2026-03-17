//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
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
	LBAFIndex int    `json:"lbafIndex,omitempty"` // Explicit LBA format index (overrides blockSize heuristic)
}

// nvmeControllerPathRE validates that a controller path looks like /dev/nvme0, /dev/nvme1, etc.
var nvmeControllerPathRE = regexp.MustCompile(`^/dev/nvme\d+$`)

// checkNVMeControllerUniqueness returns an error if any controller path appears more than once.
func checkNVMeControllerUniqueness(configs []NVMeNamespaceConfig) error {
	seen := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		if seen[cfg.Controller] {
			return fmt.Errorf("duplicate controller %q in NVMe namespace config", cfg.Controller)
		}
		seen[cfg.Controller] = true
	}
	return nil
}

// ParseNVMeConfig parses a JSON NVMe namespace configuration string.
func ParseNVMeConfig(data string) ([]NVMeNamespaceConfig, error) {
	var configs []NVMeNamespaceConfig
	if err := json.Unmarshal([]byte(data), &configs); err != nil {
		return nil, fmt.Errorf("parsing NVMe namespace config: %w", err)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("NVMe namespace config must contain at least one controller entry")
	}
	if err := checkNVMeControllerUniqueness(configs); err != nil {
		return nil, err
	}
	for i, cfg := range configs {
		if cfg.Controller == "" {
			return nil, fmt.Errorf("config[%d]: controller must not be empty", i)
		}
		if !nvmeControllerPathRE.MatchString(cfg.Controller) {
			return nil, fmt.Errorf("config[%d]: controller %q must be an NVMe controller path (e.g. /dev/nvme0)", i, cfg.Controller)
		}
		if len(cfg.Namespaces) == 0 {
			return nil, fmt.Errorf("config[%d]: namespaces must not be empty", i)
		}
		totalPct := 0
		for j, ns := range cfg.Namespaces {
			if ns.SizePct <= 0 || ns.SizePct > 100 {
				return nil, fmt.Errorf("config[%d].namespaces[%d]: sizePct %d out of range 1-100", i, j, ns.SizePct)
			}
			if ns.BlockSize != 0 && ns.BlockSize != 512 && ns.BlockSize != 4096 {
				return nil, fmt.Errorf("config[%d].namespaces[%d]: blockSize %d must be 512 or 4096", i, j, ns.BlockSize)
			}
			totalPct += ns.SizePct
		}
		if totalPct > 100 {
			return nil, fmt.Errorf("config[%d]: total sizePct %d exceeds 100%%", i, totalPct)
		}
	}
	return configs, nil
}

// DetectNVMeControllers lists NVMe controllers in /dev/.
func DetectNVMeControllers() []string {
	entries, err := os.ReadDir("/dev/")
	if err != nil {
		slog.Warn("failed to read /dev/ for NVMe detection", "error", err)
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
	if !nvmeControllerPathRE.MatchString(controller) {
		return nil, fmt.Errorf("invalid NVMe controller path %q", controller)
	}
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
	if !nvmeControllerPathRE.MatchString(controller) {
		return nil, fmt.Errorf("invalid NVMe controller path %q", controller)
	}
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
		if match := nsidRE.FindStringSubmatch(line); match != nil {
			// Parse hex NSID to decimal.
			nsid, err := parseHex(match[1])
			if err != nil {
				continue
			}
			nsidStr := fmt.Sprintf("%d", nsid)
			nsids = append(nsids, nsidStr)
		}
	}
	return nsids, nil
}

func parseHex(s string) (uint64, error) {
	val, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing hex NSID %q: %w", s, err)
	}
	return val, nil
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
		return false, fmt.Errorf("parsing namespace count %q: %w", nn, err)
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
func (m *Manager) AttachNVMeNamespace(ctx context.Context, controller, nsid string) error {
	out, err := m.cmd.Run(ctx, "nvme", "attach-ns", controller, "-n", nsid, "-c", "0")
	if err != nil {
		return fmt.Errorf("nvme attach-ns %s -n %s: %s: %w", controller, nsid, string(out), err)
	}
	return nil
}

// FormatNVMeNamespace performs a secure erase and reformat of a namespace.
// lbafIndex selects the device LBA format; use -1 to auto-select based on blockSize
// (0=512-byte sectors, 1=4096-byte sectors — valid only when the device LBAF table
// matches this common ordering; prefer explicit lbafIndex from nvme id-ns when possible).
func (m *Manager) FormatNVMeNamespace(ctx context.Context, device string, blockSize, lbafIndex int) error {
	var lbaf string
	switch {
	case lbafIndex >= 0:
		lbaf = fmt.Sprintf("%d", lbafIndex)
	case blockSize == 4096:
		lbaf = "1" // common LBAF index for 4096-byte sectors; may differ by device
	default:
		lbaf = "0" // common LBAF index for 512-byte sectors
	}

	slog.Info("Formatting NVMe namespace", "device", device, "blockSize", blockSize)

	out, err := m.cmd.Run(ctx, "nvme", "format", device, "-l", lbaf, "-s", "1")
	if err != nil {
		return fmt.Errorf("nvme format %s: %s: %w", device, string(out), err)
	}
	return nil
}

// ApplyNVMeNamespaceLayout creates namespaces from config, using percentage-based sizing.
// Returns created NSIDs per controller in creation order.
func (m *Manager) ApplyNVMeNamespaceLayout(ctx context.Context, cfgs []NVMeNamespaceConfig) (map[string][]string, error) {
	created := make(map[string][]string, len(cfgs))
	for _, cfg := range cfgs {
		nsids, err := m.applyControllerLayout(ctx, cfg)
		if err != nil {
			return nil, err
		}
		created[cfg.Controller] = nsids
	}
	return created, nil
}

func (m *Manager) applyControllerLayout(ctx context.Context, cfg NVMeNamespaceConfig) ([]string, error) {
	controller := cfg.Controller

	supported, err := m.NVMeSupportsMultiNS(ctx, controller)
	if err != nil {
		return nil, fmt.Errorf("checking NVMe multi-namespace support: %w", err)
	}
	if !supported {
		return nil, fmt.Errorf("controller %s does not support multiple namespaces; cannot apply requested layout", controller)
	}

	totalBytes, err := m.nvmeControllerCapacity(ctx, controller)
	if err != nil {
		return nil, err
	}

	// Delete existing namespaces first so layout creation is deterministic.
	if err := m.deleteAllNamespaces(ctx, controller); err != nil {
		return nil, fmt.Errorf("reset namespaces on %s: %w", controller, err)
	}

	created := make([]string, 0, len(cfg.Namespaces))
	for _, ns := range cfg.Namespaces {
		bs := ns.BlockSize
		if bs == 0 {
			bs = 512
		}
		sizeBlocks := (totalBytes * uint64(ns.SizePct)) / (100 * uint64(bs))
		if sizeBlocks == 0 {
			return nil, fmt.Errorf("controller %s namespace %q: computed size is 0 blocks (device too small for sizePct=%d%% with blockSize=%d)", controller, ns.Label, ns.SizePct, bs)
		}

		nsid, err := m.CreateNVMeNamespace(ctx, controller, sizeBlocks, bs)
		if err != nil {
			return nil, err
		}
		if err := m.AttachNVMeNamespace(ctx, controller, nsid); err != nil {
			return nil, err
		}
		created = append(created, nsid)
		slog.Info("Created NVMe namespace", "controller", controller, "nsid", nsid, "label", ns.Label, "sizePct", ns.SizePct)
	}
	return created, nil
}

func (m *Manager) nvmeControllerCapacity(ctx context.Context, controller string) (uint64, error) {
	info, err := m.NVMeIdentifyController(ctx, controller)
	if err != nil {
		return 0, fmt.Errorf("identifying controller %s: %w", controller, err)
	}
	tnvmcap, ok := info["tnvmcap"]
	if !ok {
		return 0, fmt.Errorf("controller %s missing tnvmcap field", controller)
	}
	var totalBytes uint64
	if _, err := fmt.Sscanf(tnvmcap, "%d", &totalBytes); err != nil {
		return 0, fmt.Errorf("parsing tnvmcap %q: %w", tnvmcap, err)
	}
	return totalBytes, nil
}

// NVMeResetNamespaces deletes all namespaces and recreates a single default namespace.
// Used during deprovisioning to return disk to factory state.
func (m *Manager) NVMeResetNamespaces(ctx context.Context, controller string) error {
	slog.Info("Resetting NVMe namespaces to factory default", "controller", controller)

	if err := m.deleteAllNamespaces(ctx, controller); err != nil {
		return err
	}

	// Get total capacity for the default namespace.
	totalBytes, err := m.nvmeControllerCapacity(ctx, controller)
	if err != nil {
		return fmt.Errorf("reading controller capacity for reset: %w", err)
	}
	sizeBlocks := totalBytes / 512

	// Create and attach single namespace using full capacity.
	nsid, err := m.CreateNVMeNamespace(ctx, controller, sizeBlocks, 512)
	if err != nil {
		return fmt.Errorf("creating default namespace: %w", err)
	}
	if err := m.AttachNVMeNamespace(ctx, controller, nsid); err != nil {
		return fmt.Errorf("attaching default namespace: %w", err)
	}

	slog.Info("NVMe namespaces reset to factory default", "controller", controller)
	return nil
}

func (m *Manager) deleteAllNamespaces(ctx context.Context, controller string) error {
	nsids, err := m.NVMeListNamespaces(ctx, controller)
	if err != nil {
		return err
	}
	for _, nsid := range nsids {
		if _, err := m.cmd.Run(ctx, "nvme", "delete-ns", controller, "-n", nsid); err != nil {
			return fmt.Errorf("deleting namespace %s on %s: %w", nsid, controller, err)
		}
	}
	return nil
}
