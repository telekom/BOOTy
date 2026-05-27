package config

import (
	"fmt"
	"strings"
)

// Validate checks that enum-like config fields contain known values.
// Empty strings are accepted (will use defaults downstream).
func (c *Config) Validate() error {
	validators := []func() string{
		func() string {
			return validateEnum(c.Mode, "mode", "provision", "deprovision", "soft-deprovision", "soft", "hard", "standby", "dry-run", "check")
		},
		func() string {
			return validateEnum(c.Provision.Image.Mode, "provision.image.mode", "whole-disk", "partition")
		},
		func() string {
			return validateEnumLower(c.Network.Mode, "network.mode", "gobgp", "frr", "static", "dhcp")
		},
		func() string {
			return validateEnumLower(c.Provision.Image.ChecksumType, "provision.image.checksumType", "sha256", "sha512")
		},
		func() string {
			return validateEnumLower(c.Rescue.Mode, "rescue.mode", "reboot", "retry", "shell", "wait")
		},
		func() string {
			return validateEnumLower(c.Network.BGP.PeerMode, "network.bgp.peerMode", "unnumbered", "dual", "numbered")
		},
		func() string {
			return validateEnumLower(c.Network.BGP.UnderlayAF, "network.bgp.underlayAF", "ipv4", "ipv6", "dual-stack")
		},
		func() string {
			return validateEnumLower(c.Network.BGP.OverlayType, "network.bgp.overlayType", "evpn-vxlan", "l3vpn", "none")
		},
		func() string {
			return validateEnumLower(c.Provision.CloudInit.Datasource, "provision.cloudInit.datasource", "nocloud", "configdrive")
		},
		func() string {
			return validateEnumUpper(c.Transport.TokenAlgorithm, "transport.tokenAlgorithm", "RS256", "ES256")
		},
	}

	var errs []string
	for _, v := range validators {
		if msg := v(); msg != "" {
			errs = append(errs, msg)
		}
	}

	peerMode := strings.ToLower(strings.TrimSpace(c.Network.BGP.PeerMode))
	if (peerMode == "dual" || peerMode == "numbered") && strings.TrimSpace(c.Network.BGP.Neighbors) == "" {
		errs = append(errs, "network.bgp.neighbors required when network.bgp.peerMode is dual or numbered")
	}

	if err := validateRAIDConfig(c.Provision.Disk.RAID); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
	}

	// Normalize case-insensitive fields so downstream code can do plain equality checks.
	if c.Provision.Image.ChecksumType != "" {
		c.Provision.Image.ChecksumType = strings.ToLower(c.Provision.Image.ChecksumType)
	}
	if c.Network.Mode != "" {
		c.Network.Mode = strings.ToLower(c.Network.Mode)
	}
	if c.Network.BGP.PeerMode != "" {
		c.Network.BGP.PeerMode = strings.ToLower(c.Network.BGP.PeerMode)
	}
	if c.Network.BGP.UnderlayAF != "" {
		c.Network.BGP.UnderlayAF = strings.ToLower(c.Network.BGP.UnderlayAF)
	}
	if c.Network.BGP.OverlayType != "" {
		c.Network.BGP.OverlayType = strings.ToLower(c.Network.BGP.OverlayType)
	}
	if c.Provision.CloudInit.Datasource != "" {
		c.Provision.CloudInit.Datasource = strings.ToLower(c.Provision.CloudInit.Datasource)
	}
	if c.Rescue.Mode != "" {
		c.Rescue.Mode = strings.ToLower(c.Rescue.Mode)
	}
	if c.Transport.TokenAlgorithm != "" {
		c.Transport.TokenAlgorithm = strings.ToUpper(c.Transport.TokenAlgorithm)
	}
	return nil
}

// minDevicesForLevel returns the minimum number of member devices required
// for a given RAID level. Returns 2 for unknown levels as a safe default.
func minDevicesForLevel(level int) int {
	switch level {
	case 0, 1:
		return 2
	case 5:
		return 3
	case 6, 10:
		return 4
	default:
		return 2
	}
}

func validateRAIDConfig(raids []RAIDConfig) error {
	var errs []string
	for i, r := range raids {
		if strings.TrimSpace(r.Name) == "" {
			errs = append(errs, fmt.Sprintf("provision.disk.raid[%d]: name is required", i))
		} else if strings.HasPrefix(r.Name, "/dev/") {
			errs = append(errs, fmt.Sprintf("provision.disk.raid[%d]: name must not include /dev/ prefix (got %q); use e.g. \"md0\"", i, r.Name))
		}
		if r.Level != 0 && r.Level != 1 && r.Level != 5 && r.Level != 6 && r.Level != 10 {
			errs = append(errs, fmt.Sprintf("provision.disk.raid[%d]: invalid level %d (valid: 0, 1, 5, 6, 10)", i, r.Level))
		}
		minDevices := minDevicesForLevel(r.Level)
		if len(r.Devices) < minDevices {
			errs = append(errs, fmt.Sprintf("provision.disk.raid[%d]: level %d requires at least %d devices, got %d", i, r.Level, minDevices, len(r.Devices)))
		}
		for j, dev := range r.Devices {
			if strings.TrimSpace(dev) == "" {
				errs = append(errs, fmt.Sprintf("provision.disk.raid[%d].devices[%d]: device path must not be empty", i, j))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func validateEnum(value, name string, allowed ...string) string {
	if value == "" {
		return ""
	}
	for _, a := range allowed {
		if value == a {
			return ""
		}
	}
	return fmt.Sprintf("invalid %s %q", name, value)
}

func validateEnumLower(value, name string, allowed ...string) string {
	if value == "" {
		return ""
	}
	return validateEnum(strings.ToLower(value), name, allowed...)
}

func validateEnumUpper(value, name string, allowed ...string) string {
	if value == "" {
		return ""
	}
	return validateEnum(strings.ToUpper(value), name, allowed...)
}
