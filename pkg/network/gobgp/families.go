package gobgp

import (
	apipb "github.com/osrg/gobgp/v3/api"
)

// bgpTimers returns the configured BGP keepalive/hold timers.
func bgpTimers(cfg *Config) *apipb.Timers {
	return &apipb.Timers{
		Config: &apipb.TimersConfig{
			KeepaliveInterval: cfg.KeepaliveInterval,
			HoldTime:          cfg.HoldTime,
		},
	}
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
