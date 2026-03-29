//go:build linux

package lldp

import (
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func mustSerializeLLDP(t *testing.T) []byte {
	t.Helper()

	lldpLayer := &layers.LinkLayerDiscovery{
		ChassisID: layers.LLDPChassisID{
			Subtype: layers.LLDPChassisIDSubTypeMACAddr,
			ID:      []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
		PortID: layers.LLDPPortID{
			Subtype: layers.LLDPPortIDSubtypeIfaceName,
			ID:      []byte("Ethernet1"),
		},
		TTL: 120,
	}

	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{}, lldpLayer); err != nil {
		t.Fatalf("serialize LLDP payload: %v", err)
	}

	return buf.Bytes()
}

func TestParseLLDPValidPayload(t *testing.T) {
	data := mustSerializeLLDP(t)
	n := parseLLDP(data, "eth0")
	if n == nil {
		t.Fatal("parseLLDP() returned nil for valid payload")
	}

	if n.Interface != "eth0" {
		t.Fatalf("Interface = %q, want %q", n.Interface, "eth0")
	}
	if n.ChassisID != "00:11:22:33:44:55" {
		t.Fatalf("ChassisID = %q, want %q", n.ChassisID, "00:11:22:33:44:55")
	}
	if n.PortID != "Ethernet1" {
		t.Fatalf("PortID = %q, want %q", n.PortID, "Ethernet1")
	}
	if n.SystemName != "" {
		t.Fatalf("SystemName = %q, want empty", n.SystemName)
	}
	if n.Description != "" {
		t.Fatalf("Description = %q, want empty", n.Description)
	}
	if n.TTL != 120 {
		t.Fatalf("TTL = %d, want %d", n.TTL, 120)
	}
}

func TestParseLLDPInvalidPayload(t *testing.T) {
	if got := parseLLDP([]byte{0x01, 0x02, 0x03}, "eth0"); got != nil {
		t.Fatalf("parseLLDP() = %#v, want nil for invalid payload", got)
	}
}

func TestHTONS(t *testing.T) {
	if got := htons(0x88cc); got != 0xcc88 {
		t.Fatalf("htons(0x88cc) = 0x%x, want 0xcc88", got)
	}
}
