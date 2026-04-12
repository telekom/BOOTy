package gobgp

import (
	apipb "github.com/osrg/gobgp/v3/api"
)

// bgpTimers returns the configured BGP keepalive/hold timers.
func bgpTimers(cfg *Config) *apipb.Timers {
	return &apipb.Timers{
		Config: &apipb.TimersConfig{
			ConnectRetry:      cfg.ConnectRetry,
			KeepaliveInterval: cfg.KeepaliveInterval,
			HoldTime:          cfg.HoldTime,
		},
	}
}

// buildNumberedPeer constructs a GoBGP peer config for a numbered (IP-based) session.
func buildNumberedPeer(cfg *Config, addr string, families []*apipb.AfiSafi) *apipb.Peer {
	remoteASN := cfg.RemoteASN
	if remoteASN == 0 {
		remoteASN = cfg.ASN // iBGP
	}

	peer := &apipb.Peer{
		Conf: &apipb.PeerConf{
			NeighborAddress: addr,
			PeerAsn:         remoteASN,
		},
		Timers:   bgpTimers(cfg),
		AfiSafis: families,
		Transport: &apipb.Transport{
			MtuDiscovery: true,
		},
	}

	if cfg.AuthPassword != "" {
		peer.Conf.AuthPassword = cfg.AuthPassword
	}

	return peer
}

// buildInterfacePeer constructs a GoBGP peer config for an unnumbered (link-local) session.
func buildInterfacePeer(cfg *Config, iface, addr string, families []*apipb.AfiSafi) *apipb.Peer {
	peer := &apipb.Peer{
		Conf: &apipb.PeerConf{
			NeighborAddress: addr,
			PeerAsn:         0, // External peer, ASN learned via open
		},
		Timers:   bgpTimers(cfg),
		AfiSafis: families,
		Transport: &apipb.Transport{
			MtuDiscovery:  true,
			LocalAddress:  "::",
			BindInterface: iface,
			RemoteAddress: addr,
		},
	}

	if cfg.AuthPassword != "" {
		peer.Conf.AuthPassword = cfg.AuthPassword
	}

	return peer
}

// allFamilies returns IPv4 unicast + L2VPN-EVPN address families.
func allFamilies() []*apipb.AfiSafi {
	return []*apipb.AfiSafi{
		{Config: &apipb.AfiSafiConfig{
			Family: &apipb.Family{Afi: apipb.Family_AFI_IP, Safi: apipb.Family_SAFI_UNICAST},
		}},
		{Config: &apipb.AfiSafiConfig{
			Family: &apipb.Family{Afi: apipb.Family_AFI_L2VPN, Safi: apipb.Family_SAFI_EVPN},
		}},
	}
}

// ipv4Families returns only IPv4 unicast address family.
func ipv4Families() []*apipb.AfiSafi {
	return []*apipb.AfiSafi{
		{Config: &apipb.AfiSafiConfig{
			Family: &apipb.Family{Afi: apipb.Family_AFI_IP, Safi: apipb.Family_SAFI_UNICAST},
		}},
	}
}
