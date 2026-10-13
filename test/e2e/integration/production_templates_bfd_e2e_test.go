//go:build e2e_prodtpl

// BFD part of the production template lab (#630 section 3.6, C19). The
// leaf runs single-hop BFD to every uplink, the route reflectors run
// multihop BFD to every server. BOOTy (template profile "fabric"), the
// native server (BFD_TRANSMIT_MS/BFD_RECEIVE_MS) and the shadow FRR node
// must look the same from the fabric.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// bfdView is the part of a FRR "show bfd peers json" entry that must be
// the same for every server.
type bfdView struct {
	Status       string
	Multihop     bool
	RemoteTx     float64
	RemoteRx     float64
	RemoteDetect float64
}

// prodtplBFDPeers returns the BFD peers of a FRR node by interface (single
// hop) or by peer address (multihop).
func prodtplBFDPeers(ctx context.Context, container string) (map[string]bfdView, error) {
	out, err := prodtplExec(ctx, container, "vtysh", "-c", "show bfd peers json")
	if err != nil {
		return nil, err
	}
	var peers []map[string]any
	// vtysh can print warnings before the JSON document.
	if i := strings.Index(out, "\n["); i >= 0 {
		out = out[i+1:]
	} else if !strings.HasPrefix(out, "[") {
		return nil, fmt.Errorf("%s: no JSON list in %q", container, out)
	}
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return nil, fmt.Errorf("decode %s bfd peers: %w", container, err)
	}
	views := map[string]bfdView{}
	for _, p := range peers {
		v := bfdView{}
		v.Status, _ = p["status"].(string)
		v.Multihop, _ = p["multihop"].(bool)
		v.RemoteTx, _ = p["remote-transmit-interval"].(float64)
		v.RemoteRx, _ = p["remote-receive-interval"].(float64)
		v.RemoteDetect, _ = p["remote-detect-multiplier"].(float64)
		key, _ := p["interface"].(string)
		if v.Multihop || key == "" {
			key, _ = p["peer"].(string)
		}
		views[key] = v
	}
	return views, nil
}

// Mirrors FRR bfd_profiles_topo1 test_bfd_profile_values: the fabric sees
// the "fabric" profile (150/150/3) of every server, single-hop on the
// uplinks at the leaf and multihop at the route reflectors.
func TestProdTplBFDSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), prodtplTimeout)
	defer cancel()
	single := bfdView{Status: "up", RemoteTx: 150, RemoteRx: 150, RemoteDetect: 3}
	multi := single
	multi.Multihop = true
	want := map[string]map[string]bfdView{prodtplLeaf: {}, prodtplDCGW1: {}, prodtplDCGW2: {}}
	for _, node := range prodtplServers {
		for _, port := range node.leafPorts {
			want[prodtplLeaf][port] = single
		}
		want[prodtplDCGW1][node.underlay] = multi
		want[prodtplDCGW2][node.underlay] = multi
	}
	for _, c := range []string{prodtplLeaf, prodtplDCGW1, prodtplDCGW2} {
		var got map[string]bfdView
		var err error
		for {
			got, err = prodtplBFDPeers(ctx, c)
			if err == nil && reflect.DeepEqual(pick(got, want[c]), want[c]) {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("%s BFD peers (%v):\n  got  %+v\n  want %+v", c, err, got, want[c])
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func pick(got, want map[string]bfdView) map[string]bfdView {
	out := map[string]bfdView{}
	for k := range want {
		if v, ok := got[k]; ok {
			out[k] = v
		}
	}
	return out
}

// Mirrors FRR bfd_topo1 test_bfd_fast_convergence: the leaf stops sending
// on one uplink while the carrier stays up. BFD (3 x 300 ms at the server)
// ends the BGP session long before the 90 s hold time, so the next hop
// leaves the VRF table. The link coming back restores ECMP.
func TestProdTplBFDFailover(t *testing.T) {
	const maxFailover = 2500 * time.Millisecond // 900 ms detection + docker exec latency
	for _, node := range prodtplServers {
		ctx, cancel := context.WithTimeout(context.Background(), prodtplTimeout)
		port := node.leafPorts[0]
		waitRoutes(t, node, prodtplRoutesVia("ens1f0np0", "ens1f1np1"))
		start := time.Now()
		if out, err := prodtplExec(ctx, prodtplLeaf, "tc", "qdisc", "add", "dev", port, "root", "netem", "loss", "100%"); err != nil {
			cancel()
			t.Fatalf("leaf %s loss: %v\n%s", port, err, out)
		}
		want := prodtplRoutesVia("ens1f1np1")
		for {
			got, err := prodtplRoutes(ctx, node)
			if err == nil && reflect.DeepEqual(got, want) {
				break
			}
			if time.Since(start) > maxFailover {
				_, _ = prodtplExec(context.Background(), prodtplLeaf, "tc", "qdisc", "del", "dev", port, "root")
				cancel()
				t.Fatalf("%s: routes %v (%v) %s after the leaf stopped sending, want %v within %s", node.name, got, err, time.Since(start), want, maxFailover)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("%s: next hop removed %s after the leaf stopped sending", node.name, time.Since(start).Round(10*time.Millisecond))
		if out, err := prodtplExec(ctx, prodtplLeaf, "tc", "qdisc", "del", "dev", port, "root"); err != nil {
			cancel()
			t.Fatalf("leaf %s restore: %v\n%s", port, err, out)
		}
		cancel()
		waitRoutes(t, node, prodtplRoutesVia("ens1f0np0", "ens1f1np1"))
	}
}
