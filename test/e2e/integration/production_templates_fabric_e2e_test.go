//go:build e2e_prodtpl

// Fabric parity of the originated routes, the BFD packets and the RR
// session capabilities, against the shadow FRR node (kindmetal acceptance
// round 3, fabric-parity-diff).
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// prodtplPathMetrics returns the MED of each path in an FRR JSON route
// entry ("metric"; -1 without one). FRR nests the EVPN paths one list
// deeper than the unicast ones.
func prodtplPathMetrics(v any) []float64 {
	var out []float64
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, prodtplPathMetrics(e)...)
		}
	case map[string]any:
		m, ok := x["metric"].(float64)
		if !ok {
			m = -1
		}
		out = append(out, m)
	}
	return out
}

// prodtplServerRDs returns, per server underlay address, the RDs of its
// EVPN routes at dcgw01 (native-vrf has no RR sessions) and the MEDs of their Type-5 paths.
func prodtplServerRDs(ctx context.Context) (rds map[string][]string, meds map[string][]float64, err error) {
	out, err := prodtplExec(ctx, prodtplDCGW1, "vtysh", "-c", "show bgp l2vpn evpn json")
	if err != nil {
		return nil, nil, err
	}
	var table map[string]any
	if err := json.Unmarshal([]byte(jsonPart(out)), &table); err != nil {
		return nil, nil, fmt.Errorf("decode dcgw01 evpn table: %w", err)
	}
	rds, meds = map[string][]string{}, map[string][]float64{}
	for rd, v := range table {
		routes, ok := v.(map[string]any)
		ip, _, found := strings.Cut(rd, ":")
		if !ok || !found {
			continue
		}
		for _, node := range prodtplServers {
			if ip != node.underlay {
				continue
			}
			rds[ip] = append(rds[ip], rd)
			for key, r := range routes {
				if e, ok := r.(map[string]any); ok && strings.HasPrefix(key, "[5]") {
					meds[ip] = append(meds[ip], prodtplPathMetrics(e["paths"])...)
				}
			}
		}
	}
	return rds, meds, nil
}

// BOOTy originates its EVPN routes with FRR's automatic RD
// "<router-id>:<n>" (the same n as the shadow node with the same
// template) and MED 0 on the Type-5 routes and the underlay /32, like
// FRR. Before, every host used 4210000017:3000 and sent no MED.
func TestProdTplRDAndMED(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), prodtplTimeout)
	defer cancel()
	var rds map[string][]string
	var meds map[string][]float64
	for {
		var err error
		rds, meds, err = prodtplServerRDs(ctx)
		if err == nil && len(rds) == len(prodtplServers) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server RDs at dcgw01 = %v (%v), want one per server", rds, err)
		case <-time.After(2 * time.Second):
		}
	}
	shadow := rds[prodtplShadowNode.underlay]
	if len(shadow) != 1 {
		t.Fatalf("shadow RDs = %v, want one", shadow)
	}
	_, n, _ := strings.Cut(shadow[0], ":")
	for _, node := range prodtplServers {
		if got, want := rds[node.underlay], node.underlay+":"+n; len(got) != 1 || got[0] != want {
			t.Errorf("%s: RDs %v, want [%s] like the shadow node", node.name, got, want)
		}
		if len(meds[node.underlay]) == 0 {
			t.Errorf("%s: no Type-5 paths at dcgw01", node.name)
		}
		for _, m := range meds[node.underlay] {
			if m != 0 {
				t.Errorf("%s: Type-5 MED %v at dcgw01, want 0 like the shadow node (%v)", node.name, m, meds[prodtplShadowNode.underlay])
			}
		}
		out, err := prodtplExec(ctx, prodtplLeaf, "vtysh", "-c", "show bgp ipv4 unicast "+node.underlay+"/32 json")
		if err != nil {
			t.Fatal(err)
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(jsonPart(out)), &r); err != nil {
			t.Fatalf("decode leaf route %s: %v", node.underlay, err)
		}
		if ms := prodtplPathMetrics(r["paths"]); len(ms) == 0 || ms[0] != 0 || ms[len(ms)-1] != 0 {
			t.Errorf("%s: leaf MEDs of the underlay /32 = %v, want 0", node.name, ms)
		}
	}
}

// The route reflectors see the BFD Required Min Echo RX of FRR bfdd
// (50 ms) from every server, and the extended next hop capability on the
// RR session only from the GoBGP servers (documented deviation: GoBGP has
// no per-peer switch for it).
func TestProdTplRRSessionEchoAndENHE(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), prodtplTimeout)
	defer cancel()
	out, err := prodtplExec(ctx, prodtplDCGW1, "vtysh", "-c", "show bfd peers json")
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(out, "\n["); i >= 0 {
		out = out[i+1:]
	}
	var peers []map[string]any
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		t.Fatalf("decode dcgw01 bfd peers: %v", err)
	}
	echo := map[string]any{}
	for _, p := range peers {
		if a, _ := p["peer"].(string); a != "" {
			echo[a] = p["remote-echo-receive-interval"]
		}
	}
	for _, node := range prodtplServers {
		if echo[node.underlay] != float64(50) {
			t.Errorf("%s: remote echo receive interval at dcgw01 = %v, want 50 like FRR bfdd", node.name, echo[node.underlay])
		}
		n, err := prodtplNeighbor(ctx, prodtplDCGW1, node.underlay)
		if err != nil {
			t.Fatal(err)
		}
		caps, _ := n["neighborCapabilities"].(map[string]any)
		want := any("received")
		if node.name == "shadow" {
			want = nil
		}
		if got := caps["extendedNexthop"]; got != want {
			t.Errorf("%s: extended next hop at dcgw01 = %v, want %v", node.name, got, want)
		}
	}
}
