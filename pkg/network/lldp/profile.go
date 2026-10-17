package lldp

// Identity profiles of the sent frames (network.lldp.profile, LLDP_PROFILE).
const (
	// ProfileNetworkd sends what systemd-networkd sends with EmitLLDP=yes:
	// port ID = interface name, system name = hostname. The default.
	ProfileNetworkd = "networkd"
	// ProfileBM4XCableCheck sends the identity that the bm4x connection
	// check matches: port ID = NIC MAC (subtype 3) and system name
	// "bare-minimal", see bm4x_match_test.go.
	ProfileBM4XCableCheck = "bm4x-cablecheck"
)

// BM4XSystemName is the system name that bm4x-api accepts for a compute
// port (Connection.NODE_NAME).
const BM4XSystemName = "bare-minimal"

// Announce levels (network.lldp.announce, LLDP_ANNOUNCE): which TLVs the
// frames carry besides the identity.
const (
	// AnnounceParity sends what systemd-networkd sends. The default.
	AnnounceParity = "parity"
	// AnnounceStandard adds a BOOTy system description, a port
	// description with driver and PCI address, the management addresses
	// and the 802.3 MAC/PHY and maximum frame size TLVs.
	AnnounceStandard = "standard"
	// AnnounceFull adds the host identity to the system description and
	// the LLDP-MED capabilities and inventory TLVs.
	AnnounceFull = "full"
)
