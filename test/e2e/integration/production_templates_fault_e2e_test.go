//go:build e2e_prodtpl

// Fault injection in the production template lab (#630 section 4.3).
package integration

import (
	"context"
	"slices"
	"testing"
	"time"
)

// bgpd of dcgw01 crashes and watchfrr restarts it (mirrors FRR
// bgp_evpn_rt5 and bgp_gr topotests that kill bgpd). dcgw01 is a route
// reflector and the gateway of the L3VNI: every server must withdraw its
// Type-5 routes, gateway neighbor entry and FDB entry, keep the routes of
// the other servers (reflected by dcgw02), and install everything again
// when dcgw01 is back, like the shadow node.
func TestProdTplRRRestart(t *testing.T) {
	gwRoute := "route default via ::ffff:198.51.100.1 dev br.provision [onlink] proto bgp metric 20"
	otherRoute := "route other-lo via ::ffff:other-vtep dev br.provision [onlink] proto bgp metric 20"
	prodtplWaitL3VNI(t)

	ctx, cancel := context.WithTimeout(context.Background(), prodtplTimeout)
	defer cancel()
	if out, err := prodtplExec(ctx, prodtplDCGW1, "pkill", "-9", "bgpd"); err != nil {
		t.Fatalf("kill bgpd on dcgw01: %v: %s", err, out)
	}
	start := time.Now()
	// The outage lasts until dcgw01 peers with the leaf again (8 to 70 s
	// in the lab), so all servers are polled together.
	withdrawn := map[string]bool{}
	for len(withdrawn) < len(prodtplServers) {
		for _, node := range prodtplServers {
			if withdrawn[node.name] {
				continue
			}
			got, err := prodtplL3VNIState(ctx, node)
			if err != nil || slices.Contains(got, gwRoute) {
				continue
			}
			if slices.Contains(got, "neigh ::ffff:198.51.100.1 lladdr "+prodtplGatewayMAC+" [NOARP]") ||
				slices.Contains(got, "fdb "+prodtplGatewayMAC+" dst 198.51.100.1") {
				t.Errorf("%s withdrew the dcgw01 routes but kept the gateway entries: %q", node.name, got)
			}
			// The other servers' routes come through dcgw02.
			others := 0
			for _, s := range got {
				if s == otherRoute {
					others++
				}
			}
			if others != 2 {
				t.Errorf("%s lost the routes of the other servers during the outage: %q", node.name, got)
			}
			withdrawn[node.name] = true
			t.Logf("%s: dcgw01 routes withdrawn %v after the crash", node.name, time.Since(start).Round(10*time.Millisecond))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("dcgw01 routes not withdrawn on every server: %v", withdrawn)
		case <-time.After(200 * time.Millisecond):
		}
	}

	want := sessionView{State: "Established", RemoteAS: prodtplASN, LocalAS: prodtplASN,
		HoldMsecs: 90000, KeepMsecs: 30000, Families: []string{"l2VpnEvpn"}}
	for _, node := range prodtplServers {
		waitView(t, prodtplDCGW1, node.underlay, want)
	}
	prodtplWaitL3VNI(t)
	t.Logf("all servers recovered %v after the crash", time.Since(start).Round(time.Second))
}
