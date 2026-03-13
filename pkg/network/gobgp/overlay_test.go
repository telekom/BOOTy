//go:build linux

package gobgp

import (
	"testing"

	apipb "github.com/osrg/gobgp/v3/api"
)

func TestBuildRouteDistinguisher(t *testing.T) {
	tests := []struct {
		name    string
		asn     uint32
		vni     uint32
		wantErr bool
		check   func(t *testing.T, a *apipb.RouteDistinguisherTwoOctetASN, b *apipb.RouteDistinguisherFourOctetASN)
	}{
		{
			name: "2-byte ASN",
			asn:  65000,
			vni:  4000,
			check: func(t *testing.T, two *apipb.RouteDistinguisherTwoOctetASN, _ *apipb.RouteDistinguisherFourOctetASN) {
				t.Helper()
				if two == nil {
					t.Fatal("expected 2-octet RD")
				}
				if two.Admin != 65000 {
					t.Errorf("Admin = %d, want 65000", two.Admin)
				}
				if two.Assigned != 4000 {
					t.Errorf("Assigned = %d, want 4000", two.Assigned)
				}
			},
		},
		{
			name: "4-byte ASN",
			asn:  70000,
			vni:  5000,
			check: func(t *testing.T, _ *apipb.RouteDistinguisherTwoOctetASN, four *apipb.RouteDistinguisherFourOctetASN) {
				t.Helper()
				if four == nil {
					t.Fatal("expected 4-octet RD")
				}
				if four.Admin != 70000 {
					t.Errorf("Admin = %d, want 70000", four.Admin)
				}
				// VNI is masked to 16 bits for 4-octet format.
				if four.Assigned != 5000 {
					t.Errorf("Assigned = %d, want 5000", four.Assigned)
				}
			},
		},
		{
			name: "4-byte ASN truncates large VNI",
			asn:  100000,
			vni:  70000,
			check: func(t *testing.T, _ *apipb.RouteDistinguisherTwoOctetASN, four *apipb.RouteDistinguisherFourOctetASN) {
				t.Helper()
				if four == nil {
					t.Fatal("expected 4-octet RD")
				}
				if four.Assigned != 70000&0xFFFF {
					t.Errorf("Assigned = %d, want %d (truncated)", four.Assigned, 70000&0xFFFF)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd, err := buildRouteDistinguisher(tt.asn, tt.vni)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			msg, err := rd.UnmarshalNew()
			if err != nil {
				t.Fatalf("unmarshal RD: %v", err)
			}

			var gotTwo *apipb.RouteDistinguisherTwoOctetASN
			var gotFour *apipb.RouteDistinguisherFourOctetASN
			switch v := msg.(type) {
			case *apipb.RouteDistinguisherTwoOctetASN:
				gotTwo = v
			case *apipb.RouteDistinguisherFourOctetASN:
				gotFour = v
			default:
				t.Fatalf("unexpected RD type: %T", msg)
			}
			tt.check(t, gotTwo, gotFour)
		})
	}
}

func TestBuildRouteTarget(t *testing.T) {
	tests := []struct {
		name string
		asn  uint32
		vni  uint32
	}{
		{"2-byte ASN", 65000, 4000},
		{"4-byte ASN", 70000, 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := buildRouteTarget(tt.asn, tt.vni)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rt == nil {
				t.Fatal("expected non-nil route target")
			}
		})
	}
}

func TestBuildEVPNType5NLRI(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType5NLRI(rd, "10.0.0.1", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nlri == nil {
		t.Fatal("expected non-nil NLRI")
	}
}

func TestBuildType5PathAttrs(t *testing.T) {
	rd, err := buildRouteDistinguisher(65000, 4000)
	if err != nil {
		t.Fatalf("build RD: %v", err)
	}

	nlri, err := buildEVPNType5NLRI(rd, "10.0.0.1", 4000)
	if err != nil {
		t.Fatalf("build NLRI: %v", err)
	}

	pattrs, err := buildType5PathAttrs(nlri, "10.0.0.1", 65000, 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 3 attributes: origin, mp-reach, ext-communities.
	if len(pattrs) != 3 {
		t.Errorf("got %d path attrs, want 3", len(pattrs))
	}
}
