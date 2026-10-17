//go:build linux

package lldp

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// The fixtures are LLDPDUs built TLV by TLV from IEEE 802.1AB-2016 (clause
// 8 and annexes E, F) and ANSI/TIA-1057 (LLDP-MED), with the TLV sets that
// the switches send by default: NX-OS (ifname port ID and description,
// IPv4 management address, port VLAN, MAC/PHY, max frame size), Junos (the
// SNMP ifIndex as "local" port ID, the port name as description, IPv4 and
// IPv6 management addresses, link aggregation) and lldpd (MAC port ID,
// interface name as description, LLDP-MED inventory).

// rawTLV is one TLV: 7 bits type, 9 bits length, value.
func rawTLV(typ byte, value ...byte) []byte {
	h := uint16(typ)<<9 | uint16(len(value))
	return append(binary.BigEndian.AppendUint16(nil, h), value...)
}

// org is an organizationally specific TLV (type 127).
func org(oui [3]byte, subtype byte, data ...byte) []byte {
	return rawTLV(127, append([]byte{oui[0], oui[1], oui[2], subtype}, data...)...)
}

var (
	oui8021 = [3]byte{0x00, 0x80, 0xc2}
	oui8023 = [3]byte{0x00, 0x12, 0x0f}
	ouiMED  = [3]byte{0x00, 0x12, 0xbb}
)

func cat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

func str(s string) []byte { return []byte(s) }

// mgmt is a management address TLV with an ifIndex and no OID.
func mgmt(family byte, addr ...byte) []byte {
	v := append([]byte{byte(1 + len(addr)), family}, addr...)
	v = append(v, 2, 0, 0, 0, 1, 0) // ifIndex subtype, interface 1, OID length 0
	return rawTLV(8, v...)
}

var (
	nxosFrame = cat(
		rawTLV(1, append([]byte{4}, 0x00, 0x3a, 0x9c, 0x5a, 0x10, 0x01)...), // chassis: MAC
		rawTLV(2, append([]byte{5}, str("Ethernet1/12")...)...),             // port: ifname
		rawTLV(3, 0, 120),
		rawTLV(4, str("Ethernet1/12")...),
		rawTLV(5, str("leaf01")...),
		rawTLV(6, str("Cisco Nexus Operating System (NX-OS) Software 10.3(4a)")...),
		rawTLV(7, 0x00, 0x14, 0x00, 0x14), // bridge, router
		mgmt(1, 192, 0, 2, 1),
		org(oui8021, 1, 0x00, 0x01),                   // port VLAN ID 1
		org(oui8023, 1, 0x03, 0x00, 0x00, 0x00, 0x1e), // autoneg supported+enabled, MAU 30
		org(oui8023, 4, 0x24, 0x00),                   // max frame size 9216
		rawTLV(0),
	)
	junosFrame = cat(
		rawTLV(1, append([]byte{4}, 0x40, 0xde, 0xad, 0x00, 0x00, 0x01)...),
		rawTLV(2, append([]byte{7}, str("513")...)...), // port: local, SNMP ifIndex
		rawTLV(3, 0, 120),
		rawTLV(4, str("et-0/0/3")...),
		rawTLV(5, str("site1lsw5k10")...),
		rawTLV(7, 0x00, 0x14, 0x00, 0x14),
		mgmt(1, 192, 0, 2, 2),
		mgmt(2, 0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2),
		org(oui8023, 3, 0x03, 0x00, 0x00, 0x00, 0x05), // link aggregation: capable, enabled, port 5
		org(oui8023, 4, 0x24, 0x00),
		rawTLV(0),
	)
	lldpdFrame = cat(
		rawTLV(1, append([]byte{4}, 0x02, 0x00, 0x00, 0x00, 0x00, 0x09)...),
		rawTLV(2, append([]byte{3}, 0x02, 0x00, 0x00, 0x00, 0x00, 0x0a)...), // port: MAC
		rawTLV(3, 0, 120),
		rawTLV(4, str("eth1")...),
		rawTLV(5, str("leaf02")...),
		rawTLV(7, 0x00, 0x80, 0x00, 0x80),
		mgmt(6, 0x02, 0x00, 0x00, 0x00, 0x00, 0x09), // MAC management address
		org(ouiMED, 1, 0x00, 0x21, 0x01),            // MED capabilities, inventory, class 1
		org(ouiMED, 5, str("1.2")...),               // hardware revision
		org(ouiMED, 8, str("SN123")...),             // serial number
		org(ouiMED, 9, str("Example Corp")...),      // manufacturer
		org(ouiMED, 10, str("X1")...),               // model
		rawTLV(0),
	)
)

func TestParseLLDPSwitchFrames(t *testing.T) {
	for _, tt := range []struct {
		name  string
		frame []byte
		want  Neighbor
		port  string
	}{
		{"nxos", nxosFrame, Neighbor{
			ChassisID: "00:3a:9c:5a:10:01", ChassisIDSubtype: "mac",
			PortID: "Ethernet1/12", PortIDSubtype: "ifname", PortDescription: "Ethernet1/12",
			SystemName: "leaf01", Description: "Cisco Nexus Operating System (NX-OS) Software 10.3(4a)",
			Capabilities: []string{"bridge", "router"}, EnabledCapabilities: []string{"bridge", "router"},
			MgmtAddresses: []string{"192.0.2.1"}, VLAN: 1,
			MACPHY:       &MACPHY{AutonegSupported: true, AutonegEnabled: true, MAUType: 30},
			MaxFrameSize: 9216, Interface: "ens1f0np0", TTL: 120,
		}, "Ethernet1/12"},
		{"junos", junosFrame, Neighbor{
			ChassisID: "40:de:ad:00:00:01", ChassisIDSubtype: "mac",
			PortID: "513", PortIDSubtype: "local", PortDescription: "et-0/0/3",
			SystemName: "site1lsw5k10", Capabilities: []string{"bridge", "router"}, EnabledCapabilities: []string{"bridge", "router"},
			MgmtAddresses:   []string{"192.0.2.2", "2001:db8::2"},
			LinkAggregation: &LinkAggregation{Supported: true, Enabled: true, PortID: 5},
			MaxFrameSize:    9216, Interface: "ens1f0np0", TTL: 120,
		}, "et-0/0/3"},
		{"lldpd", lldpdFrame, Neighbor{
			ChassisID: "02:00:00:00:00:09", ChassisIDSubtype: "mac",
			PortID: "02:00:00:00:00:0a", PortIDSubtype: "mac", PortDescription: "eth1",
			SystemName: "leaf02", Capabilities: []string{"station"}, EnabledCapabilities: []string{"station"},
			MgmtAddresses: []string{"02:00:00:00:00:09"},
			Inventory:     &Inventory{HardwareRevision: "1.2", SerialNumber: "SN123", Manufacturer: "Example Corp", Model: "X1"},
			Interface:     "ens1f0np0", TTL: 120,
		}, "eth1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLLDP(tt.frame, "ens1f0np0")
			if got == nil {
				t.Fatal("frame does not decode")
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("neighbor\n got  %+v\n want %+v", *got, tt.want)
			}
			if p := got.SwitchPort(); p != tt.port {
				t.Errorf("SwitchPort = %q, want %q", p, tt.port)
			}
		})
	}
}

// BOOTy's bm4x frame, read back: the MAC port ID renders in colon form.
func TestParseLLDPBM4XFrame(t *testing.T) {
	b, err := Frame("ens1f0np0", []byte{0x3c, 0xfd, 0xfe, 0xa1, 0xb2, 0xc3}, Info{MachineID: "m", Profile: ProfileBM4XCableCheck})
	if err != nil {
		t.Fatal(err)
	}
	n := parseLLDP(b[14:], "eth0")
	if n.PortID != "3c:fd:fe:a1:b2:c3" || n.PortIDSubtype != "mac" || n.SystemName != "bare-minimal" || n.PortDescription != "ens1f0np0" {
		t.Fatalf("neighbor = %+v", n)
	}
}

func TestIDString(t *testing.T) {
	for _, tt := range []struct {
		subtype string
		id      []byte
		want    string
	}{
		{"network-address", []byte{1, 192, 0, 2, 7}, "192.0.2.7"},
		{"network-address", append([]byte{2}, make([]byte, 16)...), "::"},
		{"network-address", []byte{9, 1}, "\t\x01"},
		{"mac", []byte{1, 2, 3}, "\x01\x02\x03"},
		{"local", []byte("bad\x1bname"), "bad?name"},
	} {
		if got := idString(tt.subtype, tt.id); got != sanitizeLLDP(tt.want) {
			t.Errorf("idString(%s, %x) = %q, want %q", tt.subtype, tt.id, got, sanitizeLLDP(tt.want))
		}
	}
}
