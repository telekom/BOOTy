package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/telekom/BOOTy/pkg/network/frrconf"
)

// BGPSessionConfig is one BGP session or peer-group of the native config,
// the same model as an frr.conf neighbor. A session inherits every unset
// field from its peer-group; its own value wins (FRR
// tests/bgpd/test_peer_attr.c). Unset timers use the "defaults" profile.
type BGPSessionConfig struct {
	// Name is the peer address, the interface name of an unnumbered
	// session, or the peer-group name.
	Name string `yaml:"name" json:"name"`
	// Interface makes the session unnumbered on the interface Name.
	Interface bool `yaml:"interface,omitempty" json:"interface,omitempty"`
	// VRF binds the session to a VRF, like "router bgp N vrf NAME".
	// Empty or "default" is the default VRF.
	VRF string `yaml:"vrf,omitempty" json:"vrf,omitempty"`
	// PeerGroup is the peer-group of a session.
	PeerGroup string `yaml:"peerGroup,omitempty" json:"peerGroup,omitempty"`
	// RemoteAS is a number, "internal" or "external".
	RemoteAS string `yaml:"remoteAS,omitempty" json:"remoteAS,omitempty"`
	// LocalAS is "local-as N"; LocalASMode must then be
	// "no-prepend-replace-as" (the default), the only mode BOOTy has.
	LocalAS     uint32 `yaml:"localAS,omitempty" json:"localAS,omitempty"`
	LocalASMode string `yaml:"localASMode,omitempty" json:"localASMode,omitempty"`
	// Keepalive, Hold and ConnectRetry are in seconds; unset uses the
	// peer-group, then the defaults profile.
	Keepalive    *uint32 `yaml:"keepalive,omitempty" json:"keepalive,omitempty"`
	Hold         *uint32 `yaml:"hold,omitempty" json:"hold,omitempty"`
	ConnectRetry *uint32 `yaml:"connectRetry,omitempty" json:"connectRetry,omitempty"`
	// Families are the activated address families: ipv4-unicast,
	// ipv6-unicast, l2vpn-evpn. Families of the peer-group add to them.
	Families []string `yaml:"families,omitempty" json:"families,omitempty"`
	// BFD and BFDProfile request a BFD session ("neighbor X bfd [profile P]").
	BFD        bool   `yaml:"bfd,omitempty" json:"bfd,omitempty"`
	BFDProfile string `yaml:"bfdProfile,omitempty" json:"bfdProfile,omitempty"`
}

// localASModeReplace is the only local-as mode: "no-prepend replace-as".
const localASModeReplace = "no-prepend-replace-as"

var sessionFamilies = map[string][2]string{
	frrconf.FamilyIPv4Unicast: {"ipv4", "unicast"},
	frrconf.FamilyIPv6Unicast: {"ipv6", "unicast"},
	frrconf.FamilyL2VPNEVPN:   {"l2vpn", "evpn"},
}

// HasSessions reports whether the native config defines BGP sessions.
func (b *BGPConfig) HasSessions() bool { return len(b.Sessions) > 0 }

// ResolveSessions builds the FRR model of the native sessions and
// peer-groups and resolves them with the frr.conf rules, so both inputs
// give the same sessions.
func (b *BGPConfig) ResolveSessions() ([]frrconf.Session, error) {
	model, err := b.frrModel()
	if err != nil {
		return nil, err
	}
	sessions, err := model.Sessions()
	if err != nil {
		return nil, fmt.Errorf("network.bgp.sessions: %w", err)
	}
	return sessions, nil
}

func (b *BGPConfig) frrModel() (*frrconf.Config, error) {
	model := &frrconf.Config{Defaults: frrconf.ProfileDatacenter}
	switch b.Defaults {
	case "", string(frrconf.ProfileDatacenter):
	case string(frrconf.ProfileTraditional):
		model.Defaults = frrconf.ProfileTraditional
	default:
		return nil, fmt.Errorf("network.bgp.defaults %q must be traditional or datacenter", b.Defaults)
	}
	routers := map[string]*frrconf.Router{}
	var vrfs []string
	router := func(vrf string) *frrconf.Router {
		if vrf == "" {
			vrf = "default"
		}
		if r, ok := routers[vrf]; ok {
			return r
		}
		r := &frrconf.Router{ASN: b.ASN, VRF: vrf, DefaultIPv4Unicast: new(false)}
		routers[vrf] = r
		vrfs = append(vrfs, vrf)
		return r
	}
	for i := range b.Sessions {
		s := &b.Sessions[i]
		r := router(s.VRF)
		if s.PeerGroup != "" && slices.IndexFunc(r.Neighbors, func(n frrconf.Neighbor) bool { return n.Name == s.PeerGroup }) < 0 {
			g := slices.IndexFunc(b.PeerGroups, func(g BGPSessionConfig) bool { return g.Name == s.PeerGroup })
			if g < 0 {
				return nil, fmt.Errorf("network.bgp.sessions %s: peer-group %s is not defined", s.Name, s.PeerGroup)
			}
			if err := addNeighbor(r, &b.PeerGroups[g], true); err != nil {
				return nil, err
			}
		}
		if err := addNeighbor(r, s, false); err != nil {
			return nil, err
		}
	}
	for _, vrf := range vrfs {
		model.Routers = append(model.Routers, *routers[vrf])
	}
	return model, nil
}

// addNeighbor adds a session or peer-group with its address families.
func addNeighbor(r *frrconf.Router, s *BGPSessionConfig, group bool) error {
	what := "network.bgp.sessions " + s.Name
	if group {
		what = "network.bgp.peerGroups " + s.Name
	}
	n := frrconf.Neighbor{
		Name: s.Name, PeerGroup: group, Interface: s.Interface && !group, Group: s.PeerGroup,
		KeepaliveTime: s.Keepalive, HoldTime: s.Hold, ConnectRetry: s.ConnectRetry,
		BFD: s.BFD, BFDProfile: s.BFDProfile,
	}
	if group {
		n.Group = ""
	}
	if s.RemoteAS != "" {
		ras, err := parseRemoteAS(s.RemoteAS)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		n.RemoteAS = &ras
	}
	if s.LocalAS != 0 {
		if s.LocalASMode != "" && s.LocalASMode != localASModeReplace {
			return fmt.Errorf("%s: localASMode %q: only %s is supported", what, s.LocalASMode, localASModeReplace)
		}
		n.LocalAS = &frrconf.LocalAS{ASN: s.LocalAS, NoPrepend: true, ReplaceAS: true}
	}
	r.Neighbors = append(r.Neighbors, n)
	for _, f := range s.Families {
		afi, ok := sessionFamilies[f]
		if !ok {
			return fmt.Errorf("%s: family %q must be ipv4-unicast, ipv6-unicast or l2vpn-evpn", what, f)
		}
		i := slices.IndexFunc(r.AddressFamilies, func(af frrconf.AddressFamily) bool { return af.AFI == afi[0] && af.SAFI == afi[1] })
		if i < 0 {
			r.AddressFamilies = append(r.AddressFamilies, frrconf.AddressFamily{AFI: afi[0], SAFI: afi[1]})
			i = len(r.AddressFamilies) - 1
		}
		r.AddressFamilies[i].Neighbors = append(r.AddressFamilies[i].Neighbors, frrconf.AFNeighbor{Name: s.Name, Activate: true})
	}
	return nil
}

func parseRemoteAS(v string) (frrconf.RemoteAS, error) {
	switch strings.ToLower(v) {
	case "internal":
		return frrconf.RemoteAS{Kind: frrconf.RemoteASInternal}, nil
	case "external":
		return frrconf.RemoteAS{Kind: frrconf.RemoteASExternal}, nil
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return frrconf.RemoteAS{}, fmt.Errorf("remoteAS %q must be a number, internal or external", v)
	}
	return frrconf.RemoteAS{Kind: frrconf.RemoteASNumber, Number: uint32(n)}, nil
}
