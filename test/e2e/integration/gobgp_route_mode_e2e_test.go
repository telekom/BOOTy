//go:build e2e_gobgp

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// EVPN route mode e2e tests. BOOTy selects the EVPN route types it originates
// from the provisioner FRR config (see README "Netplan Configuration"):
//   - booty-unnumbered: advertise-all-vni with an L2VNI, so Type-2 and Type-3
//     routes are originated next to Type-5.
//   - booty-numbered: advertise-all-vni with the provisioning VNI declared as
//     the default VRF L3VNI (the CAPRF frr.conf shape), so Type-5 only.
//   - booty-dual: no FRR config (vars only), so Type-5 only.
// The route distinguisher BOOTy uses is <ASN>:<VNI>.

const routeModeObserveWindow = 15 * time.Second

func spineEVPNRoutes(t *testing.T, routeType string) string {
	t.Helper()
	out, _ := gobgpDockerExecRaw(t, gobgpLabSpine, "vtysh", "-c", "show bgp l2vpn evpn route type "+routeType)
	return out
}

func rdLine(rd string) string { return "Route Distinguisher: " + rd }

func requireBootyLog(t *testing.T, container, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "logs", container).CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs %s: %v", container, err)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("%s log does not contain %q", container, want)
	}
}

func TestGoBGPRouteModeL2FromFRRConfig(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	waitForBGPInterface(t, "eth3")
	requireBootyLog(t, gobgpLabUnnumbered, "detected netplan configuration files")

	rd := rdLine("65020:100")
	for _, routeType := range []string{"prefix", "macip", "multicast"} {
		waitForGobgpCondition(t, bgpConvergeTimeout, "booty-unnumbered "+routeType+" route not on spine", func(context.Context) (bool, string) {
			out := spineEVPNRoutes(t, routeType)
			return strings.Contains(out, rd), out
		})
	}
}

func TestGoBGPRouteModeType5Only(t *testing.T) {
	requireGoBGPLab(t)
	t.Cleanup(func() { dumpDebugState(t) })

	tests := []struct {
		name, container, rd, log string
	}{
		{name: "L3VNI in FRR config", container: gobgpLabNumbered, rd: "65030:100", log: "provision VNI is an FRR L3VNI"},
		{name: "no FRR config", container: gobgpLabDual, rd: "65010:100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.log != "" {
				requireBootyLog(t, tt.container, tt.log)
			}
			rd := rdLine(tt.rd)
			waitForGobgpCondition(t, 2*bgpConvergeTimeout, tt.container+" Type-5 route not on spine", func(context.Context) (bool, string) {
				out := spineEVPNRoutes(t, "prefix")
				return strings.Contains(out, rd), out
			})
			// Type-2/Type-3 would follow Type-5 within the same setup, so watch
			// for a while before concluding that they are absent.
			deadline := time.Now().Add(routeModeObserveWindow)
			for time.Now().Before(deadline) {
				for _, routeType := range []string{"macip", "multicast"} {
					if out := spineEVPNRoutes(t, routeType); strings.Contains(out, rd) {
						t.Fatalf("%s originated %s routes in Type-5 only mode:\n%s", tt.container, routeType, out)
					}
				}
				time.Sleep(bgpConvergeInterval)
			}
		})
	}
}
