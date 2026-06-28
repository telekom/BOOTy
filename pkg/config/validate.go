package config

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	imageutil "github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/network"
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
//   - Network.BGP.UnderlayAF: "ipv4"
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
			return validateEnumLower(c.Network.BGP.UnderlayAF, "network.bgp.underlayAF", "ipv4")
		},
		func() string {
			return validateEnumLower(c.Network.BGP.OverlayType, "network.bgp.overlayType", "evpn-vxlan", "l3vpn", "none")
		},
		func() string {
			return validateEnumLower(c.Provision.CloudInit.Datasource, "provision.cloudInit.datasource", "nocloud", "configdrive")
		},
		func() string {
			return validateEnumLower(c.OSFamily, "osFamily", "ubuntu", "rhel", "flatcar")
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
	errs = append(errs, c.validateBGP()...)
	errs = append(errs, c.validatePersistence()...)

	if err := validateRAIDConfig(c.Provision.Disk.RAID); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateDiskRootSelectors(&c.Provision.Disk); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateSysextConfig(&c.Provision.Sysext); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateABConfig(c.Provision.Image.Mode, c.Provision.DisableKexec, &c.Provision.AB); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateSecureBootConfig(c.Provision.Image.Mode, &c.Provision.SecureBoot, &c.Provision.AB); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
	}

	c.normalize()
	return nil
}

func (c *Config) validateBGP() []string {
	bfdTransmit := c.Network.BGP.BFDTransmitMS
	bfdReceive := c.Network.BGP.BFDReceiveMS

	var errs []string
	if (bfdTransmit == 0) != (bfdReceive == 0) {
		errs = append(errs, "network.bgp.bfdTransmitMS and network.bgp.bfdReceiveMS must be set together")
	}
	if strings.EqualFold(strings.TrimSpace(c.Network.Mode), "gobgp") && (bfdTransmit > 0 || bfdReceive > 0) {
		errs = append(errs, "network.mode=gobgp does not support BFD; use network.mode=frr or BGP keepalive/hold timers")
	}
	return errs
}

func (c *Config) validatePersistence() []string {
	if !c.PersistNetwork {
		return nil
	}
	staticIP := strings.TrimSpace(c.Network.Static.IP)
	staticIface := strings.TrimSpace(c.Network.Static.Iface)
	bondInterfaces := strings.TrimSpace(c.Network.Bond.Interfaces)
	vlanConfig := strings.TrimSpace(c.Network.VLAN.Config)
	osFamily := strings.ToLower(strings.TrimSpace(c.OSFamily))

	var errs []string
	if osFamily == "" {
		errs = append(errs, "osFamily required when persistNetwork is true")
	}
	if persistentNetworkOSFamilyBlocked(osFamily) {
		errs = append(errs, fmt.Sprintf("osFamily %q with persistNetwork=true is blocked: target network persistence is blocked until native bootloader, BLS, and SELinux first-boot support are implemented", osFamily))
		return errs
	}
	if staticIP != "" && bondInterfaces == "" && staticIface == "" {
		errs = append(errs, "network.static.iface required when persistNetwork is true with network.static.ip and no network.bond.interfaces")
	}
	if staticIface == "" && bondInterfaces == "" && vlanConfig == "" {
		errs = append(errs, "network.static.iface, network.bond.interfaces, or network.vlan.config required when persistNetwork is true")
	}
	if bondInterfaces != "" && staticIP == "" && vlanConfig == "" {
		errs = append(errs, "network.bond.interfaces requires network.static.ip or network.vlan.config when persistNetwork is true")
	}
	errs = append(errs, validatePersistenceVLANConfig(vlanConfig)...)
	return errs
}

func persistentNetworkOSFamilyBlocked(osFamily string) bool {
	return osFamily == "rhel"
}

func validatePersistenceVLANConfig(vlanConfig string) []string {
	if vlanConfig == "" {
		return nil
	}
	vlans, err := network.ParseVLANs(vlanConfig)
	if err != nil {
		return []string{fmt.Sprintf("network.vlan.config invalid: %s", err)}
	}

	var errs []string
	for _, vlan := range vlans {
		if strings.TrimSpace(vlan.Gateway) != "" {
			errs = append(errs, fmt.Sprintf("network.vlan.config vlan %d on %s includes gateway, which target network persistence cannot render", vlan.ID, vlan.Parent))
		}
	}
	return errs
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
		&c.OSFamily,
		&c.Provision.Sysext.DefaultMode,
		&c.Provision.AB.Scheme,
		&c.Provision.AB.ActiveSlot,
		&c.Provision.AB.TargetSlot,
		&c.Rescue.Mode,
	}
	for _, f := range lowerFields {
		if *f != "" {
			*f = strings.ToLower(strings.TrimSpace(*f))
		}
	}
	if c.Transport.TokenAlgorithm != "" {
		c.Transport.TokenAlgorithm = strings.ToUpper(strings.TrimSpace(c.Transport.TokenAlgorithm))
	}
	c.Provision.Disk.RootPartitionLabel = strings.TrimSpace(c.Provision.Disk.RootPartitionLabel)
	c.Provision.AB.DataPartitions = normalizeABDataPartitions(c.Provision.AB.DataPartitions)
}

func validateDiskRootSelectors(cfg *DiskConfig) error {
	if cfg.RootPartitionNumber < 0 {
		return fmt.Errorf("provision.disk.rootPartitionNumber must be non-negative")
	}
	if strings.TrimSpace(cfg.RootPartitionLabel) != "" && cfg.RootPartitionNumber != 0 {
		return fmt.Errorf("provision.disk.rootPartitionLabel and provision.disk.rootPartitionNumber are mutually exclusive")
	}
	return nil
}

func validateABConfig(imageMode string, disableKexec bool, cfg *ABConfig) error {
	errs := make([]string, 0, 5)
	mode := strings.ToLower(strings.TrimSpace(imageMode))
	abMode := mode == ImageModeAB

	errs = append(errs, validateABEnums(cfg)...)
	errs = append(errs, validateABSizeFields(cfg)...)
	errs = append(errs, validateABModeConstraints(abMode, disableKexec, cfg)...)

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func validateSecureBootConfig(imageMode string, secureBoot *SecureBootConfig, ab *ABConfig) error {
	if secureBoot == nil || ab == nil || !secureBoot.ReEnable {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(imageMode), ImageModeAB) && ab.PreserveExisting {
		return fmt.Errorf("provision.secureBoot.reEnable requires a hard reboot, but provision.ab.preserveExisting uses kexec")
	}
	return nil
}

func validateABEnums(cfg *ABConfig) []string {
	var errs []string
	scheme := normalizeABScheme(cfg.Scheme)
	if scheme != "" && scheme != ABSchemeDualRoot && scheme != ABSchemeSystemAB {
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
	if cfg.SourceRootPartition < 0 {
		errs = append(errs, "provision.ab.sourceRootPartition must be non-negative")
	}
	for i, part := range cfg.DataPartitions {
		if part.SizeMB < 0 {
			errs = append(errs, fmt.Sprintf("provision.ab.dataPartitions[%d].sizeMB must be non-negative", i))
		}
	}
	return errs
}

func validateABModeConstraints(abMode, disableKexec bool, cfg *ABConfig) []string {
	var errs []string
	errs = append(errs, validateABDataPartitionMode(abMode, cfg)...)
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
	if layout, err := withDefaults.PartitionLayout("/dev/sda"); err != nil {
		errs = append(errs, fmt.Sprintf("provision.ab: %v", err))
	} else {
		errs = append(errs, validateABPartitionLayoutContract(layout)...)
	}
	errs = append(errs, validateABPreserveConstraints(disableKexec, cfg, &withDefaults)...)
	errs = append(errs, validateABSourceRootSelectors(cfg)...)
	if _, err := withDefaults.ResolvedTargetSlot(); err != nil {
		errs = append(errs, fmt.Sprintf("provision.ab: %v", err))
	}
	return errs
}

func validateABDataPartitionMode(abMode bool, cfg *ABConfig) []string {
	if len(cfg.DataPartitions) == 0 {
		return nil
	}

	scheme := normalizeABScheme(cfg.Scheme)
	if !abMode || scheme != ABSchemeSystemAB {
		return []string{"provision.ab.dataPartitions requires provision.image.mode=ab and provision.ab.scheme=system-ab"}
	}

	var errs []string
	for i, part := range cfg.DataPartitions {
		if strings.EqualFold(strings.TrimSpace(part.Filesystem), "vfat") {
			errs = append(errs, fmt.Sprintf("provision.ab.dataPartitions[%d].filesystem must not be vfat for system-ab shared data", i))
		}
		if strings.EqualFold(strings.TrimSpace(part.Filesystem), "swap") {
			errs = append(errs, fmt.Sprintf("provision.ab.dataPartitions[%d].filesystem must not be swap for system-ab shared data", i))
		}
		mountpoint := strings.TrimSpace(part.Mountpoint)
		if mountpoint == "" {
			errs = append(errs, fmt.Sprintf("provision.ab.dataPartitions[%d].mountpoint is required for system-ab shared data", i))
			continue
		}
		if !strings.HasPrefix(mountpoint, "/") {
			errs = append(errs, fmt.Sprintf("provision.ab.dataPartitions[%d].mountpoint %q must be an absolute path", i, mountpoint))
		}
		normalizedMountpoint := path.Clean(mountpoint)
		switch normalizedMountpoint {
		case "/", "/boot/efi":
			errs = append(errs, fmt.Sprintf("provision.ab.dataPartitions[%d].mountpoint must not be %q", i, normalizedMountpoint))
		}
	}
	return errs
}

func validateABPreserveConstraints(disableKexec bool, cfg, withDefaults *ABConfig) []string {
	if !cfg.PreserveExisting {
		return nil
	}
	var errs []string
	if disableKexec {
		errs = append(errs, "provision.disableKexec must be false when provision.ab.preserveExisting is true")
	}
	if cfg.PreserveExisting && withDefaults.ActiveSlot == "" {
		errs = append(errs, "provision.ab.activeSlot is required when preserveExisting is true")
	}
	if cfg.PreserveExisting && withDefaults.ActiveSlot != "" &&
		(withDefaults.TargetSlot == ABSlotA || withDefaults.TargetSlot == ABSlotB) &&
		withDefaults.ActiveSlot == withDefaults.TargetSlot {
		errs = append(errs, "provision.ab.targetSlot must not equal provision.ab.activeSlot when preserveExisting is true")
	}
	return errs
}

func validateABPartitionLayoutContract(layout *PartitionLayout) []string {
	var errs []string
	if err := validatePartitions(layout.Partitions); err != nil {
		errs = append(errs, fmt.Sprintf("provision.ab partition layout: %v", err))
	}
	if err := validateUniqueMountpoints(layout.Partitions, layout.LVM); err != nil {
		errs = append(errs, fmt.Sprintf("provision.ab partition layout: %v", err))
	}
	if err := validateRootPresence(layout.Partitions, layout.LVM); err != nil {
		errs = append(errs, fmt.Sprintf("provision.ab partition layout: %v", err))
	}
	return errs
}

func validateABSourceRootSelectors(cfg *ABConfig) []string {
	if cfg.SourceRootLabel != "" && cfg.SourceRootPartition != 0 {
		return []string{"provision.ab.sourceRootLabel and provision.ab.sourceRootPartition are mutually exclusive"}
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
		errs = append(errs, validateSysextLayerConfig(cfg.Enabled, cfg.AllowInsecureHTTP, i, &cfg.Layers[i])...)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func validateSysextLayerConfig(enabled, allowInsecureHTTP bool, index int, layer *SysextLayerConfig) []string {
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
	errs = append(errs, validateSysextSourceIntegrity(enabled, allowInsecureHTTP, prefix, layer)...)
	return errs
}

func validateSysextSourceIntegrity(enabled, allowInsecureHTTP bool, prefix string, layer *SysextLayerConfig) []string {
	source := strings.TrimSpace(layer.Source)
	if source == "" {
		return nil
	}
	var errs []string
	u, err := url.Parse(source)
	if err != nil {
		if looksLikeHTTPSource(source) {
			errs = append(errs, fmt.Sprintf("%s.source: invalid HTTP(S) sysext source %s: %s", prefix, imageutil.RedactURL(source), imageutil.RedactSourceError(err, source)))
		} else if looksLikeURLSource(source) {
			errs = append(errs, fmt.Sprintf("%s.source: invalid sysext source %s: %s", prefix, imageutil.RedactURL(source), imageutil.RedactSourceError(err, source)))
		}
	} else {
		switch strings.ToLower(u.Scheme) {
		case "":
			// No scheme means a local provisioner file path, which keeps the
			// existing offline provisioning behavior.
		case "https", "oci":
		case "http":
			if !allowInsecureHTTP {
				errs = append(errs, fmt.Sprintf("%s.source: plain HTTP sysext sources require provision.sysext.allowInsecureHTTP=true; use HTTPS, OCI, or a local provisioner file for production", prefix))
			}
		default:
			errs = append(errs, fmt.Sprintf("%s.source: unsupported sysext source scheme %q; use HTTPS, OCI, plain HTTP with provision.sysext.allowInsecureHTTP=true, or a local provisioner file", prefix, u.Scheme))
		}
	}
	if enabled && strings.TrimSpace(layer.SHA256) == "" && !isOCIDigestSource(source) {
		errs = append(errs, fmt.Sprintf("%s.sha256: required unless source is an OCI digest reference", prefix))
	}
	return errs
}

func looksLikeHTTPSource(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func looksLikeURLSource(source string) bool {
	return strings.Contains(strings.TrimSpace(source), "://")
}

func isOCIDigestSource(source string) bool {
	source = strings.TrimSpace(source)
	if !imageutil.IsOCIReference(source) {
		return false
	}
	_, digest, ok := strings.Cut(imageutil.TrimOCIScheme(source), "@sha256:")
	return ok && strings.TrimSpace(digest) != "" && validateSysextSHA256(digest) == nil
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
	digest := strings.TrimSpace(strings.ToLower(value))
	if digest == "" {
		return nil
	}
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) != 64 {
		return fmt.Errorf("must be 64 hex characters")
	}
	for _, r := range digest {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("must be hex characters")
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
	return validateEnum(strings.ToLower(strings.TrimSpace(value)), name, allowed...)
}

func validateEnumUpper(value, name string, allowed ...string) string {
	if value == "" {
		return ""
	}
	return validateEnum(strings.ToUpper(strings.TrimSpace(value)), name, allowed...)
}
