// Package persist manages network configuration persistence to target OS.
package persist

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
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

func (c *NetworkConfig) validateInterfaces() error {
	ifaceNames := make(map[string]struct{}, len(c.Interfaces))
	for i := range c.Interfaces {
		iface := &c.Interfaces[i]
		if _, exists := ifaceNames[iface.Name]; exists {
			return fmt.Errorf("duplicate interface name %q", iface.Name)
		}
		ifaceNames[iface.Name] = struct{}{}
		if err := validateInterface(i, iface); err != nil {
			return err
		}
	}
	return nil
}

func (c *NetworkConfig) validateBonds() error {
	for i := range c.Bonds {
		if err := validateBond(i, &c.Bonds[i]); err != nil {
			return err
		}
	}
	return nil
}

func (c *NetworkConfig) validateVLANs() error {
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
	}
	return nil
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
	if _, err := netip.ParsePrefix(v); err != nil {
		return fmt.Errorf("invalid cidr %q", v)
	}
	return nil
}

func validateIP(v string) error {
	if _, err := netip.ParseAddr(v); err != nil {
		return fmt.Errorf("invalid ip %q", v)
	}
	return nil
}

// RenderNetplan renders the configuration as netplan YAML.
// DNS and routes are placed under the first interface for netplan compatibility.
func RenderNetplan(cfg *NetworkConfig) string {
	var b strings.Builder
	b.WriteString("network:\n")
	b.WriteString("  version: 2\n")
	b.WriteString("  renderer: networkd\n")
	renderNetplanEthernets(&b, cfg.Interfaces, &cfg.DNS, cfg.Routes)
	renderNetplanBonds(&b, cfg.Bonds)
	renderNetplanVLANs(&b, cfg.VLANs)
	return b.String()
}

func renderNetplanEthernets(b *strings.Builder, ifaces []InterfaceConfig, dns *DNSConfig, routes []RouteConfig) {
	if len(ifaces) == 0 {
		return
	}
	b.WriteString("  ethernets:\n")
	for i := range ifaces {
		renderNetplanInterface(b, &ifaces[i])
		// Attach DNS and routes to the first interface.
		if i == 0 {
			renderNetplanIfaceDNS(b, dns)
			renderNetplanIfaceRoutes(b, routes)
		}
	}
}

func renderNetplanInterface(b *strings.Builder, iface *InterfaceConfig) {
	fmt.Fprintf(b, "    %s:\n", iface.Name)
	if iface.DHCP {
		b.WriteString("      dhcp4: true\n")
	} else if iface.Address != "" {
		fmt.Fprintf(b, "      addresses: [%s]\n", iface.Address)
	}
	if iface.Gateway != "" {
		fmt.Fprintf(b, "      gateway4: %s\n", iface.Gateway)
	}
	if iface.MTU > 0 {
		fmt.Fprintf(b, "      mtu: %d\n", iface.MTU)
	}
	if iface.MAC != "" {
		fmt.Fprintf(b, "      match:\n        macaddress: %s\n", iface.MAC)
	}
}

func renderNetplanBonds(b *strings.Builder, bonds []BondConfig) {
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
		if bonds[i].Gateway != "" {
			fmt.Fprintf(b, "      gateway4: %s\n", bonds[i].Gateway)
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
	}
}

func renderNetplanVLANs(b *strings.Builder, vlans []VLANConfig) {
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

func renderNetplanIfaceRoutes(b *strings.Builder, routes []RouteConfig) {
	if len(routes) == 0 {
		return
	}
	b.WriteString("      routes:\n")
	for _, r := range routes {
		fmt.Fprintf(b, "        - to: %s\n          via: %s\n", r.Destination, r.Gateway)
		if r.Metric > 0 {
			fmt.Fprintf(b, "          metric: %d\n", r.Metric)
		}
	}
}

// RenderNetworkdUnit renders a systemd-networkd .network unit for an interface.
func RenderNetworkdUnit(iface *InterfaceConfig) string {
	var b strings.Builder
	b.WriteString("[Match]\n")
	if iface.MAC != "" {
		fmt.Fprintf(&b, "MACAddress=%s\n", iface.MAC)
	} else {
		fmt.Fprintf(&b, "Name=%s\n", iface.Name)
	}
	b.WriteString("\n[Network]\n")
	if iface.DHCP {
		b.WriteString("DHCP=yes\n")
	} else if iface.Address != "" {
		fmt.Fprintf(&b, "Address=%s\n", iface.Address)
		if iface.Gateway != "" {
			fmt.Fprintf(&b, "Gateway=%s\n", iface.Gateway)
		}
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
	if len(cfg.Bonds) > 0 || len(cfg.VLANs) > 0 {
		return fmt.Errorf("networkd renderer does not yet support bonds or vlans")
	}
	for i := range cfg.Interfaces {
		content := RenderNetworkdUnit(&cfg.Interfaces[i])
		if i == 0 && (len(cfg.DNS.Servers) > 0 || len(cfg.DNS.Search) > 0 || len(cfg.Routes) > 0) {
			content = appendNetworkdDNSRoutes(content, &cfg.DNS, cfg.Routes)
		}
		filename := fmt.Sprintf("10-booty-%s.network", filepath.Base(cfg.Interfaces[i].Name))
		if err := writeFileAtomic(dir, filename, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write networkd unit for %s: %w", cfg.Interfaces[i].Name, err)
		}
	}
	return nil
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
		fmt.Fprintf(&b, "Destination=%s\n", r.Destination)
		fmt.Fprintf(&b, "Gateway=%s\n", r.Gateway)
		if r.Metric > 0 {
			fmt.Fprintf(&b, "Metric=%d\n", r.Metric)
		}
	}
	return b.String()
}

// renderNMKeyfile renders a NetworkManager keyfile for an interface.
func renderNMKeyfile(iface *InterfaceConfig, dns *DNSConfig, routes []RouteConfig) string {
	var b strings.Builder
	b.WriteString("[connection]\n")
	fmt.Fprintf(&b, "id=%s\n", iface.Name)
	b.WriteString("type=ethernet\n\n")
	b.WriteString("[ethernet]\n")
	if iface.MAC != "" {
		fmt.Fprintf(&b, "mac-address=%s\n", iface.MAC)
	}
	b.WriteString("\n[ipv4]\n")
	if iface.DHCP {
		b.WriteString("method=auto\n")
	} else if iface.Address != "" {
		b.WriteString("method=manual\n")
		fmt.Fprintf(&b, "address1=%s\n", iface.Address)
		if iface.Gateway != "" {
			fmt.Fprintf(&b, "gateway=%s\n", iface.Gateway)
		}
	}
	if len(dns.Servers) > 0 {
		fmt.Fprintf(&b, "dns=%s\n", strings.Join(dns.Servers, ";"))
	}
	if len(dns.Search) > 0 {
		fmt.Fprintf(&b, "dns-search=%s\n", strings.Join(dns.Search, ";"))
	}
	for i, r := range routes {
		fmt.Fprintf(&b, "route%d=%s,%s", i+1, r.Destination, r.Gateway)
		if r.Metric > 0 {
			fmt.Fprintf(&b, ",%d", r.Metric)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func writeNMKeyfiles(dir string, cfg *NetworkConfig) error {
	if len(cfg.Bonds) > 0 || len(cfg.VLANs) > 0 {
		return fmt.Errorf("networkmanager renderer does not yet support bonds or vlans")
	}
	for i := range cfg.Interfaces {
		content := renderNMKeyfile(&cfg.Interfaces[i], &cfg.DNS, cfg.Routes)
		filename := fmt.Sprintf("booty-%s.nmconnection", filepath.Base(cfg.Interfaces[i].Name))
		if err := writeFileAtomic(dir, filename, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write nm keyfile for %s: %w", cfg.Interfaces[i].Name, err)
		}
	}
	return nil
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
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	// #nosec G703 -- path uses sanitized basename within target config directory.
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
