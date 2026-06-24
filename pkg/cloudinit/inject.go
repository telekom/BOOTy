package cloudinit

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// InjectNoCloud writes cloud-init seed files to the NoCloud datasource directory.
func InjectNoCloud(rootPath string, ud *UserData, md *MetaData, nc *NetworkConfig) error {
	if err := validateSeedInput(rootPath, ud, md, nc); err != nil {
		return err
	}
	seedDir := filepath.Join(rootPath, "var", "lib", "cloud", "seed", "nocloud")
	if err := os.MkdirAll(seedDir, 0o700); err != nil {
		return fmt.Errorf("create nocloud seed dir: %w", err)
	}

	files, err := renderNoCloudSeedFiles(ud, md, nc)
	if err != nil {
		return err
	}
	return writeSeedFiles(seedDir, ".nocloud-*", files)
}

// InjectConfigDrive writes cloud-init seed files to the ConfigDrive datasource directory.
func InjectConfigDrive(rootPath string, ud *UserData, md *MetaData, nc *NetworkConfig) error {
	if err := validateSeedInput(rootPath, ud, md, nc); err != nil {
		return err
	}
	seedDir := filepath.Join(rootPath, "var", "lib", "cloud", "seed", "config_drive", "openstack", "latest")
	if err := os.MkdirAll(seedDir, 0o700); err != nil {
		return fmt.Errorf("create configdrive seed dir: %w", err)
	}

	files, err := renderConfigDriveSeedFiles(ud, md, nc)
	if err != nil {
		return err
	}
	return writeSeedFiles(seedDir, ".configdrive-*", files)
}

func validateSeedInput(rootPath string, ud *UserData, md *MetaData, nc *NetworkConfig) error {
	if rootPath == "" || !filepath.IsAbs(rootPath) {
		return fmt.Errorf("rootPath must be a non-empty absolute path, got %q", rootPath)
	}
	if ud == nil || md == nil || nc == nil {
		return fmt.Errorf("user-data, meta-data, and network-config must not be nil")
	}
	return nil
}

func renderNoCloudSeedFiles(ud *UserData, md *MetaData, nc *NetworkConfig) (map[string][]byte, error) {
	userData, err := ud.Render()
	if err != nil {
		return nil, fmt.Errorf("render user-data: %w", err)
	}

	metaData, err := md.Render()
	if err != nil {
		return nil, fmt.Errorf("render meta-data: %w", err)
	}

	networkConfig, err := nc.Render()
	if err != nil {
		return nil, fmt.Errorf("render network-config: %w", err)
	}

	return map[string][]byte{
		"user-data":      userData,
		"meta-data":      metaData,
		"network-config": networkConfig,
	}, nil
}

func renderConfigDriveSeedFiles(ud *UserData, md *MetaData, nc *NetworkConfig) (map[string][]byte, error) {
	userData, err := ud.Render()
	if err != nil {
		return nil, fmt.Errorf("render user-data: %w", err)
	}

	metaData, err := json.MarshalIndent(configDriveMetaData(md), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render meta_data.json: %w", err)
	}

	networkConfig, err := configDriveNetworkData(nc)
	if err != nil {
		return nil, err
	}
	networkData, err := json.MarshalIndent(networkConfig, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render network_data.json: %w", err)
	}

	return map[string][]byte{
		"user_data":         userData,
		"meta_data.json":    append(metaData, '\n'),
		"network_data.json": append(networkData, '\n'),
	}, nil
}

func writeSeedFiles(seedDir, tempPattern string, files map[string][]byte) error {
	// Two-phase write: write all files to a temp directory, then rename each
	// to its final path. Failures before the rename phase leave existing seed
	// files untouched; failures during rename can leave earlier files updated.
	tmpDir, err := os.MkdirTemp(filepath.Dir(seedDir), tempPattern)
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	for name, data := range files {
		tmp := filepath.Join(tmpDir, name)
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	// All temp files written — now rename to final paths.
	for name := range files {
		src := filepath.Join(tmpDir, name)
		dst := filepath.Join(seedDir, name)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rename %s: %w", name, err)
		}
	}
	return nil
}

type openStackMetaData struct {
	UUID        string            `json:"uuid,omitempty"`
	Hostname    string            `json:"hostname,omitempty"`
	Name        string            `json:"name,omitempty"`
	LaunchIndex int               `json:"launch_index"`
	Meta        map[string]string `json:"meta,omitempty"`
}

type openStackNetworkData struct {
	Links    []openStackLink    `json:"links"`
	Networks []openStackNetwork `json:"networks"`
	Services []openStackService `json:"services"`
}

type openStackLink struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	Name               string   `json:"name,omitempty"`
	EthernetMACAddress string   `json:"ethernet_mac_address,omitempty"`
	BondLinks          []string `json:"bond_links,omitempty"`
	BondMode           string   `json:"bond_mode,omitempty"`
	MTU                *int     `json:"mtu,omitempty"`
}

type openStackNetwork struct {
	ID        string             `json:"id"`
	Type      string             `json:"type"`
	Link      string             `json:"link"`
	IPAddress string             `json:"ip_address,omitempty"`
	Netmask   string             `json:"netmask,omitempty"`
	Gateway   string             `json:"gateway,omitempty"`
	Routes    []openStackRoute   `json:"routes,omitempty"`
	Services  []openStackService `json:"services,omitempty"`
}

type openStackRoute struct {
	Network string `json:"network"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
}

type openStackService struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

func configDriveMetaData(md *MetaData) openStackMetaData {
	meta := map[string]string{}
	if strings.TrimSpace(md.Platform) != "" {
		meta["platform"] = strings.TrimSpace(md.Platform)
	}
	if len(meta) == 0 {
		meta = nil
	}
	return openStackMetaData{
		UUID:        strings.TrimSpace(md.InstanceID),
		Hostname:    strings.TrimSpace(md.LocalHostname),
		Name:        strings.TrimSpace(md.LocalHostname),
		LaunchIndex: 0,
		Meta:        meta,
	}
}

func configDriveNetworkData(nc *NetworkConfig) (openStackNetworkData, error) {
	data := openStackNetworkData{
		Links:    []openStackLink{},
		Networks: []openStackNetwork{},
		Services: []openStackService{},
	}
	for name, eth := range nc.Ethernets {
		linkID := strings.TrimSpace(name)
		if linkID == "" {
			continue
		}
		data.Links = append(data.Links, ethernetLink(linkID, eth.Match, eth.MTU))
		networks, err := networksForLink(linkID, eth.DHCP4, eth.Addresses, eth.Gateway4)
		if err != nil {
			return data, err
		}
		services := servicesForNameservers(eth.Nameservers)
		attachServicesToNetworks(networks, services)
		data.Networks = append(data.Networks, networks...)
		data.Services = append(data.Services, services...)
	}
	for name, bond := range nc.Bonds {
		next, err := appendBondNetworkData(data, strings.TrimSpace(name), &bond)
		if err != nil {
			return data, err
		}
		data = next
	}
	return data, nil
}

func appendBondNetworkData(data openStackNetworkData, name string, bond *BondConfig) (openStackNetworkData, error) {
	if name == "" {
		return data, nil
	}
	for _, iface := range bond.Interfaces {
		iface = strings.TrimSpace(iface)
		if iface != "" {
			data.Links = append(data.Links, ethernetLink(iface, nil, 0))
		}
	}
	data.Links = append(data.Links, bondLink(name, bond))
	networks, err := networksForLink(name, bond.DHCP4, bond.Addresses, bond.Gateway4)
	if err != nil {
		return data, err
	}
	services := servicesForNameservers(bond.Nameservers)
	attachServicesToNetworks(networks, services)
	data.Networks = append(data.Networks, networks...)
	data.Services = append(data.Services, services...)
	return data, nil
}

func ethernetLink(id string, match *MatchConfig, mtu int) openStackLink {
	link := openStackLink{ID: id, Type: "phy", Name: id}
	if match != nil {
		link.EthernetMACAddress = strings.TrimSpace(match.MACAddress)
	}
	if mtu > 0 {
		link.MTU = &mtu
	}
	return link
}

func bondLink(id string, bond *BondConfig) openStackLink {
	link := openStackLink{ID: id, Type: "bond", Name: id}
	for _, iface := range bond.Interfaces {
		if iface = strings.TrimSpace(iface); iface != "" {
			link.BondLinks = append(link.BondLinks, iface)
		}
	}
	if bond.Parameters != nil {
		link.BondMode = strings.TrimSpace(bond.Parameters.Mode)
	}
	return link
}

func networksForLink(linkID string, dhcp4 bool, addresses []string, gateway string) ([]openStackNetwork, error) {
	if dhcp4 || len(addresses) == 0 {
		return []openStackNetwork{{ID: linkID + "-ipv4", Type: "ipv4_dhcp", Link: linkID}}, nil
	}
	var networks []openStackNetwork
	for i, address := range addresses {
		network, err := staticNetworkForLink(linkID, address, gateway)
		if err != nil {
			return nil, err
		}
		network.ID = fmt.Sprintf("%s-ipv4-%d", linkID, i)
		networks = append(networks, network)
	}
	if len(networks) == 0 {
		return []openStackNetwork{{ID: linkID + "-ipv4", Type: "ipv4_dhcp", Link: linkID}}, nil
	}
	return networks, nil
}

func staticNetworkForLink(linkID, address, gateway string) (openStackNetwork, error) {
	ip, ipNet, err := net.ParseCIDR(strings.TrimSpace(address))
	if err != nil || ip.To4() == nil {
		return openStackNetwork{}, fmt.Errorf("invalid IPv4 CIDR %q", address)
	}
	network := openStackNetwork{
		Type:      "ipv4",
		Link:      linkID,
		IPAddress: ip.String(),
		Netmask:   dottedIPv4Mask(ipNet.Mask),
	}
	if gateway = strings.TrimSpace(gateway); gateway != "" {
		network.Gateway = gateway
		network.Routes = []openStackRoute{{
			Network: "0.0.0.0",
			Netmask: "0.0.0.0",
			Gateway: gateway,
		}}
	}
	return network, nil
}

func dottedIPv4Mask(mask net.IPMask) string {
	ip := net.IP(mask).To4()
	if ip == nil {
		return ""
	}
	return ip.String()
}

func servicesForNameservers(ns *NSConfig) []openStackService {
	if ns == nil {
		return nil
	}
	var services []openStackService
	for _, address := range ns.Addresses {
		if address = strings.TrimSpace(address); address != "" {
			services = append(services, openStackService{Type: "dns", Address: address})
		}
	}
	return services
}

func attachServicesToNetworks(networks []openStackNetwork, services []openStackService) {
	if len(services) == 0 {
		return
	}
	for i := range networks {
		networks[i].Services = append(networks[i].Services, services...)
	}
}
