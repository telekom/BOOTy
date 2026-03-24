//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/network"
)

// ---------------------------------------------------------------------------
// Gap 1: DHCP / Static / Bond / VLAN mode detection and configuration
// ---------------------------------------------------------------------------

func TestNetworkModeDetectionStaticIP(t *testing.T) {
	cfg := &network.Config{
		StaticIP:      "10.1.0.5/24",
		StaticGateway: "10.1.0.1",
	}
	if !cfg.IsStaticMode() {
		t.Error("expected static mode when StaticIP is set")
	}
	if cfg.IsFRRMode() {
		t.Error("static-only config should not be FRR mode")
	}
}

func TestNetworkModeDetectionBondLACP(t *testing.T) {
	cfg := &network.Config{
		BondInterfaces: "eth0,eth1",
		BondMode:       "802.3ad",
	}
	if !cfg.IsBondMode() {
		t.Error("expected bond mode when BondInterfaces is set")
	}
	if cfg.IsStaticMode() {
		t.Error("bond-only config should not be static mode")
	}
	if cfg.IsFRRMode() {
		t.Error("bond-only config should not be FRR mode")
	}
}

func TestNetworkModeDetectionBondWithStaticIP(t *testing.T) {
	cfg := &network.Config{
		BondInterfaces: "eth0,eth1",
		BondMode:       "802.3ad",
		StaticIP:       "10.1.0.5/24",
		StaticGateway:  "10.1.0.1",
	}
	if !cfg.IsBondMode() {
		t.Error("expected bond mode")
	}
	if !cfg.IsStaticMode() {
		t.Error("expected static mode with StaticIP on bond")
	}
}

func TestNetworkModeDetectionVLAN(t *testing.T) {
	cfg := &network.Config{
		VLANs: []network.VLANConfig{
			{ID: 200, Parent: "eno1", Address: "10.200.0.42/24"},
		},
	}
	if !cfg.IsVLANMode() {
		t.Error("expected VLAN mode when VLANs configured")
	}
	if cfg.IsFRRMode() {
		t.Error("VLAN-only config should not be FRR mode")
	}
}

func TestNetworkModeDetectionVLANWithFRR(t *testing.T) {
	cfg := &network.Config{
		UnderlaySubnet: "10.0.0.0/24",
		ASN:            65001,
		VLANs: []network.VLANConfig{
			{ID: 200, Parent: "eno1", Address: "10.200.0.42/24"},
		},
	}
	if !cfg.IsFRRMode() {
		t.Error("expected FRR mode with underlay+ASN")
	}
	if !cfg.IsVLANMode() {
		t.Error("expected VLAN mode active alongside FRR")
	}
}

func TestNetworkModeDetectionGoBGP(t *testing.T) {
	cfg := &network.Config{
		NetworkMode:    "gobgp",
		UnderlaySubnet: "10.0.0.0/24",
		ASN:            65001,
	}
	if !cfg.IsGoBGPMode() {
		t.Error("expected GoBGP mode")
	}
	if !cfg.IsFRRMode() {
		t.Error("GoBGP mode with underlay+ASN should also be FRR mode")
	}
}

func TestNetworkModeDetectionGoBGPCaseInsensitive(t *testing.T) {
	for _, mode := range []string{"gobgp", "GoBGP", "GOBGP", "gObGp"} {
		cfg := &network.Config{NetworkMode: mode}
		if !cfg.IsGoBGPMode() {
			t.Errorf("NetworkMode=%q should be GoBGP mode", mode)
		}
	}
}

func TestNetworkModeDetectionDHCPDefault(t *testing.T) {
	cfg := &network.Config{}
	if cfg.IsFRRMode() {
		t.Error("empty config should default to DHCP (not FRR)")
	}
	if cfg.IsStaticMode() {
		t.Error("empty config should not be static mode")
	}
	if cfg.IsBondMode() {
		t.Error("empty config should not be bond mode")
	}
	if cfg.IsVLANMode() {
		t.Error("empty config should not be VLAN mode")
	}
	if cfg.IsGoBGPMode() {
		t.Error("empty config should not be GoBGP mode")
	}
}

func TestNetworkConfigApplyDefaults(t *testing.T) {
	cfg := &network.Config{}
	cfg.ApplyDefaults()
	if cfg.BridgeName != "br.provision" {
		t.Errorf("default BridgeName = %q, want br.provision", cfg.BridgeName)
	}
	if cfg.MTU != 9000 {
		t.Errorf("default MTU = %d, want 9000", cfg.MTU)
	}
	if cfg.VRFTableID != 1 {
		t.Errorf("default VRFTableID = %d, want 1", cfg.VRFTableID)
	}
}

func TestNetworkConfigApplyDefaultsPreservesExisting(t *testing.T) {
	cfg := &network.Config{
		BridgeName: "custom-br",
		MTU:        1500,
		VRFTableID: 42,
	}
	cfg.ApplyDefaults()
	if cfg.BridgeName != "custom-br" {
		t.Errorf("BridgeName overwritten to %q", cfg.BridgeName)
	}
	if cfg.MTU != 1500 {
		t.Errorf("MTU overwritten to %d", cfg.MTU)
	}
	if cfg.VRFTableID != 42 {
		t.Errorf("VRFTableID overwritten to %d", cfg.VRFTableID)
	}
}

func TestParseVLANsSingleEntry(t *testing.T) {
	vlans, err := network.ParseVLANs("200:eno1:10.200.0.42/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(vlans) != 1 {
		t.Fatalf("expected 1 VLAN, got %d", len(vlans))
	}
	if vlans[0].ID != 200 {
		t.Errorf("VLAN ID = %d, want 200", vlans[0].ID)
	}
	if vlans[0].Parent != "eno1" {
		t.Errorf("Parent = %q, want eno1", vlans[0].Parent)
	}
	if vlans[0].Address != "10.200.0.42/24" {
		t.Errorf("Address = %q", vlans[0].Address)
	}
}

func TestParseVLANsMultipleEntries(t *testing.T) {
	vlans, err := network.ParseVLANs("200:eno1:10.200.0.42/24,300:eno2")
	if err != nil {
		t.Fatal(err)
	}
	if len(vlans) != 2 {
		t.Fatalf("expected 2 VLANs, got %d", len(vlans))
	}
	if vlans[1].ID != 300 {
		t.Errorf("second VLAN ID = %d", vlans[1].ID)
	}
	if vlans[1].Address != "" {
		t.Errorf("second VLAN should have empty address, got %q", vlans[1].Address)
	}
}

func TestParseVLANsWithGateway(t *testing.T) {
	vlans, err := network.ParseVLANs("200:eno1:10.200.0.42/24:10.200.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(vlans) != 1 {
		t.Fatalf("expected 1 VLAN, got %d", len(vlans))
	}
	if vlans[0].Gateway != "10.200.0.1" {
		t.Errorf("Gateway = %q, want 10.200.0.1", vlans[0].Gateway)
	}
}

func TestParseVLANsInvalidID(t *testing.T) {
	_, err := network.ParseVLANs("abc:eno1")
	if err == nil {
		t.Fatal("expected error for non-numeric VLAN ID")
	}
}

func TestParseVLANsOutOfRange(t *testing.T) {
	for _, spec := range []string{"0:eno1", "4095:eno1", "9999:eno1"} {
		_, err := network.ParseVLANs(spec)
		if err == nil {
			t.Errorf("expected error for out-of-range VLAN: %s", spec)
		}
	}
}

func TestParseVLANsMissingParent(t *testing.T) {
	_, err := network.ParseVLANs("200")
	if err == nil {
		t.Fatal("expected error for VLAN entry without parent interface")
	}
}

func TestParseVLANsEmpty(t *testing.T) {
	vlans, err := network.ParseVLANs("")
	if err != nil {
		t.Fatal(err)
	}
	if vlans != nil {
		t.Errorf("empty spec should return nil, got %v", vlans)
	}
}

func TestParsePeerModeValues(t *testing.T) {
	tests := []struct {
		input string
		want  network.PeerMode
	}{
		{"unnumbered", network.PeerModeUnnumbered},
		{"dual", network.PeerModeDual},
		{"numbered", network.PeerModeNumbered},
		{"UNNUMBERED", network.PeerModeUnnumbered},
		{"Dual", network.PeerModeDual},
		{"NUMBERED", network.PeerModeNumbered},
		{"", network.PeerModeUnnumbered},
		{"invalid", network.PeerModeUnnumbered},
	}
	for _, tc := range tests {
		got := network.ParsePeerMode(tc.input)
		if got != tc.want {
			t.Errorf("ParsePeerMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestVarsParsingStaticNetworkConfig(t *testing.T) {
	vars := `export MODE="provision"
export HOSTNAME="static-node"
export STATIC_IP="10.1.0.5/24"
export STATIC_GATEWAY="10.1.0.1"
export STATIC_IFACE="eth0"
export IMAGE="http://img.local/test.gz"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StaticIP != "10.1.0.5/24" {
		t.Errorf("StaticIP = %q", cfg.StaticIP)
	}
	if cfg.StaticGateway != "10.1.0.1" {
		t.Errorf("StaticGateway = %q", cfg.StaticGateway)
	}
	if cfg.StaticIface != "eth0" {
		t.Errorf("StaticIface = %q", cfg.StaticIface)
	}
	netCfg := &network.Config{StaticIP: cfg.StaticIP}
	if !netCfg.IsStaticMode() {
		t.Error("should be static mode")
	}
}

func TestVarsParsingBondConfig(t *testing.T) {
	vars := `export MODE="provision"
export HOSTNAME="bond-node"
export BOND_INTERFACES="eth0,eth1"
export BOND_MODE="802.3ad"
export IMAGE="http://img.local/test.gz"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BondInterfaces != "eth0,eth1" {
		t.Errorf("BondInterfaces = %q", cfg.BondInterfaces)
	}
	if cfg.BondMode != "802.3ad" {
		t.Errorf("BondMode = %q", cfg.BondMode)
	}
	netCfg := &network.Config{BondInterfaces: cfg.BondInterfaces}
	if !netCfg.IsBondMode() {
		t.Error("should be bond mode")
	}
}

func TestVarsParsingVLANConfig(t *testing.T) {
	vars := `export MODE="provision"
export HOSTNAME="vlan-node"
export VLANS="200:eno1:10.200.0.42/24,300:eno2"
export IMAGE="http://img.local/test.gz"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VLANs != "200:eno1:10.200.0.42/24,300:eno2" {
		t.Errorf("VLANs = %q", cfg.VLANs)
	}
	vlans, err := network.ParseVLANs(cfg.VLANs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vlans) != 2 {
		t.Fatalf("expected 2 VLANs, got %d", len(vlans))
	}
	netCfg := &network.Config{VLANs: vlans}
	if !netCfg.IsVLANMode() {
		t.Error("should be VLAN mode")
	}
}

func TestVarsParsingBGPPeerMode(t *testing.T) {
	vars := `export MODE="provision"
export HOSTNAME="bgp-node"
export BGP_PEER_MODE="numbered"
export BGP_NEIGHBORS="10.0.0.1,10.0.0.2"
export BGP_REMOTE_ASN="65200"
export IMAGE="http://img.local/test.gz"
underlay_subnet="10.0.0.0/24"
asn_server="65001"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BGPPeerMode != "numbered" {
		t.Errorf("BGPPeerMode = %q", cfg.BGPPeerMode)
	}
	if cfg.BGPNeighbors != "10.0.0.1,10.0.0.2" {
		t.Errorf("BGPNeighbors = %q", cfg.BGPNeighbors)
	}
	if cfg.BGPRemoteASN != 65200 {
		t.Errorf("BGPRemoteASN = %d", cfg.BGPRemoteASN)
	}
	peerMode := network.ParsePeerMode(cfg.BGPPeerMode)
	if peerMode != network.PeerModeNumbered {
		t.Errorf("parsed PeerMode = %q", peerMode)
	}
}

func TestVarsParsingCombinedBondAndStatic(t *testing.T) {
	vars := `export MODE="provision"
export HOSTNAME="combo-node"
export STATIC_IP="10.1.0.5/24"
export STATIC_GATEWAY="10.1.0.1"
export BOND_INTERFACES="eth0,eth1"
export BOND_MODE="802.3ad"
export IMAGE="http://img.local/test.gz"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	netCfg := &network.Config{
		StaticIP:       cfg.StaticIP,
		StaticGateway:  cfg.StaticGateway,
		BondInterfaces: cfg.BondInterfaces,
		BondMode:       cfg.BondMode,
	}
	if !netCfg.IsStaticMode() {
		t.Error("combined config should be static mode")
	}
	if !netCfg.IsBondMode() {
		t.Error("combined config should be bond mode")
	}
	if netCfg.IsFRRMode() {
		t.Error("combined config should not be FRR mode")
	}
}

func TestVarsParsingMultiNICDHCPConfig(t *testing.T) {
	vars := `export MODE="provision"
export HOSTNAME="dhcp-multi-nic"
export IMAGE="http://img.local/test.gz"
dns_resolver="8.8.8.8,1.1.1.1"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	netCfg := &network.Config{DNSResolvers: cfg.DNSResolvers}
	if netCfg.IsFRRMode() {
		t.Error("DHCP config should not be FRR mode")
	}
	if netCfg.IsStaticMode() {
		t.Error("DHCP config should not be static mode")
	}
}

func TestNetworkModeExclusivity(t *testing.T) {
	tests := []struct {
		name   string
		cfg    network.Config
		frr    bool
		static bool
		bond   bool
		vlan   bool
		gobgp  bool
	}{
		{
			name: "dhcp only",
			cfg:  network.Config{},
		},
		{
			name: "frr evpn",
			cfg:  network.Config{UnderlaySubnet: "10.0.0.0/24", ASN: 65001},
			frr:  true,
		},
		{
			name:   "static only",
			cfg:    network.Config{StaticIP: "10.0.0.5/24"},
			static: true,
		},
		{
			name: "bond only",
			cfg:  network.Config{BondInterfaces: "eth0,eth1"},
			bond: true,
		},
		{
			name: "vlan only",
			cfg:  network.Config{VLANs: []network.VLANConfig{{ID: 200, Parent: "eno1"}}},
			vlan: true,
		},
		{
			name:  "gobgp",
			cfg:   network.Config{NetworkMode: "gobgp", UnderlaySubnet: "10.0.0.0/24", ASN: 65001},
			frr:   true,
			gobgp: true,
		},
		{
			name:   "static + bond",
			cfg:    network.Config{StaticIP: "10.0.0.5/24", BondInterfaces: "eth0,eth1"},
			static: true,
			bond:   true,
		},
		{
			name: "frr + vlan",
			cfg:  network.Config{UnderlaySubnet: "10.0.0.0/24", ASN: 65001, VLANs: []network.VLANConfig{{ID: 200, Parent: "eno1"}}},
			frr:  true,
			vlan: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg.IsFRRMode() != tc.frr {
				t.Errorf("IsFRRMode() = %v, want %v", tc.cfg.IsFRRMode(), tc.frr)
			}
			if tc.cfg.IsStaticMode() != tc.static {
				t.Errorf("IsStaticMode() = %v, want %v", tc.cfg.IsStaticMode(), tc.static)
			}
			if tc.cfg.IsBondMode() != tc.bond {
				t.Errorf("IsBondMode() = %v, want %v", tc.cfg.IsBondMode(), tc.bond)
			}
			if tc.cfg.IsVLANMode() != tc.vlan {
				t.Errorf("IsVLANMode() = %v, want %v", tc.cfg.IsVLANMode(), tc.vlan)
			}
			if tc.cfg.IsGoBGPMode() != tc.gobgp {
				t.Errorf("IsGoBGPMode() = %v, want %v", tc.cfg.IsGoBGPMode(), tc.gobgp)
			}
		})
	}
}
