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
	if Ubuntu.ConfigPath() != "etc/netplan" {
		t.Error("ubuntu path")
	}
	if RHEL.ConfigPath() != "etc/NetworkManager/system-connections" {
		t.Error("rhel path")
	}
	if Flatcar.ConfigPath() != "etc/systemd/network" {
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
		{"dhcp and static address conflict", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true, Address: "10.0.0.5/24"}},
		}, true},
		{"invalid interface mac", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true, MAC: "bad-mac"}},
		}, true},
		{"invalid interface gateway", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", Address: "10.0.0.5/24", Gateway: "bad-gw"}},
		}, true},
		{"invalid interface address", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", Address: "10.0.0.5"}},
		}, true},
		{"duplicate interface names", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}, {Name: "eth0", DHCP: true}},
		}, true},
		{"valid bond", NetworkConfig{
			Bonds: []BondConfig{{Name: "bond0", Members: []string{"eth0", "eth1"}, Mode: "802.3ad"}},
		}, false},
		{"invalid bond address", NetworkConfig{
			Bonds: []BondConfig{{Name: "bond0", Members: []string{"eth0", "eth1"}, Mode: "802.3ad", Address: "10.0.0.1"}},
		}, true},
		{"bond no name", NetworkConfig{
			Bonds: []BondConfig{{Members: []string{"eth0", "eth1"}}},
		}, true},
		{"bond 1 member", NetworkConfig{
			Bonds: []BondConfig{{Name: "bond0", Members: []string{"eth0"}, Mode: "802.3ad"}},
		}, true},
		{"bond no mode", NetworkConfig{
			Bonds: []BondConfig{{Name: "bond0", Members: []string{"eth0", "eth1"}}},
		}, true},
		{"invalid iface name", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "../../etc", DHCP: true}},
		}, true},
		{"invalid bond name", NetworkConfig{
			Bonds: []BondConfig{{Name: "../bad", Members: []string{"eth0", "eth1"}, Mode: "802.3ad"}},
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
		{"vlan dhcp and static conflict", NetworkConfig{
			VLANs: []VLANConfig{{Parent: "eth0", ID: 100, DHCP: true, Address: "10.0.0.5/24"}},
		}, true},
		{"vlan invalid address", NetworkConfig{
			VLANs: []VLANConfig{{Parent: "eth0", ID: 100, Address: "10.0.0.5"}},
		}, true},
		{"valid route", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			Routes:     []RouteConfig{{Destination: "10.0.0.0/8", Gateway: "10.0.0.1"}},
		}, false},
		{"invalid route destination", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			Routes:     []RouteConfig{{Destination: "10.0.0.0", Gateway: "10.0.0.1"}},
		}, true},
		{"invalid route gateway", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			Routes:     []RouteConfig{{Destination: "10.0.0.0/8", Gateway: "bad-gw"}},
		}, true},
		{"route no dest", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			Routes:     []RouteConfig{{Gateway: "10.0.0.1"}},
		}, true},
		{"route no gw", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			Routes:     []RouteConfig{{Destination: "10.0.0.0/8"}},
		}, true},
		{"invalid dns server", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			DNS:        DNSConfig{Servers: []string{"bad-dns"}},
		}, true},
		{"invalid dns search", NetworkConfig{
			Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
			DNS:        DNSConfig{Search: []string{"bad domain"}},
		}, true},
		{"dns without any interface", NetworkConfig{
			DNS: DNSConfig{Servers: []string{"8.8.8.8"}},
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
	if !strings.Contains(result, "renderer: networkd") {
		t.Error("missing renderer")
	}
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
	if !strings.Contains(result, "renderer: networkd") {
		t.Error("missing renderer")
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
	path := filepath.Join(root, "etc/netplan/01-booty-provisioned.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
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

func TestWriteNilConfig(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, Ubuntu, nil); err == nil {
		t.Error("expected nil config error")
	}
}

func TestWriteUnsupportedFamily(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{{Name: "eth0", DHCP: true}},
	}
	if err := Write(root, OSFamily("bsd"), cfg); err == nil {
		t.Error("expected unsupported family error")
	}
}

func TestRenderNetplan_DNSAndRoutes(t *testing.T) {
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "eth0", DHCP: true},
		},
		DNS: DNSConfig{
			Servers: []string{"8.8.8.8", "8.8.4.4"},
			Search:  []string{"example.com"},
		},
		Routes: []RouteConfig{
			{Destination: "10.0.0.0/8", Gateway: "10.0.0.1", Metric: 100},
			{Destination: "172.16.0.0/12", Gateway: "10.0.0.1"},
		},
	}
	result := RenderNetplan(cfg)
	// DNS and routes must appear under the interface stanza, not at root.
	ethIdx := strings.Index(result, "    eth0:")
	if ethIdx < 0 {
		t.Fatal("missing eth0 stanza")
	}
	ifaceSection := result[ethIdx:]
	if !strings.Contains(ifaceSection, "nameservers:") {
		t.Error("nameservers not under interface")
	}
	if !strings.Contains(ifaceSection, "addresses: [8.8.8.8, 8.8.4.4]") {
		t.Error("missing dns addresses under interface")
	}
	if !strings.Contains(ifaceSection, "search: [example.com]") {
		t.Error("missing dns search under interface")
	}
	if !strings.Contains(ifaceSection, "routes:") {
		t.Error("routes not under interface")
	}
	if !strings.Contains(ifaceSection, "to: 10.0.0.0/8") {
		t.Error("missing route destination")
	}
	if !strings.Contains(ifaceSection, "via: 10.0.0.1") {
		t.Error("missing route gateway")
	}
	if !strings.Contains(ifaceSection, "metric: 100") {
		t.Error("missing route metric")
	}
	if strings.Contains(result, "\n  nameservers:") {
		t.Error("nameservers rendered at root level")
	}
	if strings.Contains(result, "\n  routes:") {
		t.Error("routes rendered at root level")
	}
}

func TestRenderNetplan_BondAllFields(t *testing.T) {
	cfg := &NetworkConfig{
		Bonds: []BondConfig{
			{
				Name:       "bond0",
				Members:    []string{"eth0", "eth1"},
				Mode:       "802.3ad",
				Address:    "10.0.0.1/24",
				Gateway:    "10.0.0.254",
				MTU:        9000,
				LACPRate:   "fast",
				HashPolicy: "layer3+4",
			},
		},
	}
	result := RenderNetplan(cfg)
	for _, want := range []string{
		"gateway4: 10.0.0.254",
		"mtu: 9000",
		"lacp-rate: fast",
		"transmit-hash-policy: layer3+4",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in netplan bond output", want)
		}
	}
}

func TestWriteNetworkd_DNSAndRoutes(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "eth0", Address: "10.0.0.5/24", Gateway: "10.0.0.1"},
		},
		DNS: DNSConfig{Servers: []string{"8.8.8.8"}},
		Routes: []RouteConfig{
			{Destination: "172.16.0.0/12", Gateway: "10.0.0.1", Metric: 50},
		},
	}
	if err := Write(root, Flatcar, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	path := filepath.Join(root, "etc/systemd/network/10-booty-eth0.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "DNS=8.8.8.8") {
		t.Error("missing DNS")
	}
	if !strings.Contains(content, "[Route]") {
		t.Error("missing Route section")
	}
	if !strings.Contains(content, "Destination=172.16.0.0/12") {
		t.Error("missing route destination")
	}
	if !strings.Contains(content, "Metric=50") {
		t.Error("missing route metric")
	}
}

func TestWriteNetworkd_SearchOnly(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{{Name: "eth0", Address: "10.0.0.5/24", Gateway: "10.0.0.1"}},
		DNS:        DNSConfig{Search: []string{"example.com"}},
	}
	if err := Write(root, Flatcar, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	path := filepath.Join(root, "etc/systemd/network/10-booty-eth0.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if !strings.Contains(string(data), "Domains=example.com") {
		t.Error("missing search domain in networkd output")
	}
}

func TestWriteNetworkd_GlobalRoutesOnlyPrimary(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "eth0", Address: "10.0.0.5/24", Gateway: "10.0.0.1"},
			{Name: "eth1", Address: "10.0.1.5/24", Gateway: "10.0.1.1"},
		},
		DNS:    DNSConfig{Servers: []string{"8.8.8.8"}},
		Routes: []RouteConfig{{Destination: "172.16.0.0/12", Gateway: "10.0.0.1"}},
	}
	if err := Write(root, Flatcar, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	firstPath := filepath.Join(root, "etc/systemd/network/10-booty-eth0.network")
	secondPath := filepath.Join(root, "etc/systemd/network/10-booty-eth1.network")
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("ReadFile(first) error: %v", err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("ReadFile(second) error: %v", err)
	}
	if !strings.Contains(string(first), "[Route]") {
		t.Error("expected route in primary interface networkd file")
	}
	if strings.Contains(string(second), "[Route]") {
		t.Error("did not expect duplicated route in secondary interface networkd file")
	}
}

func TestWriteNetworkd_MTUAndDNS(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "eth0", Address: "10.0.0.5/24", Gateway: "10.0.0.1", MTU: 9000},
		},
		DNS: DNSConfig{
			Servers: []string{"8.8.8.8"},
			Search:  []string{"example.com"},
		},
	}
	if err := Write(root, Flatcar, cfg); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	path := filepath.Join(root, "etc/systemd/network/10-booty-eth0.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	content := string(data)
	// DNS must appear under [Network], not after [Link].
	linkIdx := strings.Index(content, "[Link]")
	dnsIdx := strings.Index(content, "DNS=8.8.8.8")
	domainsIdx := strings.Index(content, "Domains=example.com")
	if linkIdx < 0 {
		t.Fatal("missing [Link] section")
	}
	if dnsIdx < 0 {
		t.Fatal("missing DNS entry")
	}
	if domainsIdx < 0 {
		t.Fatal("missing Domains entry")
	}
	if dnsIdx > linkIdx {
		t.Error("DNS appears after [Link] — should be under [Network]")
	}
	if domainsIdx > linkIdx {
		t.Error("Domains appears after [Link] — should be under [Network]")
	}
}

func TestWriteNMKeyfiles_DNSAndRoutes(t *testing.T) {
	root := t.TempDir()
	cfg := &NetworkConfig{
		Interfaces: []InterfaceConfig{
			{Name: "enp1s0", Address: "10.0.0.5/24", Gateway: "10.0.0.1"},
		},
		DNS: DNSConfig{
			Servers: []string{"8.8.8.8", "8.8.4.4"},
			Search:  []string{"example.com"},
		},
		Routes: []RouteConfig{
			{Destination: "172.16.0.0/12", Gateway: "10.0.0.1"},
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
	if !strings.Contains(content, "dns=8.8.8.8;8.8.4.4") {
		t.Error("missing dns servers")
	}
	if !strings.Contains(content, "dns-search=example.com") {
		t.Error("missing dns search")
	}
	if !strings.Contains(content, "route1=172.16.0.0/12,10.0.0.1") {
		t.Error("missing route")
	}
}
