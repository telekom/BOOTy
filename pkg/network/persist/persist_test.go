package persist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOSFamily(t *testing.T) {
	tests := []struct {
		input   string
		want    OSFamily
		wantErr bool
	}{
		{"ubuntu", Ubuntu, false},
		{"Ubuntu", Ubuntu, false},
		{"rhel", RHEL, false},
		{"RHEL", RHEL, false},
		{"flatcar", Flatcar, false},
		{"windows", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseOSFamily(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseOSFamily(%q) err = %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOSFamily_ConfigPath(t *testing.T) {
	if Ubuntu.ConfigPath() != "/etc/netplan" {
		t.Error("ubuntu path")
	}
	if RHEL.ConfigPath() != "/etc/NetworkManager/system-connections" {
		t.Error("rhel path")
	}
	if Flatcar.ConfigPath() != "/etc/systemd/network" {
		t.Error("flatcar path")
	}
	if OSFamily("x").ConfigPath() != "" {
		t.Error("unknown path")
	}
}

func TestNetworkConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     NetworkConfig
		wantErr bool
	}{
		{"empty is valid", NetworkConfig{}, false},
		{"valid interface", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
		}, false},
		{"static addr", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", Address: "10.0.0.1/24"}},
		}, false},
		{"no name", NetworkConfig{
			Interfaces: []InterfaceConfig{{DHCP: true}},
		}, true},
		{"no addr no dhcp", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0"}},
		}, true},
		{"valid bond", NetworkConfig{
			Bonds: []BondConfig{{Name: "bond0", Members: []string{"eth0", "eth1"}, Mode: "802.3ad"}},
		}, false},
		{"bond no name", NetworkConfig{
			Bonds: []BondConfig{{Members: []string{"eth0", "eth1"}}},
		}, true},
		{"bond 1 member", NetworkConfig{
			Bonds: []BondConfig{{Name: "bond0", Members: []string{"eth0"}}},
		}, true},
		{"valid vlan", NetworkConfig{
			VLANs: []VLANConfig{{Parent: "eth0", ID: 100}},
		}, false},
		{"vlan no parent", NetworkConfig{
			VLANs: []VLANConfig{{ID: 100}},
		}, true},
		{"vlan bad id", NetworkConfig{
			VLANs: []VLANConfig{{Parent: "eth0", ID: 5000}},
		}, true},
		{"valid route", NetworkConfig{
			Routes: []RouteConfig{{Destination: "10.0.0.0/8", Gateway: "10.0.0.1"}},
		}, false},
		{"route no dest", NetworkConfig{
			Routes: []RouteConfig{{Gateway: "10.0.0.1"}},
		}, true},
		{"route no gw", NetworkConfig{
			Routes: []RouteConfig{{Destination: "10.0.0.0/8"}},
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRenderNetplan(t *testing.T) {
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "eth0", DHCP: true},
			{Name: "eth1", Address: "10.0.0.5/24", Gateway: "10.0.0.1", MTU: 9000, MAC: "aa:bb:cc:dd:ee:ff"},
		},
		Bonds: []BondConfig{
			{Name: "bond0", Members: []string{"eth2", "eth3"}, Mode: "802.3ad", Address: "10.1.0.1/24", LACPRate: "fast"},
		},
		VLANs: []VLANConfig{
			{Parent: "eth0", ID: 100, DHCP: true},
		},
	}

	result := RenderNetplan(cfg)
	if !strings.Contains(result, "ethernets:") {
		t.Error("missing ethernets")
	}
	if !strings.Contains(result, "dhcp4: true") {
		t.Error("missing dhcp4")
	}
	if !strings.Contains(result, "addresses: [10.0.0.5/24]") {
		t.Error("missing addresses")
	}
	if !strings.Contains(result, "mtu: 9000") {
		t.Error("missing mtu")
	}
	if !strings.Contains(result, "bonds:") {
		t.Error("missing bonds")
	}
	if !strings.Contains(result, "vlans:") {
		t.Error("missing vlans")
	}
	if !strings.Contains(result, "id: 100") {
		t.Error("missing vlan id")
	}
}

func TestRenderNetplan_Empty(t *testing.T) {
	cfg := &NetworkConfig{}
	result := RenderNetplan(cfg)
	if !strings.Contains(result, "version: 2") {
		t.Error("missing version")
	}
}

func TestRenderNetworkdUnit(t *testing.T) {
	iface := &InterfaceConfig{
		Name:    "eth0",
		Address: "10.0.0.5/24",
		Gateway: "10.0.0.1",
		MTU:     9000,
	}
	result := RenderNetworkdUnit(iface)
	if !strings.Contains(result, "Name=eth0") {
		t.Error("missing Name")
	}
	if !strings.Contains(result, "Address=10.0.0.5/24") {
		t.Error("missing Address")
	}
	if !strings.Contains(result, "Gateway=10.0.0.1") {
		t.Error("missing Gateway")
	}
	if !strings.Contains(result, "MTUBytes=9000") {
		t.Error("missing MTU")
	}
}

func TestRenderNetworkdUnit_DHCP(t *testing.T) {
	iface := &InterfaceConfig{Name: "eth0", DHCP: true}
	result := RenderNetworkdUnit(iface)
	if !strings.Contains(result, "DHCP=yes") {
		t.Error("missing DHCP")
	}
}

func TestRenderNetworkdUnit_MAC(t *testing.T) {
	iface := &InterfaceConfig{Name: "eth0", MAC: "aa:bb:cc:dd:ee:ff", DHCP: true}
	result := RenderNetworkdUnit(iface)
	if !strings.Contains(result, "MACAddress=aa:bb:cc:dd:ee:ff") {
		t.Error("missing MAC")
	}
}

func TestOSFamilyConstants(t *testing.T) {
	if string(Ubuntu) != "ubuntu" {
		t.Error("Ubuntu")
	}
	if string(RHEL) != "rhel" {
		t.Error("RHEL")
	}
	if string(Flatcar) != "flatcar" {
		t.Error("Flatcar")
	}
}
func TestWriteNetplan(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "eth0", DHCP: true},
			{Name: "eth1", Address: "10.0.0.5/24", Gateway: "10.0.0.1"},
		},
	}
	if err := Write(root, Ubuntu, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	path := filepath.Join(root, "etc/netplan/99-booty.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "eth0:") {
		t.Error("missing eth0")
	}
	if !strings.Contains(content, "dhcp4: true") {
		t.Error("missing dhcp4")
	}
	if !strings.Contains(content, "10.0.0.5/24") {
		t.Error("missing address")
	}
}

func TestWriteNetworkd(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "ens3", Address: "192.168.1.10/24", Gateway: "192.168.1.1"},
		},
	}
	if err := Write(root, Flatcar, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	path := filepath.Join(root, "etc/systemd/network/10-booty-ens3.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Name=ens3") {
		t.Error("missing Name match")
	}
	if !strings.Contains(content, "Address=192.168.1.10/24") {
		t.Error("missing address")
	}
}

func TestWriteNMKeyfiles(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "enp1s0", DHCP: true, MAC: "aa:bb:cc:dd:ee:ff"},
		},
	}
	if err := Write(root, RHEL, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	path := filepath.Join(root, "etc/NetworkManager/system-connections/booty-enp1s0.nmconnection")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "type=ethernet") {
		t.Error("missing type")
	}
	if !strings.Contains(content, "method=auto") {
		t.Error("missing dhcp method")
	}
	if !strings.Contains(content, "mac-address=aa:bb:cc:dd:ee:ff") {
		t.Error("missing mac")
	}
}

func TestWriteValidationError(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: ""}, // missing name
		},
	}
	if err := Write(root, Ubuntu, cfg); err == nil {
		t.Error("expected validation error")
	}
}
