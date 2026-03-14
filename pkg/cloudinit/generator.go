package cloudinit

import (
	"fmt"

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
	LockPasswd        bool     `yaml:"lock_passwd"`
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
}

// EthConfig represents an ethernet device configuration.
type EthConfig struct {
	Match       *MatchConfig `yaml:"match,omitempty"`
	DHCP4       bool         `yaml:"dhcp4,omitempty"`
	Addresses   []string     `yaml:"addresses,omitempty"`
	Gateway4    string       `yaml:"gateway4,omitempty"`
	Nameservers *NSConfig    `yaml:"nameservers,omitempty"`
	MTU         int          `yaml:"mtu,omitempty"`
}

// BondConfig represents a bond device configuration.
type BondConfig struct {
	Interfaces []string    `yaml:"interfaces"`
	Parameters *BondParams `yaml:"parameters,omitempty"`
	Addresses  []string    `yaml:"addresses,omitempty"`
	DHCP4      bool        `yaml:"dhcp4,omitempty"`
}

// BondParams represents bond parameters.
type BondParams struct {
	Mode               string `yaml:"mode"`
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
	Serial      string
	SSHKeys     []string
	Users       []User
	Packages    []string
	RunCommands [][]string
	WriteFiles  []WriteFile
	NTP         *NTPConfig
	Timezone    string
	StaticIP    string
	Gateway     string
	DNS         []string
	BondIfaces  []string
	BondMode    string
}

// Generate creates cloud-init user-data, meta-data, and network-config.
func Generate(cfg *Config) (*UserData, *MetaData, *NetworkConfig) {
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
		InstanceID:    cfg.Serial,
		LocalHostname: cfg.Hostname,
		Platform:      "booty",
	}

	networkConfig := generateNetworkConfig(cfg)

	return userData, metaData, networkConfig
}

func generateNetworkConfig(cfg *Config) *NetworkConfig {
	nc := &NetworkConfig{Version: 2}

	if len(cfg.BondIfaces) > 0 {
		nc.Bonds = map[string]BondConfig{
			"bond0": {
				Interfaces: cfg.BondIfaces,
				Parameters: &BondParams{Mode: cfg.BondMode},
				Addresses:  addressList(cfg.StaticIP),
				DHCP4:      cfg.StaticIP == "",
			},
		}
		return nc
	}

	if cfg.StaticIP != "" {
		nc.Ethernets = map[string]EthConfig{
			"id0": {
				Match:     &MatchConfig{Driver: "virtio*"},
				DHCP4:     false,
				Addresses: []string{cfg.StaticIP},
				Gateway4:  cfg.Gateway,
				Nameservers: &NSConfig{
					Addresses: cfg.DNS,
				},
			},
		}
		return nc
	}

	nc.Ethernets = map[string]EthConfig{
		"id0": {
			Match: &MatchConfig{Driver: "virtio*"},
			DHCP4: true,
		},
	}
	return nc
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
