//go:build linux

package lldp

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// The frame carries what systemd-networkd sends with EmitLLDP=yes
// (sd-lldp-tx.c): nearest-bridge destination, the machine ID as locally
// assigned chassis ID, the interface name as port ID, TTL 120, the
// hostname, the pretty hostname and the capabilities.
func TestFrameLikeNetworkd(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	b, err := Frame("ens1f0np0", mac, Info{MachineID: "0123456789abcdef0123456789abcdef", SystemName: "host1", PrettyHostname: "Host 1"})
	if err != nil {
		t.Fatal(err)
	}
	pkt := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	eth, _ := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	l, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	info, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if eth == nil || l == nil || info == nil {
		t.Fatalf("frame does not decode: %v", pkt.ErrorLayer())
	}
	if eth.DstMAC.String() != "01:80:c2:00:00:0e" || eth.SrcMAC.String() != mac.String() {
		t.Errorf("ethernet %s -> %s", eth.SrcMAC, eth.DstMAC)
	}
	if l.ChassisID.Subtype != layers.LLDPChassisIDSubTypeLocal || string(l.ChassisID.ID) != "0123456789abcdef0123456789abcdef" {
		t.Errorf("chassis = %v %q", l.ChassisID.Subtype, l.ChassisID.ID)
	}
	if l.PortID.Subtype != layers.LLDPPortIDSubtypeIfaceName || string(l.PortID.ID) != "ens1f0np0" || l.TTL != 120 {
		t.Errorf("port = %v %q, TTL %d", l.PortID.Subtype, l.PortID.ID, l.TTL)
	}
	if info.SysName != "host1" || info.SysDescription != "Host 1" {
		t.Errorf("system name %q description %q", info.SysName, info.SysDescription)
	}
	var caps []byte
	for _, v := range l.Values {
		if v.Type == layers.LLDPTLVSysCapabilities {
			caps = v.Value
		}
	}
	if len(caps) != 4 || binary.BigEndian.Uint16(caps) != 0x94 || binary.BigEndian.Uint16(caps[2:]) != 0x80 {
		t.Errorf("capabilities = %x, want supported 0094 enabled 0080", caps)
	}

	// Without a machine ID the NIC MAC is the chassis ID; without a pretty
	// hostname there is no system description.
	b, err = Frame("eth0", mac, Info{SystemName: "host1"})
	if err != nil {
		t.Fatal(err)
	}
	pkt = gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	l, _ = pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	info, _ = pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if l.ChassisID.Subtype != layers.LLDPChassisIDSubTypeMACAddr || net.HardwareAddr(l.ChassisID.ID).String() != mac.String() || info.SysDescription != "" {
		t.Errorf("fallback chassis %v %x, description %q", l.ChassisID.Subtype, l.ChassisID.ID, info.SysDescription)
	}
	// networkd sends no system description TLV at all without a pretty
	// hostname, not an empty one.
	for _, v := range l.Values {
		if v.Type == layers.LLDPTLVSysDescription {
			t.Errorf("fallback frame has a system description TLV %q", v.Value)
		}
	}
}
