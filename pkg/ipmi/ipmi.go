//go:build linux

package ipmi

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/telekom/BOOTy/pkg/executil"
)

// Manager handles local IPMI operations via ipmitool.
type Manager struct {
	deviceNum string
	runner    executil.Commander
	log       *slog.Logger
}

// New creates an IPMI manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		deviceNum: "0",
		runner:    &executil.ExecCommander{},
		log:       log,
	}
}

// BMCNetConfig holds BMC network configuration.
type BMCNetConfig struct {
	IPAddress   string `json:"ipAddress"`
	Netmask     string `json:"netmask"`
	Gateway     string `json:"gateway"`
	MACAddress  string `json:"macAddress"`
	DHCP        bool   `json:"dhcp"`
	VLANEnabled bool   `json:"vlanEnabled"`
	VLANID      int    `json:"vlanId"`
}

// SensorReading represents an IPMI sensor value.
type SensorReading struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Status    string  `json:"status"`
	LowerCrit float64 `json:"lowerCrit,omitempty"`
	UpperCrit float64 `json:"upperCrit,omitempty"`
}

// BootDevice represents an IPMI boot target.
type BootDevice string

const (
	// BootPXE boots from network.
	BootPXE BootDevice = "pxe"
	// BootDisk boots from local disk.
	BootDisk BootDevice = "disk"
	// BootCDROM boots from CD/DVD.
	BootCDROM BootDevice = "cdrom"
	// BootBIOS enters BIOS setup.
	BootBIOS BootDevice = "bios"
)

// ChassisStatus holds chassis power state information.
type ChassisStatus struct {
	PowerOn    bool   `json:"powerOn"`
	PowerFault bool   `json:"powerFault"`
	Intrusion  bool   `json:"intrusion"`
	LastEvent  string `json:"lastEvent"`
}

// GetBMCNetwork reads the BMC network configuration.
func (m *Manager) GetBMCNetwork(ctx context.Context, channel int) (*BMCNetConfig, error) {
	out, err := m.output(ctx, "lan", "print", fmt.Sprintf("%d", channel))
	if err != nil {
		return nil, fmt.Errorf("get BMC network: %w", err)
	}

	fields := parseLanPrint(out)
	vlanEnabled, vlanID := parseVLAN(fields)
	return &BMCNetConfig{
		IPAddress:   fields["IP Address"],
		Netmask:     fields["Subnet Mask"],
		Gateway:     fields["Default Gateway IP"],
		MACAddress:  fields["MAC Address"],
		DHCP:        strings.Contains(strings.ToLower(fields["IP Address Source"]), "dhcp"),
		VLANEnabled: vlanEnabled,
		VLANID:      vlanID,
	}, nil
}

// SetNextBoot sets the boot device for the next boot only.
func (m *Manager) SetNextBoot(ctx context.Context, device BootDevice) error {
	if err := ValidateBootDevice(string(device)); err != nil {
		return err
	}
	return m.run(ctx, "chassis", "bootdev", string(device), "options=efiboot")
}

// ChassisControl sends a chassis control command (power on/off/cycle/reset).
func (m *Manager) ChassisControl(ctx context.Context, action string) error {
	switch action {
	case "on", "off", "cycle", "reset", "soft":
		return m.run(ctx, "chassis", "power", action)
	default:
		return fmt.Errorf("invalid chassis action: %q", action)
	}
}

// DevicePath returns the IPMI device path.
func (m *Manager) DevicePath() string {
	return "/dev/ipmi" + m.deviceNum
}

// parseLanPrint parses "ipmitool lan print" output into a key-value map.
func parseLanPrint(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		result[key] = val
	}
	return result
}

func parseVLAN(fields map[string]string) (enabled bool, vlanID int) {
	raw := strings.TrimSpace(fields["802.1q VLAN ID"])
	if raw == "" {
		return false, 0
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "disabled") || lower == "off" || lower == "0" {
		return false, 0
	}
	if strings.HasPrefix(lower, "0x") {
		if value, err := strconv.ParseUint(raw[2:], 16, 16); err == nil {
			return true, int(value)
		}
	}
	if value, err := strconv.ParseUint(raw, 10, 16); err == nil {
		if value == 0 {
			return false, 0
		}
		return true, int(value)
	}
	// Unknown but present value is treated as enabled with unspecified VLAN ID.
	return true, 0
}

func (m *Manager) run(ctx context.Context, args ...string) error {
	_, err := m.execIPMITool(ctx, args...)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) output(ctx context.Context, args ...string) (string, error) {
	return m.execIPMITool(ctx, args...)
}

func (m *Manager) execIPMITool(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{"-I", "open", "-d", m.deviceNum}, args...)
	m.log.Debug("executing ipmitool", "args", redactIPMIArgs(fullArgs))
	out, err := m.runner.Run(ctx, "ipmitool", fullArgs...)
	if err != nil {
		return "", fmt.Errorf("ipmitool %s: %s: %w", strings.Join(redactIPMIArgs(args), " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

var sensitiveIPMIFlags = map[string]bool{
	"-P": true, "--password": true,
	"-U": true, "--username": true,
	"-H": true, "--hostname": true,
	"-K": true, "--kg-key": true,
}

func redactIPMIArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if sensitiveIPMIFlags[args[i]] && i+1 < len(args) {
			if out == nil {
				out = make([]string, len(args))
				copy(out, args)
			}
			i++ // skip the value so it is not itself treated as a flag
			out[i] = "[REDACTED]"
		}
	}
	if out == nil {
		return args
	}
	return out
}

// ParseBMCMAC parses a MAC address string into net.HardwareAddr.
func ParseBMCMAC(mac string) (net.HardwareAddr, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return nil, fmt.Errorf("parse BMC MAC %q: %w", mac, err)
	}
	return hw, nil
}

// ValidateBootDevice checks if a boot device string is valid.
func ValidateBootDevice(device string) error {
	switch BootDevice(strings.TrimSpace(device)) {
	case BootPXE, BootDisk, BootCDROM, BootBIOS:
		return nil
	default:
		return fmt.Errorf("invalid boot device: %q", device)
	}
}
