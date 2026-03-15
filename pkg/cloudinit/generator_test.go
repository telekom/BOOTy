package cloudinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate_Basic(t *testing.T) {
	cfg := &Config{
		Hostname: "worker-001",
		FQDN:     "worker-001.example.com",
		Serial:   "SN123456",
		SSHKeys:  []string{"ssh-ed25519 AAAA..."},
		Timezone: "UTC",
	}

	ud, md, nc := Generate(cfg)

	if ud.Hostname != "worker-001" {
		t.Errorf("hostname = %q, want %q", ud.Hostname, "worker-001")
	}
	if !ud.ManageEtcHosts {
		t.Error("ManageEtcHosts should be true")
	}
	if md.InstanceID != "SN123456" {
		t.Errorf("instance-id = %q, want %q", md.InstanceID, "SN123456")
	}
	if md.Platform != "booty" {
		t.Errorf("platform = %q, want %q", md.Platform, "booty")
	}
	if nc.Version != 2 {
		t.Errorf("network version = %d, want 2", nc.Version)
	}
}

func TestGenerate_StaticIP(t *testing.T) {
	cfg := &Config{
		Hostname: "node-1",
		Serial:   "SN1",
		StaticIP: "10.0.0.5/24",
		Gateway:  "10.0.0.1",
		DNS:      []string{"8.8.8.8", "8.8.4.4"},
	}

	_, _, nc := Generate(cfg)

	eth, ok := nc.Ethernets["id0"]
	if !ok {
		t.Fatal("expected ethernets[id0]")
	}
	if eth.DHCP4 {
		t.Error("DHCP4 should be false for static IP")
	}
	if len(eth.Addresses) != 1 || eth.Addresses[0] != "10.0.0.5/24" {
		t.Errorf("addresses = %v, want [10.0.0.5/24]", eth.Addresses)
	}
}

func TestGenerate_DHCP(t *testing.T) {
	cfg := &Config{
		Hostname: "dhcp-node",
		Serial:   "SN2",
	}

	_, _, nc := Generate(cfg)

	eth, ok := nc.Ethernets["id0"]
	if !ok {
		t.Fatal("expected ethernets[id0]")
	}
	if !eth.DHCP4 {
		t.Error("DHCP4 should be true when no static IP")
	}
}

func TestGenerate_Bond(t *testing.T) {
	cfg := &Config{
		Hostname:   "bond-node",
		Serial:     "SN3",
		BondIfaces: []string{"eth0", "eth1"},
		BondMode:   "802.3ad",
		StaticIP:   "10.0.0.10/24",
	}

	_, _, nc := Generate(cfg)

	if len(nc.Ethernets) != 0 {
		t.Error("ethernets should be empty when using bonds")
	}
	bond, ok := nc.Bonds["bond0"]
	if !ok {
		t.Fatal("expected bonds[bond0]")
	}
	if len(bond.Interfaces) != 2 {
		t.Errorf("bond interfaces = %d, want 2", len(bond.Interfaces))
	}
	if bond.Parameters.Mode != "802.3ad" {
		t.Errorf("bond mode = %q, want %q", bond.Parameters.Mode, "802.3ad")
	}
}

func TestGenerate_WithUsers(t *testing.T) {
	cfg := &Config{
		Hostname: "user-node",
		Serial:   "SN4",
		Users: []User{
			{Name: "admin", Groups: "sudo", Shell: "/bin/bash", Sudo: "ALL=(ALL) NOPASSWD:ALL"},
		},
	}

	ud, _, _ := Generate(cfg)

	if len(ud.Users) != 1 {
		t.Fatalf("users count = %d, want 1", len(ud.Users))
	}
	if ud.Users[0].Name != "admin" {
		t.Errorf("user name = %q, want %q", ud.Users[0].Name, "admin")
	}
}

func TestUserData_Render(t *testing.T) {
	ud := &UserData{
		Hostname: "test-host",
		Timezone: "Europe/Berlin",
	}

	data, err := ud.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	s := string(data)
	if !strings.HasPrefix(s, "#cloud-config\n") {
		t.Error("user-data should start with #cloud-config header")
	}
	if !strings.Contains(s, "hostname: test-host") {
		t.Error("user-data should contain hostname")
	}
}

func TestMetaData_Render(t *testing.T) {
	md := &MetaData{
		InstanceID:    "i-12345",
		LocalHostname: "test-host",
		Platform:      "booty",
	}

	data, err := md.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, "instance-id: i-12345") {
		t.Error("meta-data should contain instance-id")
	}
}

func TestNetworkConfig_Render(t *testing.T) {
	nc := &NetworkConfig{
		Version: 2,
		Ethernets: map[string]EthConfig{
			"eth0": {DHCP4: true},
		},
	}

	data, err := nc.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, "version: 2") {
		t.Error("network-config should contain version: 2")
	}
}

func TestInjectNoCloud(t *testing.T) {
	root := t.TempDir()

	ud := &UserData{Hostname: "inject-test"}
	md := &MetaData{InstanceID: "test-id", LocalHostname: "inject-test", Platform: "booty"}
	nc := &NetworkConfig{Version: 2, Ethernets: map[string]EthConfig{"eth0": {DHCP4: true}}}

	if err := InjectNoCloud(root, ud, md, nc); err != nil {
		t.Fatalf("InjectNoCloud: %v", err)
	}

	seedDir := filepath.Join(root, "var", "lib", "cloud", "seed", "nocloud")
	for _, name := range []string{"user-data", "meta-data", "network-config"} {
		data, err := os.ReadFile(filepath.Join(seedDir, name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	udData, _ := os.ReadFile(filepath.Join(seedDir, "user-data"))
	if !strings.HasPrefix(string(udData), "#cloud-config\n") {
		t.Error("user-data should start with #cloud-config")
	}
}

func TestAddressList(t *testing.T) {
	if got := addressList(""); got != nil {
		t.Errorf("addressList empty = %v, want nil", got)
	}
	got := addressList("10.0.0.1/24")
	if len(got) != 1 || got[0] != "10.0.0.1/24" {
		t.Errorf("addressList ip = %v", got)
	}
}
