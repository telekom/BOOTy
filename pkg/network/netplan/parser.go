package netplan

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/telekom/BOOTy/pkg/network"
)

// ParseDir reads all .yaml files from dir (sorted lexicographically) and
// returns a merged Config.  Later files override earlier ones at the
// top-level map key level (ethernets, tunnels, bridges, etc.).
func ParseDir(dir string) (*Config, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read netplan dir %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)

	if len(files) == 0 {
		return nil, fmt.Errorf("no netplan YAML files in %s", dir)
	}

	merged := &Config{Network: NetworkSection{Version: 2}}
	for _, f := range files {
		cfg, err := ParseFile(f)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		mergeConfig(merged, cfg)
	}
	return merged, nil
}

// ParseFile reads a single netplan YAML file.
func ParseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return &cfg, nil
}

// mergeConfig merges src into dst at the map-key level.
func mergeConfig(dst, src *Config) {
	if src.Network.Version != 0 {
		dst.Network.Version = src.Network.Version
	}
	dst.Network.Ethernets = mergeMaps(dst.Network.Ethernets, src.Network.Ethernets)
	dst.Network.Bonds = mergeMaps(dst.Network.Bonds, src.Network.Bonds)
	dst.Network.Tunnels = mergeMaps(dst.Network.Tunnels, src.Network.Tunnels)
	dst.Network.Bridges = mergeMaps(dst.Network.Bridges, src.Network.Bridges)
	dst.Network.VLANs = mergeMaps(dst.Network.VLANs, src.Network.VLANs)
	dst.Network.DummyDevices = mergeMaps(dst.Network.DummyDevices, src.Network.DummyDevices)
	dst.Network.VRFs = mergeMaps(dst.Network.VRFs, src.Network.VRFs)
}

// mergeMaps copies all entries from src into dst, creating dst if nil.
func mergeMaps[V any](dst, src map[string]V) map[string]V {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]V, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ToNetworkConfig converts a parsed netplan Config (and optional FRR params)
// into the BOOTy network.Config used by the networking stack.
func ToNetworkConfig(np *Config, frr *FRRParams) *network.Config {
	cfg := &network.Config{}

	extractTunnels(np, cfg)
	extractDummyDevices(np, cfg)
	extractBridges(np, cfg)
	dhcpCount, dnsList := extractEthernets(np, cfg)
	dnsList = extractVLANs(np, cfg, dnsList)
	cfg.DNSResolvers = joinUnique(dnsList)
	extractBonds(np, cfg)
	extractVRFs(np, cfg)
	applyFRRParams(frr, cfg)

	if cfg.ASN > 0 && cfg.ProvisionVNI > 0 {
		cfg.NetworkMode = "gobgp"
	}
	if dhcpCount > 0 && cfg.ASN == 0 && cfg.ProvisionVNI == 0 {
		slog.Info("netplan: all interfaces use DHCP, using DHCP mode")
	}
	return cfg
}

func extractTunnels(np *Config, cfg *network.Config) {
	for _, t := range np.Network.Tunnels {
		if !strings.EqualFold(t.Mode, "vxlan") {
			continue
		}
		if t.ID > 0 && cfg.ProvisionVNI == 0 {
			cfg.ProvisionVNI = uint32(t.ID)
		}
		if t.Local != "" && cfg.UnderlayIP == "" {
			cfg.UnderlayIP = t.Local
		}
	}
}

func extractDummyDevices(np *Config, cfg *network.Config) {
	for _, d := range np.Network.DummyDevices {
		if cfg.UnderlayIP != "" {
			break
		}
		for _, addr := range d.Addresses {
			if strings.HasSuffix(addr, "/32") {
				cfg.UnderlayIP = strings.TrimSuffix(addr, "/32")
				break
			}
		}
	}
}

func extractBridges(np *Config, cfg *network.Config) {
	for _, br := range np.Network.Bridges {
		if len(br.Addresses) == 0 {
			continue
		}
		hasVXLAN := false
		for _, member := range br.Interfaces {
			if _, ok := np.Network.Tunnels[member]; ok {
				hasVXLAN = true
				break
			}
		}
		if hasVXLAN && cfg.ProvisionIP == "" {
			cfg.ProvisionIP = br.Addresses[0]
			break
		}
	}
}

func extractEthernets(np *Config, cfg *network.Config) (int, []string) {
	dhcpCount := 0
	var dnsList []string
	for name, eth := range np.Network.Ethernets {
		if eth.DHCP4 != nil && *eth.DHCP4 {
			dhcpCount++
		}
		for _, ll := range eth.LinkLocal {
			if strings.EqualFold(ll, "ipv6") {
				cfg.Interfaces = append(cfg.Interfaces, "auto-detect")
				break
			}
		}
		if eth.Nameservers != nil {
			dnsList = append(dnsList, eth.Nameservers.Addresses...)
		}
		for _, r := range eth.Routes {
			if r.To == "default" && r.Via != "" && cfg.StaticGateway == "" {
				cfg.StaticGateway = r.Via
				slog.Debug("netplan: default gateway from ethernet routes", "iface", name, "gw", r.Via)
			}
		}
		if eth.MTU > cfg.MTU {
			cfg.MTU = eth.MTU
		}
	}
	return dhcpCount, dnsList
}

func extractVLANs(np *Config, cfg *network.Config, dnsList []string) []string {
	for vlanName, v := range np.Network.VLANs {
		vc := network.VLANConfig{ID: v.ID, Parent: v.Link}
		if len(v.Addresses) > 0 {
			vc.Address = v.Addresses[0]
		}
		for _, r := range v.Routes {
			if r.To == "default" && r.Via != "" {
				vc.Gateway = r.Via
			}
		}
		cfg.VLANs = append(cfg.VLANs, vc)
		if v.Nameservers != nil {
			dnsList = append(dnsList, v.Nameservers.Addresses...)
		}
		slog.Debug("netplan: found VLAN", "name", vlanName, "id", v.ID, "link", v.Link)
	}
	return dnsList
}

func extractBonds(np *Config, cfg *network.Config) {
	for name, bond := range np.Network.Bonds {
		if len(bond.Interfaces) == 0 {
			continue
		}
		cfg.BondInterfaces = strings.Join(bond.Interfaces, ",")
		if bond.Parameters != nil && bond.Parameters.Mode != "" {
			cfg.BondMode = bond.Parameters.Mode
		}
		if bond.MTU > cfg.MTU {
			cfg.MTU = bond.MTU
		}
		slog.Debug("netplan: found bond", "name", name, "members", bond.Interfaces)
		break
	}
}

func extractVRFs(np *Config, cfg *network.Config) {
	for name, vrf := range np.Network.VRFs {
		if vrf.Table > 0 && cfg.VRFTableID == 0 {
			cfg.VRFTableID = uint32(vrf.Table)
			cfg.VRFName = name
		}
	}
}

func applyFRRParams(frr *FRRParams, cfg *network.Config) {
	if frr == nil {
		return
	}
	if frr.ASN > 0 {
		cfg.ASN = frr.ASN
	}
	if frr.RouterID != "" && cfg.UnderlayIP == "" {
		cfg.UnderlayIP = frr.RouterID
	}
	if frr.EVPN {
		cfg.EVPNL2Enabled = true
	}
	if len(frr.UnnumberedPeers) > 0 {
		cfg.BGPPeerMode = network.PeerModeUnnumbered
	}
	if len(frr.NumberedPeers) > 0 && len(frr.UnnumberedPeers) == 0 {
		cfg.BGPPeerMode = network.PeerModeNumbered
		cfg.BGPNeighbors = strings.Join(frr.NumberedPeers, ",")
	}
}

// joinUnique deduplicates a string slice and joins with commas.
func joinUnique(items []string) string {
	seen := make(map[string]bool, len(items))
	var result []string
	for _, s := range items {
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return strings.Join(result, ",")
}

// HasNetplanFiles returns true if the given directory contains .yaml files.
func HasNetplanFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			return true
		}
	}
	return false
}
