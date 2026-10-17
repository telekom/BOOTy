package caprf

import (
	"strings"
	"testing"
)

func TestCableCheckVars(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader("LLDP_PROFILE=bm4x-cablecheck\nLLDP_EMIT_ALL=false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.LLDP.Profile != "bm4x-cablecheck" || cfg.Network.LLDP.EmitAll == nil || *cfg.Network.LLDP.EmitAll {
		t.Fatalf("lldp = %+v", cfg.Network.LLDP)
	}
	if cfg, err := ParseVars(strings.NewReader("NIC_FW_LLDP=keep\n")); err != nil || cfg.Network.NICFirmwareLLDP != "keep" {
		t.Fatalf("NIC_FW_LLDP: %+v, %v", cfg.Network, err)
	}
	if cfg, err := ParseVars(strings.NewReader("CHECK_HOLD=until-poweroff\n")); err != nil || cfg.Check.Hold != "until-poweroff" {
		t.Fatalf("CHECK_HOLD: %+v, %v", cfg.Check, err)
	}
	if cfg, err := ParseVars(strings.NewReader("LLDP_NEIGHBOR_WAIT=90s\n")); err != nil || cfg.Check.LLDPNeighborWait != "90s" {
		t.Fatalf("LLDP_NEIGHBOR_WAIT: %+v, %v", cfg.Check, err)
	}
	if _, err := ParseVars(strings.NewReader("LLDP_EMIT_ALL=maybe\n")); err == nil {
		t.Fatal("LLDP_EMIT_ALL=maybe accepted")
	}
}
