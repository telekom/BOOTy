//go:build linux

package gobgp

import (
	"net"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
)

type sentRA struct {
	at       time.Duration // after the start of the test
	lifetime uint16
}

// recordRAs replaces raSend and returns the RAs sent so far.
func recordRAs(t *testing.T) func() []sentRA {
	t.Helper()
	var mu sync.Mutex
	var got []sentRA
	start := time.Now()
	orig := raSend
	t.Cleanup(func() { raSend = orig })
	raSend = func(_ string, lifetime uint16) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, sentRA{time.Since(start), lifetime})
	}
	return func() []sentRA {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

func regular(secs ...float64) []sentRA {
	ras := make([]sentRA, 0, len(secs))
	for _, s := range secs {
		ras = append(ras, sentRA{time.Duration(s * float64(time.Second)), raLifetime})
	}
	return ras
}

// runAdvertise runs advertise for the given time and stops it; act runs
// at its time into the run.
func runAdvertise(t *testing.T, total time.Duration, act func(rs, up chan struct{})) []sentRA {
	t.Helper()
	got := recordRAs(t)
	stop, done := make(chan struct{}), make(chan struct{})
	rs, up := make(chan struct{}, 1), make(chan struct{}, 1)
	go func() {
		defer close(done)
		advertise("eth0", stop, rs, up)
	}()
	start := time.Now()
	if act != nil {
		act(rs, up)
	}
	time.Sleep(total - time.Since(start))
	close(stop)
	<-done
	return got()
}

// Zebra's fast start (zebra/rtadv.c rtadv_timer): 4 fast RAs and the
// first regular one, 1 s apart, then one every 10 s; the end is an RA
// with router lifetime 0.
func TestRAFastStartLikeZebra(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got := runAdvertise(t, 25*time.Second, nil)
		want := append(regular(0, 1, 2, 3, 4, 14, 24), sentRA{25 * time.Second, 0})
		if !slices.Equal(got, want) {
			t.Fatalf("RAs %v, want %v", got, want)
		}
	})
}

// Carrier back starts the fast RAs again, like zebra rtadv_if_up.
func TestRACarrierUpRestartsFastStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got := runAdvertise(t, 50*time.Second, func(_, up chan struct{}) {
			time.Sleep(30 * time.Second)
			up <- struct{}{}
		})
		want := append(regular(0, 1, 2, 3, 4, 14, 24, 30, 31, 32, 33, 34, 44), sentRA{50 * time.Second, 0})
		if !slices.Equal(got, want) {
			t.Fatalf("RAs %v, want %v", got, want)
		}
	})
}

// A Router Solicitation gets an RA after a random delay of at most
// MAX_RA_DELAY_TIME, not before MIN_DELAY_BETWEEN_RAS after the last RA,
// and not at all when an RA is due earlier (RFC 4861 section 6.2.6). The
// regular RAs follow the answer.
func TestRASolicitationAnswer(t *testing.T) {
	for _, tt := range []struct {
		name     string
		rsAt     time.Duration
		from, to time.Duration // the answer, empty for none
	}{
		{"after the fast RAs", 10 * time.Second, 10 * time.Second, 10*time.Second + maxRADelay},
		{"rate limited after the last RA at 4 s", 5 * time.Second, 7 * time.Second, 7*time.Second + maxRADelay},
		{"during the fast RAs", 2500 * time.Millisecond, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				got := runAdvertise(t, 21*time.Second, func(rs, _ chan struct{}) {
					time.Sleep(tt.rsAt)
					rs <- struct{}{}
				})
				fast := regular(0, 1, 2, 3, 4)
				if !slices.Equal(got[:5], fast) {
					t.Fatalf("RAs %v, want %v first", got, fast)
				}
				rest := got[5 : len(got)-1]
				if tt.to == 0 {
					if !slices.Equal(rest, regular(14)) {
						t.Fatalf("RAs after the fast ones %v, want only the regular one at 14 s", rest)
					}
					return
				}
				if len(rest) != 2 || rest[0].at < tt.from || rest[0].at > tt.to || rest[1].at != rest[0].at+raInterval {
					t.Fatalf("RAs after the fast ones %v, want the answer in [%s, %s] and the next one 10 s later", rest, tt.from, tt.to)
				}
			})
		})
	}
}

// The schedule with a fixed random delay.
func TestRAScheduleSolicited(t *testing.T) {
	t0 := time.Unix(1000, 0)
	delay := 200 * time.Millisecond
	for _, tt := range []struct {
		name       string
		last, next time.Duration // after t0
		rs         time.Duration
		want       time.Duration
	}{
		{"delay", 0, 10 * time.Second, 5 * time.Second, 5*time.Second + delay},
		{"rate limit", 0, 10 * time.Second, time.Second, minDelayBetweenRAs + delay},
		{"due earlier", 0, 3 * time.Second, 2900 * time.Millisecond, 3 * time.Second},
	} {
		s := raSchedule{last: t0.Add(tt.last), next: t0.Add(tt.next)}
		s.solicited(t0.Add(tt.rs), delay)
		if got := s.next.Sub(t0); got != tt.want {
			t.Errorf("%s: next RA at %s, want %s", tt.name, got, tt.want)
		}
	}
}

// RFC 4861 section 6.1.1 checks of a Router Solicitation.
func TestValidRS(t *testing.T) {
	ll := &net.IPAddr{IP: net.ParseIP("fe80::1")}
	unspec := &net.IPAddr{IP: net.IPv6unspecified}
	hop := func(h int) *ipv6.ControlMessage { return &ipv6.ControlMessage{HopLimit: h} }
	rs := []byte{133, 0, 0, 0, 0, 0, 0, 0}
	sllao := append(slices.Clone(rs), 1, 1, 2, 0, 0, 0, 0, 1)
	other := append(slices.Clone(rs), 14, 1, 0, 0, 0, 0, 0, 0)
	for _, tt := range []struct {
		name string
		b    []byte
		cm   *ipv6.ControlMessage
		src  net.Addr
		want bool
	}{
		{"plain", rs, hop(255), ll, true},
		{"source link-layer address", sllao, hop(255), ll, true},
		{"other option from ::", other, hop(255), unspec, true},
		{"from ::", rs, hop(255), unspec, true},
		{"forwarded", rs, hop(254), ll, false},
		{"no hop limit", rs, nil, ll, false},
		{"code 1", []byte{133, 1, 0, 0, 0, 0, 0, 0}, hop(255), ll, false},
		{"short", rs[:7], hop(255), ll, false},
		{"not an RS", []byte{134, 0, 0, 0, 0, 0, 0, 0}, hop(255), ll, false},
		{"source link-layer address from ::", sllao, hop(255), unspec, false},
		{"option length 0", append(slices.Clone(rs), 1, 0, 0, 0, 0, 0, 0, 0), hop(255), ll, false},
		{"option past the end", append(slices.Clone(rs), 1, 2, 0, 0, 0, 0, 0, 0), hop(255), ll, false},
	} {
		if got := validRS(tt.b, tt.cm, tt.src); got != tt.want {
			t.Errorf("%s: validRS = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Only a NIC that had no carrier and gets it signals; other NICs, IFF_UP
// alone and repeated updates do not.
func TestRelayCarrierUp(t *testing.T) {
	updates := make(chan netlink.LinkUpdate, 8)
	up := map[string]chan struct{}{"eth0": make(chan struct{}, 1), "eth1": make(chan struct{}, 1)}
	carrier := map[string]bool{"eth0": true, "eth1": false}
	for _, u := range []netlink.LinkUpdate{
		linkUpdate("eth0", unix.IFF_UP|unix.IFF_LOWER_UP), // no change
		linkUpdate("eth9", unix.IFF_UP|unix.IFF_LOWER_UP), // not an RA NIC
		{},
		linkUpdate("eth1", unix.IFF_UP), // still no carrier
		linkUpdate("eth1", unix.IFF_UP|unix.IFF_LOWER_UP), // back
		linkUpdate("eth0", unix.IFF_UP),                   // lost
	} {
		updates <- u
	}
	close(updates)
	relayCarrierUp(updates, carrier, up)
	if len(up["eth0"]) != 0 || len(up["eth1"]) != 1 {
		t.Fatalf("signals eth0 %d eth1 %d, want 0 and 1", len(up["eth0"]), len(up["eth1"]))
	}
	if carrier["eth0"] || !carrier["eth1"] {
		t.Fatalf("carrier %v, want eth0 off and eth1 on", carrier)
	}
}
