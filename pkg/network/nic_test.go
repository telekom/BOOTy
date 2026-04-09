//go:build linux

package network

import (
	"net"
	"testing"
)

func mockAddr(ip string) []net.Addr {
	_, ipNet, _ := net.ParseCIDR(ip)
	return []net.Addr{ipNet}
}

func makeIface(name string, mac string, flags net.Flags) net.Interface {
	hw, _ := net.ParseMAC(mac)
	return net.Interface{
		Name:         name,
		HardwareAddr: hw,
		Flags:        flags,
	}
}

func addrFor(addrs map[string][]net.Addr) func(net.Interface) ([]net.Addr, error) {
	return func(i net.Interface) ([]net.Addr, error) {
		return addrs[i.Name], nil
	}
}

func TestSelectIPMIInterface_BMCNameWins(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
		makeIface("ipmi0", "aa:bb:cc:dd:ee:02", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0":  mockAddr("192.168.1.1/24"),
		"ipmi0": mockAddr("192.168.2.1/24"),
	}

	got, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "ipmi0" {
		t.Errorf("got interface %q, want %q", got.Name, "ipmi0")
	}
}

func TestSelectIPMIInterface_BMCBeatsEth(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
		makeIface("bmc0", "aa:bb:cc:dd:ee:02", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0": mockAddr("10.0.0.1/24"),
		"bmc0": mockAddr("10.0.0.2/24"),
	}

	got, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "bmc0" {
		t.Errorf("got interface %q, want %q", got.Name, "bmc0")
	}
}

func TestSelectIPMIInterface_FallbackToFirstWithWarning(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0": mockAddr("10.0.0.1/24"),
	}

	got, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "eth0" {
		t.Errorf("got interface %q, want %q", got.Name, "eth0")
	}
}

func TestSelectIPMIInterface_NoAddressesReturnsError(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{}

	_, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSelectIPMIInterface_MgmtBeatsEth(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
		makeIface("mgmt0", "aa:bb:cc:dd:ee:02", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0":  mockAddr("10.0.0.1/24"),
		"mgmt0": mockAddr("10.0.0.2/24"),
	}

	got, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "mgmt0" {
		t.Errorf("got interface %q, want %q", got.Name, "mgmt0")
	}
}

func TestSelectIPMIInterface_CaseInsensitive(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("IPMI0", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"IPMI0": mockAddr("192.168.0.1/24"),
	}

	got, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "IPMI0" {
		t.Errorf("got interface %q, want %q", got.Name, "IPMI0")
	}
}

func TestSelectIPMIInterface_LoopbackExcluded(t *testing.T) {
	ifaces := []net.Interface{
		makeIface("lo", "00:00:00:00:00:00", net.FlagLoopback),
		makeIface("ipmi0", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"lo":    mockAddr("127.0.0.1/8"),
		"ipmi0": mockAddr("192.168.1.1/24"),
	}

	got, err := selectIPMIInterfaceWith(ifaces, addrFor(addrs))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "ipmi0" {
		t.Errorf("got interface %q, want %q", got.Name, "ipmi0")
	}
}

func TestFilterAddressed_NoMACInterface(t *testing.T) {
	ifaces := []net.Interface{
		{Name: "eth0", HardwareAddr: nil, Flags: net.FlagUp},
		makeIface("eth1", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0": mockAddr("10.0.0.1/24"),
		"eth1": mockAddr("10.0.0.2/24"),
	}

	got := filterAddressed(ifaces, addrFor(addrs))
	if len(got) != 1 || got[0].Name != "eth1" {
		t.Errorf("got %v, want only eth1", got)
	}
}

func TestFilterAddressed_ValidIPInLaterEntry(t *testing.T) {
	_, linkLocal, _ := net.ParseCIDR("169.254.0.1/16")
	_, validIP, _ := net.ParseCIDR("10.0.0.1/24")
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0": {linkLocal, validIP},
	}

	got := filterAddressed(ifaces, addrFor(addrs))
	if len(got) != 1 || got[0].Name != "eth0" {
		t.Errorf("got %v, want eth0 (valid IP in later addr entry)", got)
	}
}

func TestFilterAddressed_NoValidIPExcluded(t *testing.T) {
	_, linkLocal, _ := net.ParseCIDR("169.254.1.1/16")
	_, loopback, _ := net.ParseCIDR("127.0.0.1/8")
	ifaces := []net.Interface{
		makeIface("eth0", "aa:bb:cc:dd:ee:01", net.FlagUp),
	}
	addrs := map[string][]net.Addr{
		"eth0": {linkLocal, loopback},
	}

	got := filterAddressed(ifaces, addrFor(addrs))
	if len(got) != 0 {
		t.Errorf("got %v, want empty (all addrs are link-local or loopback)", got)
	}
}
