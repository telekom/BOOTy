package gobgp

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
)

// evpnUpdate returns a BGP UPDATE with one MP_REACH_NLRI attribute for
// L2VPN EVPN that carries one EVPN NLRI of the given route type, with the
// length field and data of the NLRI as given.
func evpnUpdate(routeType byte, nlri []byte) []byte {
	mp := []byte{0, 25, 70, 4, 192, 0, 2, 1, 0, routeType, byte(len(nlri))} // AFI, SAFI, next hop, reserved
	mp = append(mp, nlri...)
	attrs := []byte{0x40, 1, 1, 0}        // ORIGIN IGP
	attrs = append(attrs, 0x40, 2, 0)     // AS_PATH, empty
	attrs = append(attrs, 0x90, 14, 0, 0) // MP_REACH_NLRI, extended length
	binary.BigEndian.PutUint16(attrs[len(attrs)-2:], uint16(len(mp)))
	attrs = append(attrs, mp...)
	body := []byte{0, 0, 0, 0} // withdrawn routes length, path attribute length
	binary.BigEndian.PutUint16(body[2:], uint16(len(attrs)))
	body = append(body, attrs...)
	msg := make([]byte, 19, 19+len(body))
	for i := range 16 {
		msg[i] = 0xff
	}
	binary.BigEndian.PutUint16(msg[16:], uint16(19+len(body)))
	msg[18] = bgp.BGP_MSG_UPDATE
	return append(msg, body...)
}

// TestEVPNDecodeTruncatedNLRIDoesNotPanic is the RFC 7606 evidence of
// docs/rfc-compliance.md (finding RFC7606-EVPN-PANIC): a truncated EVPN
// NLRI from a peer must end in an error (RFC 7606 section 5.3: session
// reset or AFI/SAFI disable), not in a panic of the GoBGP receive
// goroutine, which would end BOOTy, the PID 1 of the ramdisk. GoBGP
// 3.37.0 panics for these inputs (slice bounds in the route type
// decoders); 4.10.0 checks the lengths (upstream commit ac5a8894).
func TestEVPNDecodeTruncatedNLRIDoesNotPanic(t *testing.T) {
	for routeType := byte(1); routeType <= 5; routeType++ {
		for _, n := range []int{0, 1, 8, 17, 18, 19, 20, 21, 22, 23, 24, 25, 30} {
			t.Run(fmt.Sprintf("type%d/len%d", routeType, n), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decode panicked: %v", r)
					}
				}()
				_, _ = bgp.ParseBGPMessage(evpnUpdate(routeType, make([]byte, n)))
			})
		}
	}
}
