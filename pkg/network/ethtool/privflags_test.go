//go:build linux

package ethtool

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// bitset builds a verbose bitset reply like the kernel's
// ethnl_put_bitset32: bits with their names, VALUE on the set ones.
func bitset(noMask bool, bits ...Flag) []byte {
	list := nl.NewRtAttr(unix.ETHTOOL_A_BITSET_BITS|unix.NLA_F_NESTED, nil)
	for i, f := range bits {
		b := nl.NewRtAttr(unix.ETHTOOL_A_BITSET_BITS_BIT|unix.NLA_F_NESTED, nil)
		b.AddRtAttr(unix.ETHTOOL_A_BITSET_BIT_INDEX, nl.Uint32Attr(uint32(i)))
		b.AddRtAttr(unix.ETHTOOL_A_BITSET_BIT_NAME, nl.ZeroTerminated(f.Name))
		if f.On && !noMask {
			b.AddRtAttr(unix.ETHTOOL_A_BITSET_BIT_VALUE, nil)
		}
		list.AddChild(b)
	}
	set := nl.NewRtAttr(unix.ETHTOOL_A_PRIVFLAGS_FLAGS|unix.NLA_F_NESTED, nil)
	if noMask {
		set.AddRtAttr(unix.ETHTOOL_A_BITSET_NOMASK, nil)
	}
	set.AddRtAttr(unix.ETHTOOL_A_BITSET_SIZE, nl.Uint32Attr(uint32(len(bits))))
	set.AddChild(list)
	return set.Serialize()[unix.SizeofRtAttr:]
}

func TestParseBitset(t *testing.T) {
	want := []Flag{{"link-down-on-close", false}, {"fw-lldp-agent", true}, {"channel-inline-flow-director", false}}
	got, err := parseBitset(bitset(false, want...))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBitset = %+v, %v; want %+v", got, err, want)
	}
	// Without a mask the kernel lists only the set bits.
	got, err = parseBitset(bitset(true, Flag{"disable-fw-lldp", true}))
	if err != nil || !reflect.DeepEqual(got, []Flag{{"disable-fw-lldp", true}}) {
		t.Fatalf("parseBitset nomask = %+v, %v", got, err)
	}
}

// The set request names one bit, has VALUE only to turn it on and no
// NOMASK, so the kernel leaves the other flags as they are.
func TestFlagBitset(t *testing.T) {
	for _, on := range []bool{true, false} {
		b := flagBitset("fw-lldp-agent", on).Serialize()
		attrs, err := parse(b)
		if err != nil || len(attrs) != 1 || attrs[0].typ != unix.ETHTOOL_A_PRIVFLAGS_FLAGS {
			t.Fatalf("on=%v: attrs %+v, %v", on, attrs, err)
		}
		got, err := parseBitset(attrs[0].value)
		if err != nil || !reflect.DeepEqual(got, []Flag{{"fw-lldp-agent", on}}) {
			t.Fatalf("on=%v: bitset = %+v, %v", on, got, err)
		}
		inner, _ := parse(attrs[0].value)
		for _, a := range inner {
			if a.typ == unix.ETHTOOL_A_BITSET_NOMASK {
				t.Fatalf("on=%v: the request has NOMASK and would clear the other flags", on)
			}
		}
	}
}

// fakeNIC is a NIC with private flags for the flag seams.
type fakeNIC struct {
	flags map[string]bool
	sets  []string
}

func fakeNICs(t *testing.T, nics map[string]*fakeNIC) {
	t.Helper()
	og, os, oi, on := getFlags, setFlag, ifIndex, ifName
	t.Cleanup(func() {
		getFlags, setFlag, ifIndex, ifName = og, os, oi, on
		changed.Lock()
		changed.list = nil
		changed.Unlock()
	})
	index := map[string]int{}
	names := map[int]string{}
	n := 0
	for name := range nics {
		n++
		index[name], names[n] = n, name
	}
	getFlags = func(name string) ([]Flag, error) {
		out := make([]Flag, 0, len(nics[name].flags))
		for k, v := range nics[name].flags {
			out = append(out, Flag{k, v})
		}
		return out, nil
	}
	setFlag = func(name, flag string, on bool) error {
		nic := nics[name]
		nic.flags[flag] = on
		nic.sets = append(nic.sets, fmt.Sprintf("%s=%v", flag, on))
		return nil
	}
	ifIndex = func(name string) (int, error) { return index[name], nil }
	ifName = func(i int) (string, error) { return names[i], nil }
}

// BOOTy stops the agent with the driver's flag and puts back the value it
// found; a flag that is already off is neither set nor restored.
func TestDisableFirmwareLLDP(t *testing.T) {
	ice := &fakeNIC{flags: map[string]bool{"fw-lldp-agent": true, "link-down-on-close": false}}
	i40e := &fakeNIC{flags: map[string]bool{"disable-fw-lldp": false}}
	off := &fakeNIC{flags: map[string]bool{"fw-lldp-agent": false}}
	fakeNICs(t, map[string]*fakeNIC{"ice0": ice, "i40e0": i40e, "off0": off})
	for _, tt := range []struct{ nic, driver, flag string }{
		{"ice0", "ice", "fw-lldp-agent"}, {"i40e0", "i40e", "disable-fw-lldp"}, {"off0", "ice", "fw-lldp-agent"}, {"mlx0", "mlx5_core", ""},
	} {
		if flag, err := disableFirmwareLLDP(tt.nic, tt.driver); err != nil || flag != tt.flag {
			t.Fatalf("%s: flag %q, %v", tt.nic, flag, err)
		}
	}
	if ice.flags["fw-lldp-agent"] || !i40e.flags["disable-fw-lldp"] || len(off.sets) != 0 {
		t.Fatalf("after disable: ice %v, i40e %v, already off set %v", ice.flags, i40e.flags, off.sets)
	}
	n, err := RestoreFirmwareLLDP()
	if err != nil || n != 2 || !ice.flags["fw-lldp-agent"] || i40e.flags["disable-fw-lldp"] || len(off.sets) != 0 {
		t.Fatalf("restore: %d, %v; ice %v, i40e %v, already off set %v", n, err, ice.flags, i40e.flags, off.sets)
	}
	if n, _ := RestoreFirmwareLLDP(); n != 0 {
		t.Fatalf("second restore changed %d flags", n)
	}
	// A driver without the flag (too old) fails and changes nothing.
	old := &fakeNIC{flags: map[string]bool{}}
	fakeNICs(t, map[string]*fakeNIC{"old0": old})
	if _, err := disableFirmwareLLDP("old0", "ice"); err == nil || len(old.sets) != 0 {
		t.Fatalf("missing flag: %v, sets %v", err, old.sets)
	}
}

func TestDriver(t *testing.T) {
	orig := sysClassNet
	t.Cleanup(func() { sysClassNet = orig })
	sysClassNet = t.TempDir()
	dev := filepath.Join(sysClassNet, "ens1f0", "device")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../bus/pci/drivers/ice", filepath.Join(dev, "driver")); err != nil {
		t.Fatal(err)
	}
	if got := Driver("ens1f0"); got != "ice" {
		t.Fatalf("Driver = %q, want ice", got)
	}
	if got := Driver("veth0"); got != "" {
		t.Fatalf("Driver of a NIC without device = %q", got)
	}
}

// The kernel accepts the request: lo has no private flags, so a well
// formed request ends with EOPNOTSUPP, a malformed one with EINVAL.
func TestPrivateFlagsKernel(t *testing.T) {
	if _, err := familyID(); err != nil {
		t.Skipf("no ethtool netlink family: %v", err)
	}
	_, err := PrivateFlags("lo")
	if !IsNotSupported(err) {
		t.Fatalf("PrivateFlags(lo) = %v, want EOPNOTSUPP", err)
	}
}
