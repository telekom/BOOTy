package ipmi

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
)

// Manager handles local IPMI operations via ipmitool.
type Manager struct {
	device string
	log    *slog.Logger
}

// New creates an IPMI manager.
func New(log *slog.Logger) *Manager {
	return &Manager{
		device: "/dev/ipmi0",
		log:    log,
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
	BootBIOS BootDevice = "bios-setup"
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
	ip, err := m.lanGet(ctx, channel, "IP Address")
	if err != nil {
		return nil, fmt.Errorf("get BMC IP: %w", err)
	}
	mask, err := m.lanGet(ctx, channel, "Subnet Mask")
	if err != nil {
		return nil, fmt.Errorf("get BMC netmask: %w", err)
	}
	gw, err := m.lanGet(ctx, channel, "Default Gateway IP")
	if err != nil {
		return nil, fmt.Errorf("get BMC gateway: %w", err)
	}
	mac, err := m.lanGet(ctx, channel, "MAC Address")
	if err != nil {
		return nil, fmt.Errorf("get BMC MAC: %w", err)
	}
	src, err := m.lanGet(ctx, channel, "IP Address Source")
	if err != nil {
		return nil, fmt.Errorf("get BMC IP source: %w", err)
	}

	return &BMCNetConfig{
		IPAddress:  strings.TrimSpace(ip),
		Netmask:    strings.TrimSpace(mask),
		Gateway:    strings.TrimSpace(gw),
		MACAddress: strings.TrimSpace(mac),
		DHCP:       strings.Contains(strings.ToLower(src), "dhcp"),
	}, nil
}

// SetNextBoot sets the boot device for the next boot only.
func (m *Manager) SetNextBoot(ctx context.Context, device BootDevice) error {
	var devStr string
	switch device {
	case BootPXE:
		devStr = "pxe"
	case BootDisk:
		devStr = "disk"
	case BootCDROM:
		devStr = "cdrom"
	case BootBIOS:
		devStr = "bios"
	default:
		return fmt.Errorf("unknown boot device: %s", device)
	}

	return m.run(ctx, "chassis", "bootdev", devStr, "options=efiboot")
}

// ChassisControl sends a chassis control command (power on/off/cycle/reset).
func (m *Manager) ChassisControl(ctx context.Context, action string) error {
	validActions := map[string]bool{
		"on": true, "off": true, "cycle": true,
		"reset": true, "soft": true,
	}
	if !validActions[action] {
		return fmt.Errorf("invalid chassis action: %s", action)
	}
	return m.run(ctx, "chassis", "power", action)
}

// DevicePath returns the IPMI device path.
func (m *Manager) DevicePath() string {
	return m.device
}

func (m *Manager) lanGet(ctx context.Context, channel int, param string) (string, error) {
	out, err := m.output(ctx, "lan", "print", fmt.Sprintf("%d", channel))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, param) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}
	return "", fmt.Errorf("parameter %q not found in LAN config", param)
}

func (m *Manager) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ipmitool", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ipmitool %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	return nil
}

func (m *Manager) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "ipmitool", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ipmitool %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	return string(out), nil
}

// ParseBMCMAC parses a MAC address string into net.HardwareAddr.
func ParseBMCMAC(mac string) (net.HardwareAddr, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, fmt.Errorf("parse BMC MAC %q: %w", mac, err)
	}
	return hw, nil
}

// ValidateBootDevice checks if a boot device string is valid.
func ValidateBootDevice(device string) (BootDevice, error) {
	switch BootDevice(device) {
	case BootPXE, BootDisk, BootCDROM, BootBIOS:
		return BootDevice(device), nil
	default:
		return "", fmt.Errorf("invalid boot device: %q", device)
	}
}
