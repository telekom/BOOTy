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
	if _, err := ParseVars(strings.NewReader("LLDP_EMIT_ALL=maybe\n")); err == nil {
		t.Fatal("LLDP_EMIT_ALL=maybe accepted")
	}
}
