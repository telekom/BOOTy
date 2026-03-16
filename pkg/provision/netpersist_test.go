//go:build linux

package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestDetectOSFamily(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"ubuntu", "ID=ubuntu\nVERSION_ID=\"22.04\"", "ubuntu"},
		{"flatcar", "ID=flatcar\nVERSION_ID=3510.2.1", "flatcar"},
		{"rhel", "ID=\"rhel\"\nVERSION_ID=\"8.8\"", "rhel"},
		{"rocky", "ID=\"rocky\"\nVERSION_ID=\"9.2\"", "rhel"},
		{"centos", "ID=\"centos\"\nVERSION_ID=\"7\"", "rhel"},
		{"unknown", "ID=arch", "ubuntu"},
		{"missing", "", "ubuntu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.content != "" {
				if err := os.MkdirAll(filepath.Join(dir, "etc"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "etc", "os-release"), []byte(tt.content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got := detectOSFamily(dir)
			if got != tt.expected {
				t.Errorf("detectOSFamily() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWriteNetplanStatic(t *testing.T) {
	dir := t.TempDir()
	err := writeNetplan(dir, "eth0", "10.0.1.5/24", "10.0.1.1", "8.8.8.8,8.8.4.4")
	if err != nil {
		t.Fatalf("writeNetplan() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "netplan", "01-booty-provisioned.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read netplan file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "10.0.1.5/24") {
		t.Error("netplan missing static IP address")
	}
	if !strings.Contains(content, "10.0.1.1") {
		t.Error("netplan missing gateway")
	}
	if !strings.Contains(content, "8.8.8.8") {
		t.Error("netplan missing DNS")
	}
}

func TestWriteNetplanDHCP(t *testing.T) {
	dir := t.TempDir()
	err := writeNetplan(dir, "eth0", "", "", "")
	if err != nil {
		t.Fatalf("writeNetplan() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "netplan", "01-booty-provisioned.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read netplan file: %v", err)
	}

	if !strings.Contains(string(data), "dhcp4: true") {
		t.Error("netplan missing dhcp4")
	}
}

func TestWriteSystemdNetworkdStatic(t *testing.T) {
	dir := t.TempDir()
	err := writeSystemdNetworkd(dir, "eth0", "10.0.1.5/24", "10.0.1.1", "8.8.8.8,8.8.4.4")
	if err != nil {
		t.Fatalf("writeSystemdNetworkd() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "systemd", "network", "10-booty.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read systemd-networkd file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Address=10.0.1.5/24") {
		t.Error("networkd missing static address")
	}
	if !strings.Contains(content, "Gateway=10.0.1.1") {
		t.Error("networkd missing gateway")
	}
	if !strings.Contains(content, "DNS=8.8.8.8") {
		t.Error("networkd missing DNS")
	}
}

func TestWriteSystemdNetworkdDHCP(t *testing.T) {
	dir := t.TempDir()
	err := writeSystemdNetworkd(dir, "eth0", "", "", "")
	if err != nil {
		t.Fatalf("writeSystemdNetworkd() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "systemd", "network", "10-booty.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read systemd-networkd file: %v", err)
	}

	if !strings.Contains(string(data), "DHCP=yes") {
		t.Error("networkd missing DHCP")
	}
}

func TestWriteBondNetplan(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.MachineConfig{
		StaticIP:      "10.0.0.5/24",
		StaticGateway: "10.0.0.1",
		DNSResolvers:  "8.8.8.8",
	}
	err := writeBondNetplan(dir, []string{"eth0", "eth1"}, "802.3ad", cfg)
	if err != nil {
		t.Fatalf("writeBondNetplan() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "etc", "netplan", "02-booty-bond.yaml"))
	if err != nil {
		t.Fatalf("read bond netplan: %v", err)
	}
	content := string(data)
	for _, want := range []string{"bond0", "eth0", "eth1", "802.3ad", "gateway4: 10.0.0.1", "8.8.8.8"} {
		if !strings.Contains(content, want) {
			t.Errorf("bond netplan missing %q", want)
		}
	}
}

func TestWriteBondSystemd(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.MachineConfig{
		StaticIP:      "10.0.0.5/24",
		StaticGateway: "10.0.0.1",
		DNSResolvers:  "8.8.8.8, 1.1.1.1",
	}
	err := writeBondSystemd(dir, []string{"eth0", "eth1"}, "802.3ad", cfg)
	if err != nil {
		t.Fatalf("writeBondSystemd() error = %v", err)
	}

	networkDir := filepath.Join(dir, "etc", "systemd", "network")

	// Check bond netdev
	netdev, err := os.ReadFile(filepath.Join(networkDir, "10-bond0.netdev"))
	if err != nil {
		t.Fatalf("read bond netdev: %v", err)
	}
	if !strings.Contains(string(netdev), "Kind=bond") {
		t.Error("bond netdev missing Kind=bond")
	}

	// Check bond network has gateway and DNS
	bondNet, err := os.ReadFile(filepath.Join(networkDir, "10-bond0.network"))
	if err != nil {
		t.Fatalf("read bond network: %v", err)
	}
	content := string(bondNet)
	if !strings.Contains(content, "Gateway=10.0.0.1") {
		t.Error("bond network missing Gateway")
	}
	if !strings.Contains(content, "DNS=8.8.8.8") {
		t.Error("bond network missing DNS")
	}
}

func TestWriteVLANNetplan(t *testing.T) {
	dir := t.TempDir()
	err := writeVLANNetplan(dir, "200", "eno1", "10.200.0.42/24")
	if err != nil {
		t.Fatalf("writeVLANNetplan() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "etc", "netplan", "03-booty-vlan200.yaml"))
	if err != nil {
		t.Fatalf("read VLAN netplan: %v", err)
	}
	content := string(data)
	for _, want := range []string{"vlan200", "id: 200", "link: eno1", "10.200.0.42/24"} {
		if !strings.Contains(content, want) {
			t.Errorf("VLAN netplan missing %q", want)
		}
	}
}

func TestWriteVLANNetplanDHCP(t *testing.T) {
	dir := t.TempDir()
	err := writeVLANNetplan(dir, "300", "eno2", "")
	if err != nil {
		t.Fatalf("writeVLANNetplan() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "etc", "netplan", "03-booty-vlan300.yaml"))
	if err != nil {
		t.Fatalf("read VLAN netplan: %v", err)
	}
	if !strings.Contains(string(data), "dhcp4: true") {
		t.Error("VLAN netplan missing dhcp4 fallback")
	}
}

func TestWriteNetworkManager(t *testing.T) {
	dir := t.TempDir()
	err := writeNetworkManager(dir, "enp3s0", "10.0.0.5/24", "10.0.0.1", "8.8.8.8")
	if err != nil {
		t.Fatalf("writeNetworkManager() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "etc", "NetworkManager", "system-connections", "booty-enp3s0.nmconnection"))
	if err != nil {
		t.Fatalf("read NM config: %v", err)
	}
	content := string(data)
	for _, want := range []string{"[connection]", "[ipv4]", "enp3s0", "10.0.0.5/24"} {
		if !strings.Contains(content, want) {
			t.Errorf("NM config missing %q", want)
		}
	}
}

func TestValidateNetName(t *testing.T) {
	valid := []string{"eth0", "eno1", "bond0", "vlan.200", "enp3s0f0"}
	for _, name := range valid {
		if err := validateNetName(name); err != nil {
			t.Errorf("validateNetName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{"", "../etc", "eth0/../../passwd", "a b", "eth\x00"}
	for _, name := range invalid {
		if err := validateNetName(name); err == nil {
			t.Errorf("validateNetName(%q) = nil, want error", name)
		}
	}
}

func TestParseGateway(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"10.0.0.1", "10.0.0.1"},
		{"10.0.1.1/24", "10.0.1.1"},
		{"", ""},
		{"::1", "::1"},
	}
	for _, tc := range tests {
		ip := parseGateway(tc.input)
		got := ""
		if ip != nil {
			got = ip.String()
		}
		if got != tc.want {
			t.Errorf("parseGateway(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
