// Package persist manages network configuration persistence to target OS.
package persist

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// OSFamily represents the target OS family.
type OSFamily string

const (
	// Ubuntu uses netplan YAML.
	Ubuntu OSFamily = "ubuntu"
	// RHEL uses NetworkManager keyfiles.
	RHEL OSFamily = "rhel"
	// Flatcar uses systemd-networkd units.
	Flatcar OSFamily = "flatcar"
)

// ParseOSFamily parses an OS family string.
func ParseOSFamily(s string) (OSFamily, error) {
	switch OSFamily(strings.ToLower(s)) {
	case Ubuntu:
		return Ubuntu, nil
	case RHEL:
		return RHEL, nil
	case Flatcar:
		return Flatcar, nil
	default:
		return "", fmt.Errorf("unsupported OS family %q", s)
	}
}

// ConfigPath returns the relative network config directory for the OS family.
func (f OSFamily) ConfigPath() string {
	switch f {
	case Ubuntu:
		return "etc/netplan"
	case RHEL:
		return "etc/NetworkManager/system-connections"
	case Flatcar:
		return "etc/systemd/network"
	default:
		return ""
	}
}

// InterfaceConfig describes a network interface.
type InterfaceConfig struct {
	Name    string `json:"name"`
	MAC     string `json:"mac,omitempty"`
	DHCP    bool   `json:"dhcp,omitempty"`
	Address string `json:"address,omitempty"` // CIDR notation.
	Gateway string `json:"gateway,omitempty"`
	MTU     int    `json:"mtu,omitempty"`
}

// BondConfig describes a network bond.
type BondConfig struct {
	Name       string   `json:"name"`
	Members    []string `json:"members"`
	Mode       string   `json:"mode"`
	Address    string   `json:"address,omitempty"`
	Gateway    string   `json:"gateway,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	LACPRate   string   `json:"lacpRate,omitempty"`
	HashPolicy string   `json:"hashPolicy,omitempty"`
}

// VLANConfig describes a VLAN interface.
type VLANConfig struct {
	Name    string `json:"name"`
	Parent  string `json:"parent"`
	ID      int    `json:"id"`
	DHCP    bool   `json:"dhcp,omitempty"`
	Address string `json:"address,omitempty"`
}

// DNSConfig holds DNS settings.
type DNSConfig struct {
	Servers []string `json:"servers,omitempty"`
	Search  []string `json:"search,omitempty"`
}

// RouteConfig describes a static route.
type RouteConfig struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Metric      int    `json:"metric,omitempty"`
}

// NetworkConfig holds the complete network configuration to persist.
type NetworkConfig struct {
	Interfaces []InterfaceConfig `json:"interfaces,omitempty"`
	Bonds      []BondConfig      `json:"bonds,omitempty"`
	VLANs      []VLANConfig      `json:"vlans,omitempty"`
	DNS        DNSConfig         `json:"dns,omitempty"`
	Routes     []RouteConfig     `json:"routes,omitempty"`
}

// validName matches safe interface and bond names (alphanumeric, dots, hyphens, underscores).
var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Validate checks the network configuration.
func (c *NetworkConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("network config is nil")
	}
	if err := c.validateInterfaces(); err != nil {
		return err
	}
	if err := c.validateBonds(); err != nil {
		return err
	}
	if err := c.validateVLANs(); err != nil {
		return err
	}
	if err := c.validateLogicalReferences(); err != nil {
		return err
	}
	if err := validateDNSConfig(&c.DNS); err != nil {
		return err
	}
	if err := c.validateRoutes(); err != nil {
		return err
	}
	if (len(c.DNS.Servers) > 0 || len(c.DNS.Search) > 0 || len(c.Routes) > 0) &&
		len(c.Interfaces) == 0 && len(c.Bonds) == 0 && len(c.VLANs) == 0 {
		return fmt.Errorf("dns/routes require at least one interface, bond, or vlan")
	}
	return nil
}

func (c *NetworkConfig) validateLogicalReferences() error {
	logicalNames := make(map[string]struct{}, len(c.Bonds)+len(c.VLANs))
	for i := range c.Bonds {
		logicalNames[c.Bonds[i].Name] = struct{}{}
	}
	for i := range c.VLANs {
		logicalNames[vlanEffectiveName(&c.VLANs[i])] = struct{}{}
	}

	for i := range c.Bonds {
		for _, member := range c.Bonds[i].Members {
			if _, ok := logicalNames[member]; ok {
				return fmt.Errorf("bond %q: member %q must be a physical interface, not a bond or vlan", c.Bonds[i].Name, member)
			}
		}
	}
	return nil
}

func (c *NetworkConfig) validateInterfaces() error {
	ifaceNames := make(map[string]struct{}, len(c.Interfaces))
	for i := range c.Interfaces {
		iface := &c.Interfaces[i]
		if err := validateInterface(i, iface); err != nil {
			return err
		}
		if _, exists := ifaceNames[iface.Name]; exists {
			return fmt.Errorf("duplicate interface name %q", iface.Name)
		}
		ifaceNames[iface.Name] = struct{}{}
	}
	return nil
}

func (c *NetworkConfig) validateBonds() error {
	bondNames := make(map[string]struct{}, len(c.Bonds))
	for i := range c.Bonds {
		if err := validateBond(i, &c.Bonds[i]); err != nil {
			return err
		}
		if _, exists := bondNames[c.Bonds[i].Name]; exists {
			return fmt.Errorf("duplicate bond name %q", c.Bonds[i].Name)
		}
		bondNames[c.Bonds[i].Name] = struct{}{}
	}
	return nil
}

func (c *NetworkConfig) validateVLANs() error {
	vlanNames := make(map[string]struct{}, len(c.VLANs))
	for i, vlan := range c.VLANs {
		if vlan.Parent == "" {
			return fmt.Errorf("vlan %d: parent required", i)
		}
		if !validName.MatchString(vlan.Parent) {
			return fmt.Errorf("vlan %d: invalid parent name %q", i, vlan.Parent)
		}
		if vlan.Name != "" && !validName.MatchString(vlan.Name) {
			return fmt.Errorf("vlan %d: invalid name %q", i, vlan.Name)
		}
		if vlan.ID < 1 || vlan.ID > 4094 {
			return fmt.Errorf("vlan %d: id must be 1-4094", i)
		}
		if vlan.DHCP && vlan.Address != "" {
			return fmt.Errorf("vlan %d: dhcp and static address are mutually exclusive", i)
		}
		if vlan.Address != "" {
			if err := validateCIDR(vlan.Address); err != nil {
				return fmt.Errorf("vlan %d: invalid address: %w", i, err)
			}
		}
		effName := vlanEffectiveName(&vlan)
		if _, exists := vlanNames[effName]; exists {
			return fmt.Errorf("vlan %d: duplicate vlan name %q", i, effName)
		}
		vlanNames[effName] = struct{}{}
	}
	return nil
}

func vlanEffectiveName(vlan *VLANConfig) string {
	if vlan.Name != "" {
		return vlan.Name
	}
	return fmt.Sprintf("%s.%d", vlan.Parent, vlan.ID)
}

func (c *NetworkConfig) validateRoutes() error {
	for i, route := range c.Routes {
		if route.Destination == "" {
			return fmt.Errorf("route %d: destination required", i)
		}
		if route.Gateway == "" {
			return fmt.Errorf("route %d: gateway required", i)
		}
		if route.Destination != "default" {
			if err := validateCIDR(route.Destination); err != nil {
				return fmt.Errorf("route %d: invalid destination: %w", i, err)
			}
		}
		if err := validateIP(route.Gateway); err != nil {
			return fmt.Errorf("route %d: invalid gateway: %w", i, err)
		}
		if route.Destination != "default" && cidrIsIPv6(route.Destination) != ipIsIPv6(route.Gateway) {
			return fmt.Errorf("route %d: destination and gateway IP families differ", i)
		}
		if route.Metric < 0 {
			return fmt.Errorf("route %d: metric must be >= 0", i)
		}
	}
	return nil
}

func validateInterface(i int, iface *InterfaceConfig) error {
	if iface.Name == "" {
		return fmt.Errorf("interface %d: name required", i)
	}
	if !validName.MatchString(iface.Name) {
		return fmt.Errorf("interface %q: invalid name", iface.Name)
	}
	if iface.DHCP && iface.Address != "" {
		return fmt.Errorf("interface %q: dhcp and static address are mutually exclusive", iface.Name)
	}
	if !iface.DHCP && iface.Address == "" {
		return fmt.Errorf("interface %q: address or dhcp required", iface.Name)
	}
	if iface.Address != "" {
		if err := validateCIDR(iface.Address); err != nil {
			return fmt.Errorf("interface %q: invalid address: %w", iface.Name, err)
		}
	}
	if iface.Gateway != "" {
		if err := validateIP(iface.Gateway); err != nil {
			return fmt.Errorf("interface %q: invalid gateway: %w", iface.Name, err)
		}
	}
	if iface.MAC != "" {
		if _, err := net.ParseMAC(iface.MAC); err != nil {
			return fmt.Errorf("interface %q: invalid mac %q", iface.Name, iface.MAC)
		}
	}
	return nil
}

func validateBond(i int, bond *BondConfig) error {
	if bond.Name == "" {
		return fmt.Errorf("bond %d: name required", i)
	}
	if !validName.MatchString(bond.Name) {
		return fmt.Errorf("bond %q: invalid name", bond.Name)
	}
	if len(bond.Members) < 2 {
		return fmt.Errorf("bond %q: at least 2 members required", bond.Name)
	}
	for j, m := range bond.Members {
		if m == "" {
			return fmt.Errorf("bond %q: member %d is empty", bond.Name, j)
		}
		if !validName.MatchString(m) {
			return fmt.Errorf("bond %q: member %q: invalid name", bond.Name, m)
		}
	}
	if bond.Mode == "" {
		return fmt.Errorf("bond %q: mode required", bond.Name)
	}
	if bond.Address != "" {
		if err := validateCIDR(bond.Address); err != nil {
			return fmt.Errorf("bond %q: invalid address: %w", bond.Name, err)
		}
	}
	if bond.Gateway != "" {
		if err := validateIP(bond.Gateway); err != nil {
			return fmt.Errorf("bond %q: invalid gateway: %w", bond.Name, err)
		}
	}
	return nil
}

func validateDNSConfig(cfg *DNSConfig) error {
	for i, s := range cfg.Servers {
		if err := validateIP(s); err != nil {
			return fmt.Errorf("dns server %d: %w", i, err)
		}
	}
	for i, d := range cfg.Search {
		if strings.TrimSpace(d) == "" {
			return fmt.Errorf("dns search %d: empty domain", i)
		}
		if strings.ContainsAny(d, " \t\n\r") {
			return fmt.Errorf("dns search %d: invalid domain %q", i, d)
		}
	}
	return nil
}

func validateCIDR(v string) error {
	_, err := netip.ParsePrefix(v)
	if err != nil {
		return fmt.Errorf("invalid cidr %q: %w", v, err)
	}
	return nil
}

func validateIP(v string) error {
	_, err := netip.ParseAddr(v)
	if err != nil {
		return fmt.Errorf("invalid ip %q: %w", v, err)
	}
	return nil
}

func cidrIsIPv6(v string) bool {
	prefix, err := netip.ParsePrefix(v)
	return err == nil && prefix.Addr().Is6()
}

func ipIsIPv6(v string) bool {
	addr, err := netip.ParseAddr(v)
	return err == nil && addr.Is6()
}

// RenderNetplan renders the configuration as netplan YAML.
// DNS and routes are placed under the first rendered stanza:
// ethernet if present, otherwise bond, otherwise VLAN.
func RenderNetplan(cfg *NetworkConfig) string {
	var b strings.Builder
	dnsAttached := false
	b.WriteString("network:\n")
	b.WriteString("  version: 2\n")
	b.WriteString("  renderer: networkd\n")
	renderNetplanEthernets(&b, cfg, &cfg.DNS, cfg.Routes, &dnsAttached)
	renderNetplanBonds(&b, cfg.Bonds, &cfg.DNS, cfg.Routes, &dnsAttached)
	renderNetplanVLANs(&b, cfg.VLANs, &cfg.DNS, cfg.Routes, &dnsAttached)
	return b.String()
}

func renderNetplanEthernets(b *strings.Builder, cfg *NetworkConfig, dns *DNSConfig, routes []RouteConfig, dnsAttached *bool) {
	backing := netplanBackingEthernets(cfg)
	if len(cfg.Interfaces) == 0 && len(backing) == 0 {
		return
	}
	b.WriteString("  ethernets:\n")
	for i := range cfg.Interfaces {
		renderNetplanInterface(b, &cfg.Interfaces[i])
		attachedRoutes := []RouteConfig(nil)
		if !*dnsAttached {
			renderNetplanIfaceDNS(b, dns)
			attachedRoutes = routes
			*dnsAttached = true
		}
		renderNetplanIfaceRoutes(b, cfg.Interfaces[i].Gateway, attachedRoutes)
	}
	for _, name := range backing {
		fmt.Fprintf(b, "    %s: {}\n", name)
	}
}

func netplanBackingEthernets(cfg *NetworkConfig) []string {
	explicit := make(map[string]struct{}, len(cfg.Interfaces))
	for i := range cfg.Interfaces {
		explicit[cfg.Interfaces[i].Name] = struct{}{}
	}

	logical := make(map[string]struct{}, len(cfg.Bonds)+len(cfg.VLANs))
	for i := range cfg.Bonds {
		logical[cfg.Bonds[i].Name] = struct{}{}
	}
	for i := range cfg.VLANs {
		logical[vlanEffectiveName(&cfg.VLANs[i])] = struct{}{}
	}

	needed := make(map[string]struct{})
	for i := range cfg.Bonds {
		for _, member := range cfg.Bonds[i].Members {
			if _, ok := explicit[member]; ok {
				continue
			}
			needed[member] = struct{}{}
		}
	}
	for i := range cfg.VLANs {
		parent := cfg.VLANs[i].Parent
		if _, ok := explicit[parent]; ok {
			continue
		}
		if _, ok := logical[parent]; ok {
			continue
		}
		needed[parent] = struct{}{}
	}

	names := make([]string, 0, len(needed))
	for name := range needed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderNetplanInterface(b *strings.Builder, iface *InterfaceConfig) {
	fmt.Fprintf(b, "    %s:\n", iface.Name)
	if iface.DHCP {
		b.WriteString("      dhcp4: true\n")
	} else if iface.Address != "" {
		fmt.Fprintf(b, "      addresses: [%s]\n", iface.Address)
	}
	if iface.MTU > 0 {
		fmt.Fprintf(b, "      mtu: %d\n", iface.MTU)
	}
	if iface.MAC != "" {
		fmt.Fprintf(b, "      match:\n        macaddress: %s\n", iface.MAC)
	}
}

func renderNetplanBonds(b *strings.Builder, bonds []BondConfig, dns *DNSConfig, routes []RouteConfig, dnsAttached *bool) {
	if len(bonds) == 0 {
		return
	}
	b.WriteString("  bonds:\n")
	for i := range bonds {
		fmt.Fprintf(b, "    %s:\n", bonds[i].Name)
		fmt.Fprintf(b, "      interfaces: [%s]\n", strings.Join(bonds[i].Members, ", "))
		if bonds[i].Address != "" {
			fmt.Fprintf(b, "      addresses: [%s]\n", bonds[i].Address)
		}
		if bonds[i].MTU > 0 {
			fmt.Fprintf(b, "      mtu: %d\n", bonds[i].MTU)
		}
		fmt.Fprintf(b, "      parameters:\n        mode: %s\n", bonds[i].Mode)
		if bonds[i].LACPRate != "" {
			fmt.Fprintf(b, "        lacp-rate: %s\n", bonds[i].LACPRate)
		}
		if bonds[i].HashPolicy != "" {
			fmt.Fprintf(b, "        transmit-hash-policy: %s\n", bonds[i].HashPolicy)
		}
		attachedRoutes := []RouteConfig(nil)
		if !*dnsAttached {
			renderNetplanIfaceDNS(b, dns)
			attachedRoutes = routes
			*dnsAttached = true
		}
		renderNetplanIfaceRoutes(b, bonds[i].Gateway, attachedRoutes)
	}
}

func renderNetplanVLANs(b *strings.Builder, vlans []VLANConfig, dns *DNSConfig, routes []RouteConfig, dnsAttached *bool) {
	if len(vlans) == 0 {
		return
	}
	b.WriteString("  vlans:\n")
	for i := range vlans {
		name := vlans[i].Name
		if name == "" {
			name = fmt.Sprintf("%s.%d", vlans[i].Parent, vlans[i].ID)
		}
		fmt.Fprintf(b, "    %s:\n", name)
		fmt.Fprintf(b, "      id: %d\n", vlans[i].ID)
		fmt.Fprintf(b, "      link: %s\n", vlans[i].Parent)
		if vlans[i].DHCP {
			b.WriteString("      dhcp4: true\n")
		} else if vlans[i].Address != "" {
			fmt.Fprintf(b, "      addresses: [%s]\n", vlans[i].Address)
		}
		attachedRoutes := []RouteConfig(nil)
		if !*dnsAttached {
			renderNetplanIfaceDNS(b, dns)
			attachedRoutes = routes
			*dnsAttached = true
		}
		renderNetplanIfaceRoutes(b, "", attachedRoutes)
	}
}

func renderNetplanIfaceDNS(b *strings.Builder, dns *DNSConfig) {
	if len(dns.Servers) == 0 && len(dns.Search) == 0 {
		return
	}
	b.WriteString("      nameservers:\n")
	if len(dns.Servers) > 0 {
		fmt.Fprintf(b, "        addresses: [%s]\n", strings.Join(dns.Servers, ", "))
	}
	if len(dns.Search) > 0 {
		fmt.Fprintf(b, "        search: [%s]\n", strings.Join(dns.Search, ", "))
	}
}

func renderNetplanIfaceRoutes(b *strings.Builder, gateway string, routes []RouteConfig) {
	if gateway == "" && len(routes) == 0 {
		return
	}
	b.WriteString("      routes:\n")
	if gateway != "" {
		fmt.Fprintf(b, "        - to: %s\n          via: %s\n", netplanDefaultRoute(gateway), gateway)
	}
	for _, r := range routes {
		fmt.Fprintf(b, "        - to: %s\n          via: %s\n", netplanRouteDestination(r), r.Gateway)
		if r.Metric > 0 {
			fmt.Fprintf(b, "          metric: %d\n", r.Metric)
		}
	}
}

func netplanDefaultRoute(gateway string) string {
	if ipIsIPv6(gateway) {
		return "::/0"
	}
	return "default"
}

func netplanRouteDestination(r RouteConfig) string {
	if r.Destination == "default" {
		return netplanDefaultRoute(r.Gateway)
	}
	return r.Destination
}

// RenderNetworkdUnit renders a systemd-networkd .network unit for an interface.
func RenderNetworkdUnit(iface *InterfaceConfig) string {
	return renderNetworkdUnit(iface, nil)
}

func renderNetworkdUnit(iface *InterfaceConfig, vlans []string) string {
	var b strings.Builder
	b.WriteString("[Match]\n")
	if iface.MAC != "" {
		fmt.Fprintf(&b, "MACAddress=%s\n", iface.MAC)
	} else {
		fmt.Fprintf(&b, "Name=%s\n", iface.Name)
	}
	b.WriteString("\n[Network]\n")
	if iface.DHCP {
		b.WriteString("DHCP=ipv4\n")
	} else if iface.Address != "" {
		fmt.Fprintf(&b, "Address=%s\n", iface.Address)
		if iface.Gateway != "" {
			fmt.Fprintf(&b, "Gateway=%s\n", iface.Gateway)
		}
	}
	for _, vlan := range vlans {
		fmt.Fprintf(&b, "VLAN=%s\n", vlan)
	}
	if iface.MTU > 0 {
		b.WriteString("\n[Link]\n")
		fmt.Fprintf(&b, "MTUBytes=%d\n", iface.MTU)
	}
	return b.String()
}

// Write renders and writes the network configuration to the target OS root.
// rootDir is the mount point of the target root filesystem (e.g., "/newroot").
func Write(rootDir string, family OSFamily, cfg *NetworkConfig) error {
	if rootDir == "" {
		return fmt.Errorf("rootDir is empty")
	}
	if !filepath.IsAbs(rootDir) {
		return fmt.Errorf("rootDir must be absolute, got %q", rootDir)
	}
	if cfg == nil {
		return fmt.Errorf("network config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	configPath := family.ConfigPath()
	if configPath == "" {
		return fmt.Errorf("unsupported OS family %q", family)
	}

	configDir := filepath.Join(rootDir, configPath)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", configDir, err)
	}

	switch family {
	case Ubuntu:
		return writeNetplan(configDir, cfg)
	case Flatcar:
		return writeNetworkd(configDir, cfg)
	case RHEL:
		return writeNMKeyfiles(configDir, cfg)
	default:
		return fmt.Errorf("unsupported OS family %q", family)
	}
}

func writeNetplan(dir string, cfg *NetworkConfig) error {
	content := RenderNetplan(cfg)
	if err := writeFileAtomic(dir, "01-booty-provisioned.yaml", []byte(content), 0o600); err != nil {
		return fmt.Errorf("write netplan config: %w", err)
	}
	return nil
}

func writeNetworkd(dir string, cfg *NetworkConfig) error {
	primaryAttached := false
	parentVLANs := parentVLANMap(cfg.VLANs)
	handledParents := make(map[string]struct{})
	if err := writeNetworkdInterfaces(dir, cfg, parentVLANs, handledParents, &primaryAttached); err != nil {
		return err
	}
	if err := writeNetworkdBonds(dir, cfg, parentVLANs, handledParents, &primaryAttached); err != nil {
		return err
	}
	if err := writeNetworkdParentLinks(dir, parentVLANs, handledParents); err != nil {
		return err
	}
	return writeNetworkdVLANs(dir, cfg, &primaryAttached)
}

func writeNetworkdInterfaces(
	dir string,
	cfg *NetworkConfig,
	parentVLANs map[string][]string,
	handledParents map[string]struct{},
	primaryAttached *bool,
) error {
	for i := range cfg.Interfaces {
		iface := &cfg.Interfaces[i]
		content := renderNetworkdUnit(iface, parentVLANs[iface.Name])
		content = attachNetworkdPrimary(content, &cfg.DNS, cfg.Routes, primaryAttached, true)
		if err := writeNetworkdFile(dir, iface.Name, "network", content); err != nil {
			return fmt.Errorf("write networkd unit for %s: %w", iface.Name, err)
		}
		handledParents[iface.Name] = struct{}{}
	}
	return nil
}

func writeNetworkdBonds(
	dir string,
	cfg *NetworkConfig,
	parentVLANs map[string][]string,
	handledParents map[string]struct{},
	primaryAttached *bool,
) error {
	for i := range cfg.Bonds {
		bond := &cfg.Bonds[i]
		if err := writeNetworkdBond(dir, bond, parentVLANs[bond.Name], &cfg.DNS, cfg.Routes, primaryAttached); err != nil {
			return err
		}
		handledParents[bond.Name] = struct{}{}
	}
	return nil
}

func writeNetworkdBond(
	dir string,
	bond *BondConfig,
	vlans []string,
	dns *DNSConfig,
	routes []RouteConfig,
	primaryAttached *bool,
) error {
	if err := writeNetworkdFile(dir, bond.Name, "netdev", renderNetworkdBondNetdev(bond)); err != nil {
		return fmt.Errorf("write networkd bond netdev for %s: %w", bond.Name, err)
	}
	for _, member := range bond.Members {
		content := renderNetworkdBondMemberUnit(member, bond.Name)
		if err := writeNetworkdFile(dir, "bond-"+bond.Name+"-"+member, "network", content); err != nil {
			return fmt.Errorf("write networkd bond member unit for %s: %w", member, err)
		}
	}
	content := renderNetworkdBondNetwork(bond, vlans)
	content = attachNetworkdPrimary(content, dns, routes, primaryAttached, bond.Address != "")
	if err := writeNetworkdFile(dir, bond.Name, "network", content); err != nil {
		return fmt.Errorf("write networkd bond unit for %s: %w", bond.Name, err)
	}
	return nil
}

func writeNetworkdParentLinks(dir string, parentVLANs map[string][]string, handledParents map[string]struct{}) error {
	for parent, vlans := range parentVLANs {
		if _, ok := handledParents[parent]; ok {
			continue
		}
		iface := &InterfaceConfig{Name: parent}
		content := renderNetworkdUnit(iface, vlans)
		if err := writeNetworkdFile(dir, parent, "network", content); err != nil {
			return fmt.Errorf("write networkd vlan parent unit for %s: %w", parent, err)
		}
	}
	return nil
}

func writeNetworkdVLANs(dir string, cfg *NetworkConfig, primaryAttached *bool) error {
	for i := range cfg.VLANs {
		vlan := &cfg.VLANs[i]
		name := vlanName(vlan)
		if err := writeNetworkdFile(dir, name, "netdev", renderNetworkdVLANNetdev(vlan)); err != nil {
			return fmt.Errorf("write networkd vlan netdev for %s: %w", name, err)
		}
		content := renderNetworkdVLANNetwork(vlan)
		content = attachNetworkdPrimary(content, &cfg.DNS, cfg.Routes, primaryAttached, vlan.DHCP || vlan.Address != "")
		if err := writeNetworkdFile(dir, name, "network", content); err != nil {
			return fmt.Errorf("write networkd vlan unit for %s: %w", name, err)
		}
	}
	return nil
}

func renderNetworkdBondNetdev(bond *BondConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[NetDev]\nName=%s\nKind=bond\n\n[Bond]\nMode=%s\n", bond.Name, bond.Mode)
	if bond.LACPRate != "" {
		fmt.Fprintf(&b, "LACPTransmitRate=%s\n", bond.LACPRate)
	}
	if bond.HashPolicy != "" {
		fmt.Fprintf(&b, "TransmitHashPolicy=%s\n", bond.HashPolicy)
	}
	return b.String()
}

func renderNetworkdBondMemberUnit(member, bondName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Match]\nName=%s\n\n[Network]\nBond=%s\n", member, bondName)
	return b.String()
}

func renderNetworkdBondNetwork(bond *BondConfig, vlans []string) string {
	iface := &InterfaceConfig{Name: bond.Name, Address: bond.Address, Gateway: bond.Gateway, MTU: bond.MTU}
	return renderNetworkdUnit(iface, vlans)
}

func renderNetworkdVLANNetdev(vlan *VLANConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[NetDev]\nName=%s\nKind=vlan\n\n[VLAN]\nId=%d\n", vlanName(vlan), vlan.ID)
	return b.String()
}

func renderNetworkdVLANNetwork(vlan *VLANConfig) string {
	iface := &InterfaceConfig{Name: vlanName(vlan), DHCP: vlan.DHCP, Address: vlan.Address}
	return renderNetworkdUnit(iface, nil)
}

func writeNetworkdFile(dir, name, suffix, content string) error {
	filename := fmt.Sprintf("10-booty-%s.%s", filepath.Base(name), suffix)
	return writeFileAtomic(dir, filename, []byte(content), 0o644)
}

func attachNetworkdPrimary(
	content string,
	dns *DNSConfig,
	routes []RouteConfig,
	primaryAttached *bool,
	canAttach bool,
) string {
	if *primaryAttached || !canAttach {
		return content
	}
	*primaryAttached = true
	if len(dns.Servers) == 0 && len(dns.Search) == 0 && len(routes) == 0 {
		return content
	}
	return appendNetworkdDNSRoutes(content, dns, routes)
}

func appendNetworkdDNSRoutes(content string, dns *DNSConfig, routes []RouteConfig) string {
	// DNS and Domains must appear under [Network], not after [Link].
	// Insert DNS entries before [Link] if present, otherwise append.
	var dnsLines strings.Builder
	for _, s := range dns.Servers {
		fmt.Fprintf(&dnsLines, "DNS=%s\n", s)
	}
	for _, d := range dns.Search {
		fmt.Fprintf(&dnsLines, "Domains=%s\n", d)
	}

	if idx := strings.Index(content, "\n[Link]"); idx >= 0 {
		content = content[:idx] + "\n" + dnsLines.String() + content[idx:]
	} else {
		content += dnsLines.String()
	}

	var b strings.Builder
	b.WriteString(content)
	for _, r := range routes {
		b.WriteString("\n[Route]\n")
		fmt.Fprintf(&b, "Destination=%s\n", routeDestination(r))
		fmt.Fprintf(&b, "Gateway=%s\n", r.Gateway)
		if r.Metric > 0 {
			fmt.Fprintf(&b, "Metric=%d\n", r.Metric)
		}
	}
	return b.String()
}

func routeDestination(r RouteConfig) string {
	if r.Destination != "default" {
		return r.Destination
	}
	if ipIsIPv6(r.Gateway) {
		return "::/0"
	}
	return "0.0.0.0/0"
}

// renderNMKeyfile renders a NetworkManager keyfile for an interface.
func renderNMKeyfile(iface *InterfaceConfig, dns *DNSConfig, routes []RouteConfig) string {
	var b strings.Builder
	b.WriteString("[connection]\n")
	fmt.Fprintf(&b, "id=%s\n", iface.Name)
	b.WriteString("type=ethernet\n")
	fmt.Fprintf(&b, "interface-name=%s\n\n", iface.Name)
	b.WriteString("[ethernet]\n")
	if iface.MAC != "" {
		fmt.Fprintf(&b, "mac-address=%s\n", iface.MAC)
	}
	renderNMIPv4(&b, iface, dns, routes)
	renderNMIPv6(&b, iface, dns, routes)
	return b.String()
}

func renderNMIPv4(b *strings.Builder, iface *InterfaceConfig, dns *DNSConfig, routes []RouteConfig) {
	b.WriteString("\n[ipv4]\n")
	staticIPv4 := iface.Address != "" && !cidrIsIPv6(iface.Address)
	switch {
	case iface.DHCP:
		b.WriteString("method=auto\n")
	case staticIPv4:
		b.WriteString("method=manual\n")
		fmt.Fprintf(b, "address1=%s\n", iface.Address)
		if iface.Gateway != "" && !ipIsIPv6(iface.Gateway) {
			fmt.Fprintf(b, "gateway=%s\n", iface.Gateway)
		}
	default:
		b.WriteString("method=disabled\n")
	}
	if iface.DHCP || staticIPv4 {
		renderNMDNSRoutes(b, dns, routes, false)
	}
}

func renderNMIPv6(b *strings.Builder, iface *InterfaceConfig, dns *DNSConfig, routes []RouteConfig) {
	b.WriteString("\n[ipv6]\n")
	staticIPv6 := iface.Address != "" && cidrIsIPv6(iface.Address)
	hasConfig := staticIPv6 || hasFamilyDNSRoutes(dns, routes, true)
	switch {
	case staticIPv6:
		b.WriteString("method=manual\n")
		fmt.Fprintf(b, "address1=%s\n", iface.Address)
		if iface.Gateway != "" && ipIsIPv6(iface.Gateway) {
			fmt.Fprintf(b, "gateway=%s\n", iface.Gateway)
		}
	case hasConfig:
		b.WriteString("method=auto\n")
	default:
		b.WriteString("method=disabled\n")
	}
	if hasConfig {
		renderNMDNSRoutes(b, dns, routes, true)
	}
}

func renderNMDNSRoutes(b *strings.Builder, dns *DNSConfig, routes []RouteConfig, ipv6 bool) {
	servers := familyDNSServers(dns, ipv6)
	if len(servers) > 0 {
		fmt.Fprintf(b, "dns=%s\n", strings.Join(servers, ";"))
	}
	if dns != nil && len(dns.Search) > 0 {
		fmt.Fprintf(b, "dns-search=%s\n", strings.Join(dns.Search, ";"))
	}
	for i, r := range familyRoutes(routes, ipv6) {
		fmt.Fprintf(b, "route%d=%s,%s", i+1, routeDestination(r), r.Gateway)
		if r.Metric > 0 {
			fmt.Fprintf(b, ",%d", r.Metric)
		}
		b.WriteByte('\n')
	}
}

func hasFamilyDNSRoutes(dns *DNSConfig, routes []RouteConfig, ipv6 bool) bool {
	return len(familyDNSServers(dns, ipv6)) > 0 || len(familyRoutes(routes, ipv6)) > 0
}

func familyDNSServers(dns *DNSConfig, ipv6 bool) []string {
	if dns == nil {
		return nil
	}
	servers := make([]string, 0, len(dns.Servers))
	for _, server := range dns.Servers {
		if ipIsIPv6(server) == ipv6 {
			servers = append(servers, server)
		}
	}
	return servers
}

func familyRoutes(routes []RouteConfig, ipv6 bool) []RouteConfig {
	filtered := make([]RouteConfig, 0, len(routes))
	for _, route := range routes {
		if routeIsIPv6(route) == ipv6 {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func routeIsIPv6(route RouteConfig) bool {
	if route.Destination != "default" {
		return cidrIsIPv6(route.Destination)
	}
	return ipIsIPv6(route.Gateway)
}

func parentVLANMap(vlans []VLANConfig) map[string][]string {
	parents := make(map[string][]string, len(vlans))
	for i := range vlans {
		vlan := &vlans[i]
		parents[vlan.Parent] = append(parents[vlan.Parent], vlanName(vlan))
	}
	return parents
}

func vlanName(vlan *VLANConfig) string {
	if vlan.Name != "" {
		return vlan.Name
	}
	return fmt.Sprintf("%s.%d", vlan.Parent, vlan.ID)
}

func writeNMKeyfiles(dir string, cfg *NetworkConfig) error {
	primaryAttached := false
	parentVLANs := parentVLANMap(cfg.VLANs)
	handledParents := make(map[string]struct{})
	if err := writeNMInterfaces(dir, cfg, handledParents, &primaryAttached); err != nil {
		return err
	}
	if err := writeNMBonds(dir, cfg, handledParents, &primaryAttached); err != nil {
		return err
	}
	if err := writeNMParentLinks(dir, parentVLANs, handledParents); err != nil {
		return err
	}
	return writeNMVLANs(dir, cfg, &primaryAttached)
}

func writeNMInterfaces(
	dir string,
	cfg *NetworkConfig,
	handledParents map[string]struct{},
	primaryAttached *bool,
) error {
	var emptyDNS DNSConfig
	for i := range cfg.Interfaces {
		iface := &cfg.Interfaces[i]
		dns, routes := nmPrimaryPayload(&cfg.DNS, cfg.Routes, &emptyDNS, primaryAttached, true)
		content := renderNMKeyfile(iface, dns, routes)
		filename := nmFilename(iface.Name)
		if err := writeFileAtomic(dir, filename, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write nm keyfile for %s: %w", iface.Name, err)
		}
		handledParents[iface.Name] = struct{}{}
	}
	return nil
}

func writeNMBonds(dir string, cfg *NetworkConfig, handledParents map[string]struct{}, primaryAttached *bool) error {
	var emptyDNS DNSConfig
	for i := range cfg.Bonds {
		bond := &cfg.Bonds[i]
		dns, routes := nmPrimaryPayload(&cfg.DNS, cfg.Routes, &emptyDNS, primaryAttached, bond.Address != "")
		if err := writeNMBond(dir, bond, dns, routes); err != nil {
			return err
		}
		handledParents[bond.Name] = struct{}{}
	}
	return nil
}

func writeNMBond(dir string, bond *BondConfig, dns *DNSConfig, routes []RouteConfig) error {
	content := renderNMBondKeyfile(bond, dns, routes)
	if err := writeFileAtomic(dir, nmFilename(bond.Name), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write nm bond keyfile for %s: %w", bond.Name, err)
	}
	for _, member := range bond.Members {
		content := renderNMBondPortKeyfile(bond.Name, member)
		name := fmt.Sprintf("%s-%s", bond.Name, member)
		if err := writeFileAtomic(dir, nmFilename(name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write nm bond port keyfile for %s: %w", member, err)
		}
	}
	return nil
}

func writeNMParentLinks(dir string, parentVLANs map[string][]string, handledParents map[string]struct{}) error {
	for parent := range parentVLANs {
		if _, ok := handledParents[parent]; ok {
			continue
		}
		content := renderNMKeyfile(&InterfaceConfig{Name: parent}, &DNSConfig{}, nil)
		if err := writeFileAtomic(dir, nmFilename(parent), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write nm vlan parent keyfile for %s: %w", parent, err)
		}
	}
	return nil
}

func writeNMVLANs(dir string, cfg *NetworkConfig, primaryAttached *bool) error {
	var emptyDNS DNSConfig
	for i := range cfg.VLANs {
		vlan := &cfg.VLANs[i]
		attach := vlan.DHCP || vlan.Address != ""
		dns, routes := nmPrimaryPayload(&cfg.DNS, cfg.Routes, &emptyDNS, primaryAttached, attach)
		content := renderNMVLANKeyfile(vlan, dns, routes)
		name := vlanName(vlan)
		if err := writeFileAtomic(dir, nmFilename(name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write nm vlan keyfile for %s: %w", name, err)
		}
	}
	return nil
}

func nmPrimaryPayload(
	dns *DNSConfig,
	routes []RouteConfig,
	emptyDNS *DNSConfig,
	primaryAttached *bool,
	canAttach bool,
) (*DNSConfig, []RouteConfig) {
	if !canAttach || *primaryAttached {
		return emptyDNS, nil
	}
	*primaryAttached = true
	return dns, routes
}

func renderNMBondKeyfile(bond *BondConfig, dns *DNSConfig, routes []RouteConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[connection]\nid=%s\ntype=bond\ninterface-name=%s\n\n", bond.Name, bond.Name)
	b.WriteString("[bond]\n")
	fmt.Fprintf(&b, "mode=%s\n", bond.Mode)
	if bond.LACPRate != "" {
		fmt.Fprintf(&b, "lacp_rate=%s\n", bond.LACPRate)
	}
	if bond.HashPolicy != "" {
		fmt.Fprintf(&b, "xmit_hash_policy=%s\n", bond.HashPolicy)
	}
	if bond.MTU > 0 {
		fmt.Fprintf(&b, "\n[ethernet]\nmtu=%d\n", bond.MTU)
	}
	iface := &InterfaceConfig{Name: bond.Name, Address: bond.Address, Gateway: bond.Gateway}
	renderNMIPv4(&b, iface, dns, routes)
	renderNMIPv6(&b, iface, dns, routes)
	return b.String()
}

func renderNMBondPortKeyfile(bondName, member string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[connection]\nid=%s-%s\ntype=ethernet\n", bondName, member)
	fmt.Fprintf(&b, "interface-name=%s\nmaster=%s\nslave-type=bond\n\n[ethernet]\n", member, bondName)
	return b.String()
}

func renderNMVLANKeyfile(vlan *VLANConfig, dns *DNSConfig, routes []RouteConfig) string {
	name := vlanName(vlan)
	var b strings.Builder
	fmt.Fprintf(&b, "[connection]\nid=%s\ntype=vlan\ninterface-name=%s\n\n", name, name)
	fmt.Fprintf(&b, "[vlan]\nparent=%s\nid=%d\n", vlan.Parent, vlan.ID)
	iface := &InterfaceConfig{Name: name, DHCP: vlan.DHCP, Address: vlan.Address}
	renderNMIPv4(&b, iface, dns, routes)
	renderNMIPv6(&b, iface, dns, routes)
	return b.String()
}

func nmFilename(name string) string {
	return fmt.Sprintf("booty-%s.nmconnection", filepath.Base(name))
}

func writeFileAtomic(dir, filename string, content []byte, perm os.FileMode) error {
	safeName := filepath.Base(filename)
	path := filepath.Join(dir, safeName)

	tmp, err := os.CreateTemp(dir, ".booty-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	// #nosec G703 -- path uses sanitized basename within target config directory.
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
