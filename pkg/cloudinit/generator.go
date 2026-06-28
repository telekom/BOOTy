package cloudinit

import (
	"fmt"
	"net/netip"
	"strings"

	"gopkg.in/yaml.v3"
)

// UserData represents cloud-init user-data configuration.
type UserData struct {
	Hostname          string      `yaml:"hostname,omitempty"`
	FQDN              string      `yaml:"fqdn,omitempty"`
	ManageEtcHosts    bool        `yaml:"manage_etc_hosts,omitempty"`
	Users             []User      `yaml:"users,omitempty"`
	SSHAuthorizedKeys []string    `yaml:"ssh_authorized_keys,omitempty"`
	Packages          []string    `yaml:"packages,omitempty"`
	PackageUpdate     bool        `yaml:"package_update,omitempty"`
	RunCmd            [][]string  `yaml:"runcmd,omitempty"`
	WriteFiles        []WriteFile `yaml:"write_files,omitempty"`
	NTP               *NTPConfig  `yaml:"ntp,omitempty"`
	Timezone          string      `yaml:"timezone,omitempty"`
}

// User represents a cloud-init user entry.
type User struct {
	Name              string   `yaml:"name"`
	Groups            string   `yaml:"groups,omitempty"`
	Shell             string   `yaml:"shell,omitempty"`
	Sudo              string   `yaml:"sudo,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
	LockPasswd        *bool    `yaml:"lock_passwd,omitempty"`
}

// WriteFile represents a file to write during cloud-init.
type WriteFile struct {
	Path        string `yaml:"path"`
	Content     string `yaml:"content"`
	Owner       string `yaml:"owner,omitempty"`
	Permissions string `yaml:"permissions,omitempty"`
}

// NTPConfig represents cloud-init NTP configuration.
type NTPConfig struct {
	Enabled bool     `yaml:"enabled"`
	Servers []string `yaml:"servers,omitempty"`
	Pools   []string `yaml:"pools,omitempty"`
}

// MetaData represents cloud-init meta-data.
type MetaData struct {
	InstanceID    string `yaml:"instance-id"`
	LocalHostname string `yaml:"local-hostname"`
	Platform      string `yaml:"platform,omitempty"`
}

// NetworkConfig represents cloud-init network-config v2.
type NetworkConfig struct {
	Version   int                   `yaml:"version"`
	Ethernets map[string]EthConfig  `yaml:"ethernets,omitempty"`
	Bonds     map[string]BondConfig `yaml:"bonds,omitempty"`
	VLANs     map[string]VLANConfig `yaml:"vlans,omitempty"`
}

// EthConfig represents an ethernet device configuration.
type EthConfig struct {
	Match       *MatchConfig `yaml:"match,omitempty"`
	DHCP4       bool         `yaml:"dhcp4,omitempty"`
	DHCP6       bool         `yaml:"dhcp6,omitempty"`
	Addresses   []string     `yaml:"addresses,omitempty"`
	Gateway4    string       `yaml:"gateway4,omitempty"`
	Gateway6    string       `yaml:"gateway6,omitempty"`
	Nameservers *NSConfig    `yaml:"nameservers,omitempty"`
	MTU         int          `yaml:"mtu,omitempty"`
}

// BondConfig represents a bond device configuration.
type BondConfig struct {
	Interfaces  []string    `yaml:"interfaces"`
	Parameters  *BondParams `yaml:"parameters,omitempty"`
	Addresses   []string    `yaml:"addresses,omitempty"`
	Gateway4    string      `yaml:"gateway4,omitempty"`
	Gateway6    string      `yaml:"gateway6,omitempty"`
	Nameservers *NSConfig   `yaml:"nameservers,omitempty"`
	DHCP4       bool        `yaml:"dhcp4,omitempty"`
	DHCP6       bool        `yaml:"dhcp6,omitempty"`
}

// VLANConfig represents a VLAN device configuration.
type VLANConfig struct {
	ID          int       `yaml:"id"`
	Link        string    `yaml:"link"`
	DHCP4       bool      `yaml:"dhcp4,omitempty"`
	DHCP6       bool      `yaml:"dhcp6,omitempty"`
	Addresses   []string  `yaml:"addresses,omitempty"`
	Gateway4    string    `yaml:"gateway4,omitempty"`
	Gateway6    string    `yaml:"gateway6,omitempty"`
	Nameservers *NSConfig `yaml:"nameservers,omitempty"`
	MTU         int       `yaml:"mtu,omitempty"`
}

// BondParams represents bond parameters.
type BondParams struct {
	Mode               string `yaml:"mode,omitempty"`
	LACPRate           string `yaml:"lacp-rate,omitempty"`
	TransmitHashPolicy string `yaml:"transmit-hash-policy,omitempty"`
}

// MatchConfig matches network interfaces.
type MatchConfig struct {
	MACAddress string `yaml:"macaddress,omitempty"`
	Driver     string `yaml:"driver,omitempty"`
}

// NSConfig represents nameserver configuration.
type NSConfig struct {
	Addresses []string `yaml:"addresses,omitempty"`
	Search    []string `yaml:"search,omitempty"`
}

// Config holds the input configuration for cloud-init generation.
type Config struct {
	Hostname    string
	FQDN        string
	InstanceID  string
	SSHKeys     []string
	Users       []User
	Packages    []string
	RunCommands [][]string
	WriteFiles  []WriteFile
	NTP         *NTPConfig
	Timezone    string
	StaticIP    string
	Interface   string
	Gateway     string
	DNS         []string
	BondIfaces  []string
	BondMode    string
	MACAddress  string
	VLANs       []VLANInput
}

// VLANInput holds BOOTy VLAN settings for cloud-init seed generation.
type VLANInput struct {
	ID      int
	Parent  string
	Address string
	Gateway string
}

// Generate creates cloud-init user-data, meta-data, and network-config.
func Generate(cfg *Config) (*UserData, *MetaData, *NetworkConfig) {
	if cfg == nil {
		cfg = &Config{}
	}
	userData := &UserData{
		Hostname:          cfg.Hostname,
		FQDN:              cfg.FQDN,
		ManageEtcHosts:    true,
		Users:             cfg.Users,
		SSHAuthorizedKeys: cfg.SSHKeys,
		Packages:          cfg.Packages,
		RunCmd:            cfg.RunCommands,
		WriteFiles:        cfg.WriteFiles,
		NTP:               cfg.NTP,
		Timezone:          cfg.Timezone,
	}

	metaData := &MetaData{
		InstanceID:    cfg.InstanceID,
		LocalHostname: cfg.Hostname,
		Platform:      "booty",
	}

	networkConfig := generateNetworkConfig(cfg)

	return userData, metaData, networkConfig
}

func generateNetworkConfig(cfg *Config) *NetworkConfig {
	nc := &NetworkConfig{Version: 2}
	vlanInputs := cleanVLANInputs(cfg.VLANs)
	bondIfaces := cleanBondIfaces(cfg.BondIfaces)

	if len(bondIfaces) > 0 {
		addBondConfig(nc, cfg, bondIfaces, vlanInputs)
		addVLANConfigs(nc, vlanInputs, cfg.DNS)
		return nc
	}

	if cfg.StaticIP != "" {
		addStaticEthernetConfig(nc, cfg)
		addVLANConfigs(nc, vlanInputs, cfg.DNS)
		return nc
	}

	if len(vlanInputs) == 0 || strings.TrimSpace(cfg.Interface) != "" || strings.TrimSpace(cfg.MACAddress) != "" {
		addDHCPEthernetConfig(nc, cfg)
	}
	addVLANConfigs(nc, vlanInputs, cfg.DNS)
	return nc
}

func cleanBondIfaces(ifaces []string) []string {
	var cleaned []string
	for _, iface := range ifaces {
		if iface = strings.TrimSpace(iface); iface != "" {
			cleaned = append(cleaned, iface)
		}
	}
	return cleaned
}

func cleanVLANInputs(vlans []VLANInput) []VLANInput {
	var cleaned []VLANInput
	for _, vlan := range vlans {
		vlan.Parent = strings.TrimSpace(vlan.Parent)
		vlan.Address = strings.TrimSpace(vlan.Address)
		vlan.Gateway = strings.TrimSpace(vlan.Gateway)
		if vlan.ID > 0 && vlan.Parent != "" {
			cleaned = append(cleaned, vlan)
		}
	}
	return cleaned
}

func addBondConfig(nc *NetworkConfig, cfg *Config, bondIfaces []string, vlans []VLANInput) {
	bondMode := strings.TrimSpace(cfg.BondMode)
	if bondMode == "" {
		bondMode = "802.3ad"
	}
	bond := BondConfig{
		Interfaces: bondIfaces,
		Parameters: &BondParams{Mode: bondMode},
		Addresses:  addressList(cfg.StaticIP),
		DHCP4:      cfg.StaticIP == "" && !hasVLANParent(vlans, "bond0"),
	}
	if cfg.StaticIP != "" {
		bond.Gateway4, bond.Gateway6 = gatewayFields(cfg.Gateway)
	}
	if len(cfg.DNS) > 0 {
		bond.Nameservers = &NSConfig{Addresses: cfg.DNS}
	}
	nc.Bonds = map[string]BondConfig{"bond0": bond}
}

func addStaticEthernetConfig(nc *NetworkConfig, cfg *Config) {
	eth := EthConfig{
		Addresses: []string{strings.TrimSpace(cfg.StaticIP)},
	}
	eth.Gateway4, eth.Gateway6 = gatewayFields(cfg.Gateway)
	if len(cfg.DNS) > 0 {
		eth.Nameservers = &NSConfig{Addresses: cfg.DNS}
	}
	if strings.TrimSpace(cfg.MACAddress) != "" {
		eth.Match = &MatchConfig{MACAddress: strings.TrimSpace(cfg.MACAddress)}
	}
	nc.Ethernets = map[string]EthConfig{ethernetID(cfg.Interface): eth}
}

func addDHCPEthernetConfig(nc *NetworkConfig, cfg *Config) {
	eth := EthConfig{DHCP4: true}
	if strings.TrimSpace(cfg.MACAddress) != "" {
		eth.Match = &MatchConfig{MACAddress: strings.TrimSpace(cfg.MACAddress)}
	}
	nc.Ethernets = map[string]EthConfig{ethernetID(cfg.Interface): eth}
}

func addVLANConfigs(nc *NetworkConfig, vlans []VLANInput, dns []string) {
	if len(vlans) == 0 {
		return
	}
	if nc.VLANs == nil {
		nc.VLANs = map[string]VLANConfig{}
	}
	for _, input := range vlans {
		ensureVLANParent(nc, input.Parent)
		vlan := VLANConfig{
			ID:        input.ID,
			Link:      input.Parent,
			Addresses: addressList(input.Address),
			DHCP4:     input.Address == "",
		}
		if input.Address != "" {
			vlan.Gateway4, vlan.Gateway6 = gatewayFields(input.Gateway)
		}
		if len(dns) > 0 {
			vlan.Nameservers = &NSConfig{Addresses: dns}
		}
		nc.VLANs[vlanID(input)] = vlan
	}
}

func ensureVLANParent(nc *NetworkConfig, parent string) {
	if _, ok := nc.Bonds[parent]; ok {
		return
	}
	if nc.Ethernets == nil {
		nc.Ethernets = map[string]EthConfig{}
	}
	if _, ok := nc.Ethernets[parent]; !ok {
		nc.Ethernets[parent] = EthConfig{}
	}
}

func hasVLANParent(vlans []VLANInput, parent string) bool {
	for _, vlan := range vlans {
		if vlan.Parent == parent {
			return true
		}
	}
	return false
}

func vlanID(vlan VLANInput) string {
	return fmt.Sprintf("%s.%d", vlan.Parent, vlan.ID)
}

func gatewayFields(gateway string) (gateway4, gateway6 string) {
	gateway = strings.TrimSpace(gateway)
	if gateway == "" {
		return "", ""
	}
	addr, err := netip.ParseAddr(gateway)
	if err == nil && addr.Is6() {
		return "", gateway
	}
	return gateway, ""
}

func ethernetID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "id0"
	}
	return name
}

func addressList(ip string) []string {
	if ip == "" {
		return nil
	}
	return []string{ip}
}

// Render serializes UserData to YAML with the cloud-config header.
func (u *UserData) Render() ([]byte, error) {
	data, err := yaml.Marshal(u)
	if err != nil {
		return nil, fmt.Errorf("marshal user-data: %w", err)
	}
	return append([]byte("#cloud-config\n"), data...), nil
}

// Render serializes MetaData to YAML.
func (m *MetaData) Render() ([]byte, error) {
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal meta-data: %w", err)
	}
	return data, nil
}

// Render serializes NetworkConfig to YAML.
func (n *NetworkConfig) Render() ([]byte, error) {
	data, err := yaml.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("marshal network-config: %w", err)
	}
	return data, nil
}
