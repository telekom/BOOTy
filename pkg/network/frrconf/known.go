package frrconf

import "slices"

// The tables below list, per CLI node, the first words of the FRR 8.2.2
// commands (lib/command.c node fallback, bgpd/bgp_vty.c, bfdd/bfdd_cli.c,
// lib/routemap_cli.c). They decide in which node FRR would apply a
// statement that BOOTy does not implement, so a skipped statement moves the
// parser to the same node as FRR.

var configWords = []string{
	"frr", "hostname", "domainname", "log", "service", "password", "enable",
	"banner", "debug", "ip", "ipv6", "vni", "router", "bfd", "route-map", "bgp",
	"vrf", "interface", "line", "key", "access-list", "agentx", "zebra", "table",
	"allow-external-route-update", "fpm", "mpls", "segment-routing", "pbr-map",
	"nexthop-group", "l2vpn", "rpki", "username", "evpn", "end", "exit",
}

var bgpWords = []string{
	"bgp", "neighbor", "address-family", "network", "aggregate-address",
	"redistribute", "maximum-paths", "distance", "timers", "coalesce-time",
	"read-quanta", "write-quanta", "update-delay", "table-map", "vnc", "bmp",
	"segment-routing", "sid", "exit",
}

var afWords = []string{
	"network", "aggregate-address", "redistribute", "maximum-paths", "neighbor",
	"table-map", "distance", "bgp", "import", "export", "rd", "rt", "label",
	"nexthop", "advertise", "advertise-all-vni", "advertise-default-gw",
	"advertise-svi-ip", "advertise-pip", "vni", "route-target", "autort",
	"default-originate", "flooding", "dup-addr-detection", "disable-ead-evi-rx",
	"disable-ead-evi-tx", "use-es-l3nhg", "ead-es-frag", "ead-es-route-target",
	"exit-address-family", "exit",
}

// afNeighborWords are the neighbor attributes of an address-family. FRR
// also accepts them in the router node (for ipv4 unicast).
var afNeighborWords = []string{
	"activate", "addpath-tx-all-paths", "addpath-tx-bestpath-per-AS",
	"allowas-in", "as-override", "attribute-unchanged", "default-originate",
	"distribute-list", "filter-list", "maximum-prefix", "maximum-prefix-out",
	"next-hop-self", "prefix-list", "remove-private-AS", "route-map",
	"route-reflector-client", "route-server-client", "send-community",
	"soft-reconfiguration", "unsuppress-map", "weight",
}

var l2vniWords = []string{"rd", "route-target", "advertise-default-gw", "advertise-svi-ip", "exit-vni", "exit"}

var routeMapWords = []string{"match", "set", "call", "on-match", "continue", "description", "exit"}

var bfdWords = []string{"profile", "peer", "exit"}

var bfdProfileWords = []string{
	"transmit-interval", "receive-interval", "detect-multiplier", "echo-mode",
	"echo-interval", "echo", "minimum-ttl", "passive-mode", "shutdown", "exit",
}

var vrfWords = []string{"vni", "ip", "ipv6", "netns", "exit-vrf", "exit"}

var lineVTYWords = []string{"exec-timeout", "login", "access-class", "ipv6", "exit"}

// knownIn reports whether FRR has a command in node n that starts like f.
func knownIn(n node, f []string) bool {
	if len(f) > 1 && f[0] == "no" {
		f = f[1:]
	}
	switch n {
	case nodeConfig:
		return slices.Contains(configWords, f[0])
	case nodeBGP:
		return slices.Contains(bgpWords, f[0])
	case nodeAF:
		if f[0] == "neighbor" {
			return isAFNeighborAttr(f)
		}
		return slices.Contains(afWords, f[0])
	case nodeL2VNI:
		return slices.Contains(l2vniWords, f[0])
	case nodeRouteMap:
		return slices.Contains(routeMapWords, f[0])
	case nodeBFD:
		return slices.Contains(bfdWords, f[0])
	case nodeBFDProfile:
		return slices.Contains(bfdProfileWords, f[0])
	case nodeVRF:
		return slices.Contains(vrfWords, f[0])
	case nodeLineVTY:
		return slices.Contains(lineVTYWords, f[0])
	}
	return false
}

// isAFNeighborAttr reports whether "neighbor X ATTR ..." is an
// address-family command. "capability orf" is one, the other capabilities
// belong to the router node.
func isAFNeighborAttr(f []string) bool {
	if len(f) < 3 {
		return false
	}
	if f[2] == "capability" {
		return len(f) > 3 && f[3] == "orf"
	}
	return slices.Contains(afNeighborWords, f[2])
}
