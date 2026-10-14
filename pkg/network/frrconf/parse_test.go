package frrconf

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// testdata/bm4x holds the bm4x-operator provisioning templates
// (cit/t-caas/bm4x-operator b90274c,
// internal/controller/infra/redfish/provcfg/templates) rendered with
// documentation addresses (RFC 5737, RFC 3849). testdata/caprf holds the
// CAPRF e2e variants from test/e2e/lib.sh generate_evpn_provisioner_config.
func goldenInputs(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/*/*/frr.conf")
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden inputs: %v", err)
	}
	return files
}

func TestParseGolden(t *testing.T) {
	for _, in := range goldenInputs(t) {
		t.Run(filepath.Dir(in), func(t *testing.T) {
			data, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse() = %v", err)
			}
			got, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join(filepath.Dir(in), "frr.golden.json")
			if *update {
				if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden file (run with -update): %v", err)
			}
			if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
				t.Fatalf("model differs from %s (run with -update and review the diff):\n%s", golden, got)
			}
		})
	}
}

// Every statement of the production template must matter: removing any
// single line changes the model or makes the config invalid. A line whose
// removal changes nothing would be a statement that BOOTy silently drops.
func TestParseMutationRemoveLine(t *testing.T) {
	data, err := os.ReadFile("testdata/bm4x/two-uplinks/frr.conf")
	if err != nil {
		t.Fatal(err)
	}
	base, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "!") || ignoredWhenRemoved(fields) {
			continue
		}
		mutated := strings.Join(append(append([]string{}, lines[:i]...), lines[i+1:]...), "\n")
		cfg, err := Parse([]byte(mutated))
		if err == nil && reflect.DeepEqual(stripLines(cfg), stripLines(base)) {
			t.Errorf("removing line %d %q changes nothing", i+1, strings.TrimSpace(line))
		}
	}
}

// ignoredWhenRemoved lists statements whose only effect is on FRR logging
// or on the block structure, so removing them does not change the model.
func ignoredWhenRemoved(f []string) bool {
	switch {
	case f[0] == "log", is(f, "service", "integrated-vtysh-config"), is(f, "exit"), is(f, "exit-address-family"):
		return true
	case len(f) == 3 && f[0] == "neighbor" && f[2] == "bfd":
		// The template also has "neighbor X bfd profile P", which enables
		// BFD as well (doc/user/bfd.rst, "neighbor <A.B.C.D|X:X::X:X|WORD>
		// bfd profile BFDPROF").
		return true
	}
	return false
}

// stripLines removes line numbers, which change with every removed line.
func stripLines(cfg *Config) *Config {
	data, _ := json.Marshal(cfg)
	var out Config
	_ = json.Unmarshal(data, &out)
	clearLines(reflect.ValueOf(&out).Elem())
	return &out
}

func clearLines(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).Name == "Line" {
				v.Field(i).SetInt(0)
				continue
			}
			clearLines(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			clearLines(v.Index(i))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			clearLines(v.Elem())
		}
	}
}

// Statements outside the supported set fail with their line number, also
// statements FRR knows (FRR would log an error and go on).
func TestParseRejectsUnsupportedStatements(t *testing.T) {
	data, err := os.ReadFile("testdata/bm4x/two-uplinks/frr.conf")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	afterAF := 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "address-family ipv4 unicast" {
			afterAF = i + 1
		}
	}
	for _, stmt := range []string{
		"neighbor leaf password secret",
		"neighbor leaf ebgp-multihop 2",
		"neighbor leaf local-as 65501 replace-as",
		"neighbor leaf local-as 65501 dual-as",
		"neighbor unknown activate",
		"bgp graceful-restart",
		"set metric 10",
		"ip route 192.0.2.0/24 192.0.2.1",
		"ipv6 route 192.0.2.0/24 blackhole",
		"echo-mode",
		"route-target import 65169",
		"frr defaults cloud",
		"address-family ipv4 multicast",
		"garbage",
	} {
		t.Run(stmt, func(t *testing.T) {
			mutated := append(append(append([]string{}, lines[:afterAF]...), "  "+stmt), lines[afterAF:]...)
			_, err := Parse([]byte(strings.Join(mutated, "\n")))
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Parse() = %v, want a ParseError", err)
			}
			if pe.Line != afterAF+1 {
				t.Fatalf("error line = %d, want %d (%v)", pe.Line, afterAF+1, err)
			}
		})
	}
}

// FRR ignores indentation and retries a statement in the parent nodes
// (lib/command.c, command_config_read_one_line).
func TestParseFRRNodeRules(t *testing.T) {
	cfg, err := Parse([]byte(`
router bgp 65000 vrf Vrf_underlay
neighbor leaf peer-group
 address-family ipv4 unicast
  network 192.0.2.1/32
 neighbor leaf remote-as 65500
neighbor eth0 interface peer-group leaf
address-family l2vpn evpn
advertise-all-vni
route-map RM permit 10
set local-preference 90
route-map RM deny 5
vrf red
 vni 100 prefix-routes-only
exit-vrf
vni 200
`))
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}
	r := cfg.Routers[0]
	if r.VRF != "Vrf_underlay" || len(r.Neighbors) != 2 || r.Neighbors[0].RemoteAS == nil || r.Neighbors[1].Group != "leaf" {
		t.Fatalf("router = %+v", r)
	}
	if len(r.AddressFamilies) != 2 || r.AddressFamilies[0].Networks[0] != "192.0.2.1/32" || !r.AddressFamilies[1].AdvertiseAllVNI {
		t.Fatalf("address families = %+v", r.AddressFamilies)
	}
	if e := cfg.RouteMaps[0].Entries; len(e) != 2 || e[0].Seq != 5 || e[1].SetLocalPreference == nil {
		t.Fatalf("route-map entries = %+v, want sorted by sequence", e)
	}
	want := []VRFVNI{{VRF: "red", VNI: 100, PrefixRoutesOnly: true, Line: 14}, {VNI: 200, Line: 16}}
	if !reflect.DeepEqual(cfg.VRFVNIs, want) {
		t.Fatalf("VRFVNIs = %+v, want %+v", cfg.VRFVNIs, want)
	}
}

// Statements without effect on routing are accepted in strict mode and do
// not change the model; "#" lines are comments like "!" lines.
func TestParseNoOpStatements(t *testing.T) {
	base := "router bgp 65000\n neighbor leaf peer-group\n neighbor leaf remote-as external\n"
	want, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		" neighbor leaf description uplink to leaf01",
		" bgp log-neighbor-changes",
		" no bgp log-neighbor-changes",
		"debug bgp updates",
		"log timestamp precision 6",
		"# a comment",
		"   #indented comment with router bgp 1",
	} {
		t.Run(stmt, func(t *testing.T) {
			got, err := Parse([]byte(base + stmt + "\n"))
			if err != nil {
				t.Fatalf("Parse() = %v", err)
			}
			got.Routers[0].Line, want.Routers[0].Line = 0, 0
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("model changed:\n got  %+v\n want %+v", got, want)
			}
		})
	}
	if _, err := Parse([]byte(base + " neighbor other description x\n")); err == nil {
		t.Fatal("description of an undefined neighbor parsed")
	}
}

// A static route inside a vrf block is a route of the VRF table, which
// BOOTy does not install: it is rejected, not moved to the main table.
func TestParseRejectsVRFStaticRoute(t *testing.T) {
	cfg, err := Parse([]byte("vrf red\n ip route 192.0.2.0/24 blackhole\nexit-vrf\n"))
	if err == nil || !strings.Contains(err.Error(), "static routes inside vrf red") {
		t.Fatalf("Parse() = %+v, %v, want the vrf static route error", cfg, err)
	}
}

// The error names the node the parser is in, also after a statement that
// popped to a parent node.
func TestParseErrorNamesTheNode(t *testing.T) {
	for _, tt := range []struct{ conf, want string }{
		{"router bgp 65000\n address-family l2vpn evpn\n  maximum-paths 8\n", "(in router bgp 65000 > address-family l2vpn evpn)"},
		{"router bgp 65000\n address-family ipv4 unicast\n exit-address-family\n maximum-paths 8\n", "(in router bgp 65000)"},
		{"route-map RM permit 10\n set metric 5\n", "(in route-map RM permit 10)"},
		{"bgp graceful-restart\n", "(in the top level)"},
		{"router bgp 65000\n neighbor 192.0.2.1 remote-as 99999999999\n", "invalid remote-as \"99999999999\" (in router bgp 65000)"},
	} {
		_, err := Parse([]byte(tt.conf))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%q) = %v, want %q", tt.conf, err, tt.want)
		}
	}
}

// Lenient mode (FRR_CONFIG_STRICT=false) reports every statement it skips
// with its line and keeps the node, so the next statements still apply.
func TestParseLenient(t *testing.T) {
	var skipped []int
	cfg, err := ParseWith([]byte("router bgp 65000\n address-family ipv4 unicast\n  maximum-paths 8\n  network 192.0.2.0/24\nip prefix-list PL seq 5 permit 192.0.2.0/24\n"),
		Options{Lenient: true, Warn: func(e *ParseError) { skipped = append(skipped, e.Line) }})
	if err != nil {
		t.Fatalf("ParseWith() = %v", err)
	}
	if !reflect.DeepEqual(skipped, []int{3, 5}) {
		t.Fatalf("skipped lines = %v, want [3 5]", skipped)
	}
	if nets := cfg.Routers[0].AddressFamilies[0].Networks; !reflect.DeepEqual(nets, []string{"192.0.2.0/24"}) {
		t.Fatalf("networks = %v: the statement after a skipped one must stay in the address-family", nets)
	}
}

// Lenient mode skips only unsupported statements. An invalid value and an
// unsupported statement that opens a block stay errors: skipping a block
// opener would apply its body to the enclosing node.
func TestParseLenientLimits(t *testing.T) {
	base := "router bgp 65000\n neighbor leaf peer-group\n neighbor leaf remote-as external\n address-family ipv4 unicast\n"
	for _, stmt := range []string{
		" neighbor 192.0.2.1 remote-as 65oo1",
		" bgp router-id 300.1.1.1",
		" network 192.0.2.0/33",
		"router ospf",
		"router bgp 65000 view foo",
		"interface eth0",
		" address-family ipv4 multicast",
		"vrf red blue",
		" neighbor swp2 interface peer-group UNDEF",
	} {
		t.Run(stmt, func(t *testing.T) {
			_, err := ParseWith([]byte(base+stmt+"\n"), Options{Lenient: true})
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Line != 5 {
				t.Fatalf("ParseWith(lenient) = %v, want a ParseError on line 5", err)
			}
		})
	}
}

// A rejected "neighbor IFNAME interface ..." leaves no neighbor behind, and
// a statement that failed in a parent node leaves the parser in its node.
func TestParseLenientKeepsState(t *testing.T) {
	cfg, err := ParseWith([]byte(`router bgp 65000
 neighbor swp1 interface v6only
 address-family ipv4 unicast
  ip route 192.0.2.0/24 192.0.2.1
  network 198.51.100.0/24
`), Options{Lenient: true})
	if err != nil {
		t.Fatalf("ParseWith() = %v", err)
	}
	r := cfg.Routers[0]
	if len(r.Neighbors) != 0 {
		t.Fatalf("neighbors = %+v, want none from the rejected interface statement", r.Neighbors)
	}
	if len(cfg.StaticRoutes) != 0 || !reflect.DeepEqual(r.AddressFamilies[0].Networks, []string{"198.51.100.0/24"}) {
		t.Fatalf("static routes %+v, networks %v", cfg.StaticRoutes, r.AddressFamilies[0].Networks)
	}
}
