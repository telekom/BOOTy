package config

import (
	"fmt"
	"strings"
)

// Validate checks that all enum-like config fields contain known values and
// that cross-field constraints are satisfied. Empty strings are accepted
// everywhere — unset fields fall back to documented defaults at runtime.
//
// Fields validated:
//   - Mode: "provision", "deprovision", "soft-deprovision", "soft", "hard",
//     "standby", "dry-run", "check"
//   - Provision.Image.Mode: "whole-disk", "partition", "ab"
//   - Provision.Image.ChecksumType: "sha256", "sha512"
//   - Provision.CloudInit.Datasource: "nocloud", "configdrive"
//   - Provision.Disk.RAID[*]: valid level, unique non-empty name without /dev/ prefix,
//     minimum device count per RAID level
//   - Network.Mode: "gobgp", "frr", "static", "dhcp"
//   - Network.BGP.PeerMode: "unnumbered", "dual", "numbered"
//   - Network.BGP.UnderlayAF: "ipv4", "ipv6", "dual-stack"
//   - Network.BGP.OverlayType: "evpn-vxlan", "l3vpn", "none"
//   - Rescue.Mode: "reboot", "retry", "shell", "wait"
//   - Transport.TokenAlgorithm: "RS256", "ES256"
//   - Cross-field: Network.BGP.Neighbors required when PeerMode is "dual" or "numbered"
//
// Validate also calls normalize(), which lowercases/uppercases case-insensitive
// enum fields in place so that downstream code can use plain equality comparisons.
// After a successful Validate call, string enums are in their canonical form.
//
// All validation errors are collected and returned as a single error. Returns
// nil when the config is valid.
func (c *Config) Validate() error {
	validators := []func() string{
		func() string {
			return validateEnum(c.Mode, "mode", "provision", "deprovision", "soft-deprovision", "soft", "hard", "standby", "dry-run", "check")
		},
		func() string {
			return validateEnum(c.Provision.Image.Mode, "provision.image.mode", ImageModeWholeDisk, ImageModePartition, ImageModeAB)
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
	if err := validateSysextConfig(&c.Provision.Sysext); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateABConfig(c.Provision.Image.Mode, &c.Provision.AB); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
	}

	c.normalize()
	return nil
}

// normalize lowercases or uppercases case-insensitive enum fields so downstream
// code can use plain equality comparisons without calling ToLower/ToUpper.
func (c *Config) normalize() {
	lowerFields := []*string{
		&c.Provision.Image.ChecksumType,
		&c.Network.Mode,
		&c.Network.BGP.PeerMode,
		&c.Network.BGP.UnderlayAF,
		&c.Network.BGP.OverlayType,
		&c.Provision.CloudInit.Datasource,
		&c.Provision.Sysext.DefaultMode,
		&c.Provision.AB.Scheme,
		&c.Provision.AB.ActiveSlot,
		&c.Provision.AB.TargetSlot,
		&c.Rescue.Mode,
	}
	for _, f := range lowerFields {
		if *f != "" {
			*f = strings.ToLower(*f)
		}
	}
	if c.Transport.TokenAlgorithm != "" {
		c.Transport.TokenAlgorithm = strings.ToUpper(c.Transport.TokenAlgorithm)
	}
}

func validateABConfig(imageMode string, cfg *ABConfig) error {
	errs := make([]string, 0, 5)
	mode := strings.ToLower(strings.TrimSpace(imageMode))
	abMode := mode == ImageModeAB

	errs = append(errs, validateABEnums(cfg)...)
	errs = append(errs, validateABSizeFields(cfg)...)
	errs = append(errs, validateABModeConstraints(abMode, cfg)...)

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func validateABEnums(cfg *ABConfig) []string {
	var errs []string
	scheme := normalizeABScheme(cfg.Scheme)
	if scheme != "" && scheme != ABSchemeDualRoot {
		errs = append(errs, fmt.Sprintf("invalid provision.ab.scheme %q", cfg.Scheme))
	}

	activeSlot := normalizeABSlot(cfg.ActiveSlot)
	if activeSlot != "" && activeSlot != ABSlotA && activeSlot != ABSlotB {
		errs = append(errs, fmt.Sprintf("invalid provision.ab.activeSlot %q", cfg.ActiveSlot))
	}

	targetSlot := strings.ToLower(strings.TrimSpace(cfg.TargetSlot))
	if targetSlot != "" && targetSlot != ABSlotA && targetSlot != ABSlotB && targetSlot != ABTargetInactive {
		errs = append(errs, fmt.Sprintf("invalid provision.ab.targetSlot %q", cfg.TargetSlot))
	}
	return errs
}

func validateABSizeFields(cfg *ABConfig) []string {
	var errs []string
	if cfg.BootSizeMB < 0 {
		errs = append(errs, "provision.ab.bootSizeMB must be non-negative")
	}
	if cfg.RootSizeMB < 0 {
		errs = append(errs, "provision.ab.rootSizeMB must be non-negative")
	}
	if cfg.StateSizeMB < 0 {
		errs = append(errs, "provision.ab.stateSizeMB must be non-negative")
	}
	return errs
}

func validateABModeConstraints(abMode bool, cfg *ABConfig) []string {
	var errs []string
	if cfg.PreserveExisting && !abMode {
		errs = append(errs, "provision.ab.preserveExisting requires provision.image.mode=ab")
	}
	if !abMode {
		return errs
	}

	withDefaults := cfg.WithDefaults()
	if withDefaults.RootSizeMB <= 0 {
		errs = append(errs, "provision.ab.rootSizeMB must be positive in ab image mode")
	}
	if _, err := withDefaults.ResolvedTargetSlot(); err != nil {
		errs = append(errs, fmt.Sprintf("provision.ab: %v", err))
	}
	return errs
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

func validateSysextConfig(cfg *SysextConfig) error {
	var errs []string
	defaultMode := strings.ToLower(strings.TrimSpace(cfg.DefaultMode))
	if defaultMode != "" && defaultMode != "preload" && defaultMode != "active" {
		errs = append(errs, fmt.Sprintf("invalid provision.sysext.defaultMode %q", cfg.DefaultMode))
	}
	if err := validateSysextTargetDir(cfg.CatalogDir); err != nil {
		errs = append(errs, fmt.Sprintf("provision.sysext.catalogDir: %v", err))
	}
	if err := validateSysextTargetDir(cfg.ActiveDir); err != nil {
		errs = append(errs, fmt.Sprintf("provision.sysext.activeDir: %v", err))
	}
	for i := range cfg.Layers {
		errs = append(errs, validateSysextLayerConfig(cfg.Enabled, i, &cfg.Layers[i])...)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func validateSysextLayerConfig(enabled bool, index int, layer *SysextLayerConfig) []string {
	prefix := fmt.Sprintf("provision.sysext.layers[%d]", index)
	var errs []string
	if strings.TrimSpace(layer.Name) == "" {
		errs = append(errs, prefix+": name is required")
	}
	if enabled && strings.TrimSpace(layer.Source) == "" {
		errs = append(errs, prefix+": source is required when provision.sysext.enabled is true")
	}
	mode := strings.ToLower(strings.TrimSpace(layer.Mode))
	if mode != "" && mode != "preload" && mode != "active" {
		errs = append(errs, fmt.Sprintf("invalid %s.mode %q", prefix, layer.Mode))
	}
	if err := validateSysextFileName(layer.FileName); err != nil {
		errs = append(errs, fmt.Sprintf("%s.fileName: %v", prefix, err))
	}
	if err := validateSysextSHA256(layer.SHA256); err != nil {
		errs = append(errs, fmt.Sprintf("%s.sha256: %v", prefix, err))
	}
	return errs
}

func validateSysextTargetDir(value string) error {
	dir := strings.TrimSpace(value)
	if dir == "" {
		return nil
	}
	if !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("must be an absolute path")
	}
	if dir == "/" {
		return fmt.Errorf("must not be root")
	}
	for _, part := range strings.Split(dir, "/") {
		if part == ".." {
			return fmt.Errorf("must not contain parent-directory segments")
		}
	}
	return nil
}

func validateSysextFileName(name string) error {
	if name == "" {
		return nil
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("must be a plain file name")
	}
	return nil
}

func validateSysextSHA256(value string) error {
	value = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(value)), "sha256:")
	if value == "" {
		return nil
	}
	if len(value) != 64 {
		return fmt.Errorf("must be 64 hex characters")
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("must be lowercase or uppercase hex")
		}
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
