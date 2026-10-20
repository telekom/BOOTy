//go:build linux

package lldp

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// The frame carries what systemd-networkd sends with EmitLLDP=yes
// (sd-lldp-tx.c): nearest-bridge destination, the machine ID as locally
// assigned chassis ID, the interface name as port ID, TTL 120, the
// hostname, the pretty hostname and the capabilities.
func TestFrameLikeNetworkd(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	b, err := Frame("ens1f0np0", mac, &Info{MachineID: "0123456789abcdef0123456789abcdef", SystemName: "host1", PrettyHostname: "Host 1"})
	if err != nil {
		t.Fatal(err)
	}
	pkt := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	eth, _ := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	l, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	info, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if eth == nil || l == nil || info == nil {
		t.Fatalf("frame does not decode: %v", pkt.ErrorLayer())
	}
	if eth.DstMAC.String() != "01:80:c2:00:00:0e" || eth.SrcMAC.String() != mac.String() {
		t.Errorf("ethernet %s -> %s", eth.SrcMAC, eth.DstMAC)
	}
	if l.ChassisID.Subtype != layers.LLDPChassisIDSubTypeLocal || string(l.ChassisID.ID) != "0123456789abcdef0123456789abcdef" {
		t.Errorf("chassis = %v %q", l.ChassisID.Subtype, l.ChassisID.ID)
	}
	if l.PortID.Subtype != layers.LLDPPortIDSubtypeIfaceName || string(l.PortID.ID) != "ens1f0np0" || l.TTL != 120 {
		t.Errorf("port = %v %q, TTL %d", l.PortID.Subtype, l.PortID.ID, l.TTL)
	}
	if info.SysName != "host1" || info.SysDescription != "Host 1" {
		t.Errorf("system name %q description %q", info.SysName, info.SysDescription)
	}
	var caps []byte
	for _, v := range l.Values {
		if v.Type == layers.LLDPTLVSysCapabilities {
			caps = v.Value
		}
	}
	if len(caps) != 4 || binary.BigEndian.Uint16(caps) != 0x94 || binary.BigEndian.Uint16(caps[2:]) != 0x80 {
		t.Errorf("capabilities = %x, want supported 0094 enabled 0080", caps)
	}

	// Without a machine ID the NIC MAC is the chassis ID; without a pretty
	// hostname there is no system description.
	b, err = Frame("eth0", mac, &Info{SystemName: "host1"})
	if err != nil {
		t.Fatal(err)
	}
	pkt = gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	l, _ = pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	info, _ = pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if l.ChassisID.Subtype != layers.LLDPChassisIDSubTypeMACAddr || net.HardwareAddr(l.ChassisID.ID).String() != mac.String() || info.SysDescription != "" {
		t.Errorf("fallback chassis %v %x, description %q", l.ChassisID.Subtype, l.ChassisID.ID, info.SysDescription)
	}
	// networkd sends no system description TLV at all without a pretty
	// hostname, not an empty one.
	for _, v := range l.Values {
		if v.Type == layers.LLDPTLVSysDescription {
			t.Errorf("fallback frame has a system description TLV %q", v.Value)
		}
	}
}

// BOOTy creates a machine ID for the boot like systemd (32 hex digits, a
// version 4 UUID), keeps it under /run (never /etc) and uses it again.
// When it cannot write it, there is no machine ID and the frames fall
// back to the MAC.
func TestMachineIDPerBoot(t *testing.T) {
	if !strings.HasPrefix(machineIDPath, "/run/") {
		t.Fatalf("machine ID path %s, want one under /run", machineIDPath)
	}
	orig := machineIDPath
	t.Cleanup(func() { machineIDPath = orig })
	machineIDPath = filepath.Join(t.TempDir(), "booty", "machine-id")
	id := machineID()
	if !regexp.MustCompile(`^[0-9a-f]{12}4[0-9a-f]{3}[89ab][0-9a-f]{15}$`).MatchString(id) {
		t.Fatalf("machine ID %q, want 32 hex digits of a version 4 UUID", id)
	}
	if again := machineID(); again != id {
		t.Fatalf("machine ID changed: %q then %q", id, again)
	}
	if b, err := os.ReadFile(machineIDPath); err != nil || string(b) != id+"\n" {
		t.Fatalf("machine-id file = %q (%v)", b, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	machineIDPath = filepath.Join(file, "machine-id") // its directory is a file
	if got := machineID(); got != "" {
		t.Fatalf("machine ID %q although its directory can not be created, want none", got)
	}
	machineIDPath = t.TempDir() // a directory: the write fails, also as root
	if got := machineID(); got != "" {
		t.Fatalf("machine ID %q although it can not be written, want none", got)
	}
}

// The networkd profile (the default) sends the bytes that BOOTy sent
// before the identity profiles existed (#698, e136315e).
func TestFrameNetworkdGolden(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	for _, tt := range []struct {
		ifname string
		info   Info
		want   string
	}{
		{"ens1f0np0", Info{MachineID: "0123456789abcdef0123456789abcdef", SystemName: "host1", PrettyHostname: "Host 1"},
			"0180c200000e02000000000188cc0221073031323334353637383961626364656630313233343536373839616263646566040a05656e733166306e7030060200780a05686f7374310c06486f737420310e04009400800000"},
		{"eth0", Info{SystemName: "host1"},
			"0180c200000e02000000000188cc02070402000000000104050565746830060200780a05686f7374310e040094008000000000000000000000000000"},
	} {
		for _, profile := range []string{"", ProfileNetworkd} {
			info := tt.info
			info.Profile = profile
			info.Announce = map[string]string{"": "", ProfileNetworkd: AnnounceParity}[profile]
			info.System = testSystem // only standard and full send it
			b, err := Frame(tt.ifname, mac, &info)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(b); got != tt.want {
				t.Errorf("%s profile %q:\n got  %s\n want %s", tt.ifname, profile, got, tt.want)
			}
		}
	}
}

// The bm4x-cablecheck profile sends the NIC MAC as port ID (subtype 3),
// the interface name as port description and the system name
// "bare-minimal"; chassis, TTL and capabilities stay as networkd sends them.
func TestFrameBM4XCableCheck(t *testing.T) {
	mac, _ := net.ParseMAC("3c:fd:fe:a1:b2:c3")
	b, err := Frame("ens1f0np0", mac, &Info{MachineID: "0123456789abcdef0123456789abcdef", SystemName: "host1", Profile: ProfileBM4XCableCheck})
	if err != nil {
		t.Fatal(err)
	}
	pkt := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	l, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	info, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if l == nil || info == nil {
		t.Fatalf("frame does not decode: %v", pkt.ErrorLayer())
	}
	if l.PortID.Subtype != layers.LLDPPortIDSubtypeMACAddr || !bytes.Equal(l.PortID.ID, mac) {
		t.Errorf("port ID = %v %x, want MAC %s", l.PortID.Subtype, l.PortID.ID, mac)
	}
	if info.SysName != "bare-minimal" || info.PortDescription != "ens1f0np0" {
		t.Errorf("system name %q, port description %q", info.SysName, info.PortDescription)
	}
	if l.ChassisID.Subtype != layers.LLDPChassisIDSubTypeLocal || l.TTL != 120 {
		t.Errorf("chassis %v, TTL %d", l.ChassisID.Subtype, l.TTL)
	}
}

// network.lldp.portIDFormat selects the port ID of the bm4x-cablecheck
// profile; "" is "mac" byte for byte.
func TestFrameBM4XPortIDFormats(t *testing.T) {
	mac, _ := net.ParseMAC("3c:fd:fe:a1:b2:c3")
	base := Info{MachineID: "0123456789abcdef0123456789abcdef", SystemName: "host1", Profile: ProfileBM4XCableCheck}
	def, err := Frame("ens1f0np0", mac, &base)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		format  string
		subtype layers.LLDPPortIDSubType
		id      []byte
	}{
		{PortIDMAC, layers.LLDPPortIDSubtypeMACAddr, mac},
		{PortIDMACStringColon, layers.LLDPPortIDSubtypeLocal, []byte("3c:fd:fe:a1:b2:c3")},
		{PortIDMACStringDotted, layers.LLDPPortIDSubtypeLocal, []byte("3cfd.fea1.b2c3")},
		{PortIDIfname, layers.LLDPPortIDSubtypeIfaceName, []byte("ens1f0np0")},
	} {
		info := base
		info.PortIDFormat = tt.format
		b, err := Frame("ens1f0np0", mac, &info)
		if err != nil {
			t.Fatal(err)
		}
		pkt := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
		l, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
		d, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
		if l == nil || d == nil {
			t.Fatalf("%s: frame does not decode: %v", tt.format, pkt.ErrorLayer())
		}
		if l.PortID.Subtype != tt.subtype || !bytes.Equal(l.PortID.ID, tt.id) {
			t.Errorf("%s: port ID = %v %q, want %v %q", tt.format, l.PortID.Subtype, l.PortID.ID, tt.subtype, tt.id)
		}
		if d.SysName != BM4XSystemName || d.PortDescription != "ens1f0np0" {
			t.Errorf("%s: system name %q, port description %q", tt.format, d.SysName, d.PortDescription)
		}
		if tt.format == PortIDMAC && !bytes.Equal(b, def) {
			t.Errorf("format mac differs from the default")
		}
	}
}

func TestDottedMAC(t *testing.T) {
	mac, _ := net.ParseMAC("00:1B:54:C2:00:0A")
	if got := dottedMAC(mac); got != "001b.54c2.000a" {
		t.Fatalf("dottedMAC = %q", got)
	}
}

// IEEE 802.1AB shutdown LLDPDU: TTL 0 and only the mandatory TLVs.
func TestShutdownFrame(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	b, err := ShutdownFrame("eth0", mac, &Info{MachineID: "0123456789abcdef0123456789abcdef", SystemName: "host1"})
	if err != nil {
		t.Fatal(err)
	}
	pkt := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	l, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	if l == nil {
		t.Fatalf("frame does not decode: %v", pkt.ErrorLayer())
	}
	if l.TTL != 0 || len(l.Values) != 0 || string(l.PortID.ID) != "eth0" {
		t.Errorf("shutdown frame TTL %d, %d optional TLVs, port %q; want TTL 0, none, eth0", l.TTL, len(l.Values), l.PortID.ID)
	}
	// The shutdown LLDPDU names the same port as the frames before it: the
	// neighbor keys its entry by chassis and port ID.
	bm4x := &Info{MachineID: "m", Profile: ProfileBM4XCableCheck}
	b, err = ShutdownFrame("eth0", mac, bm4x)
	if err != nil {
		t.Fatal(err)
	}
	l, _ = gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default).Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
	if l == nil || l.PortID.Subtype != layers.LLDPPortIDSubtypeMACAddr || !bytes.Equal(l.PortID.ID, mac) {
		t.Errorf("bm4x shutdown frame port ID %+v, want the NIC MAC like its normal frames", l)
	}
}

// The sender ends with the shutdown LLDPDU when it stops.
func TestEmitEndsWithShutdownFrame(t *testing.T) {
	orig, origSys := sendFrame, sysClassNet
	t.Cleanup(func() { sendFrame, sysClassNet = orig, origSys })
	sysClassNet = t.TempDir()
	setCarrier(t, "eth0", true, 1)
	ttls := make(chan uint16, 16)
	sendFrame = func(iface string, info *Info, build func(string, net.HardwareAddr, *Info) ([]byte, error)) ([]byte, error) {
		b, err := build(iface, net.HardwareAddr{2, 0, 0, 0, 0, 1}, info)
		if err != nil {
			return nil, err
		}
		l, _ := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default).Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
		if l != nil {
			ttls <- l.TTL
		}
		return b, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := Emit(ctx, []string{"eth0"}, &Info{MachineID: "m", SystemName: "host1"}, slog.Default())
	if got := <-ttls; got != txTTL {
		t.Fatalf("first frame TTL %d, want %d", got, txTTL)
	}
	cancel()
	wait()
	close(ttls)
	var last uint16 = txTTL
	for v := range ttls {
		last = v
	}
	if last != 0 {
		t.Fatalf("last frame TTL %d, want 0 (shutdown LLDPDU)", last)
	}
}

// The transmit timer of networkd 249 (networkd-lldp-tx.c): a carrier gain
// starts 4 frames 1 s apart, then one every 30 s; without carrier no frame
// goes out and none counts.
func TestFastInit(t *testing.T) {
	// The first poll with carrier sends, whatever the change count.
	var first fastInit
	if !first.due(time.Unix(1000, 0), true, 0) {
		t.Fatal("no frame at the first poll with carrier")
	}
	var f fastInit
	now := time.Unix(1000, 0)
	// poll advances the clock by d, polls with the carrier state and sends
	// a frame when one is due; it reports whether one was due.
	poll := func(d time.Duration, up bool, changes uint64) bool {
		now = now.Add(d)
		if !f.due(now, up, changes) {
			return false
		}
		f.sent(now)
		return true
	}
	// frames polls every second for 40 s and returns the seconds at which
	// frames went out.
	frames := func(up bool, changes uint64) []int {
		var at []int
		for i := range 40 {
			if poll(time.Second, up, changes) {
				at = append(at, i)
			}
		}
		return at
	}
	fastStart := []int{0, 1, 2, 3, 33}
	if got := frames(false, 1); len(got) != 0 {
		t.Fatalf("frames without carrier at %v, want none", got)
	}
	if got := frames(true, 2); !slices.Equal(got, fastStart) {
		t.Fatalf("after the carrier came up: frames at %v, want %v", got, fastStart)
	}
	// A carrier loss that a poll sees stops the frames and the counting;
	// the gain starts the fast frames again.
	if got := frames(false, 3); len(got) != 0 {
		t.Fatalf("frames after the carrier loss at %v, want none", got)
	}
	if got := frames(true, 4); !slices.Equal(got, fastStart) {
		t.Fatalf("after the carrier came back: frames at %v, want %v", got, fastStart)
	}
	// A flap between two polls (the carrier change count grew by 2) is a
	// carrier gain too.
	if got := frames(true, 6); !slices.Equal(got, fastStart) {
		t.Fatalf("after a flap between polls: frames at %v, want %v", got, fastStart)
	}
	// Without a change count (no carrier_changes file) a carrier loss
	// that a poll sees still starts the fast frames again.
	if got := frames(false, 6); len(got) != 0 {
		t.Fatalf("frames after the carrier loss at %v, want none", got)
	}
	if got := frames(true, 6); !slices.Equal(got, fastStart) {
		t.Fatalf("after the carrier came back with the same count: frames at %v, want %v", got, fastStart)
	}
	// The poll interval: at most 1 s, and the time to a due frame below it.
	if w := f.wait(now); w != txFastInterval {
		t.Fatalf("wait %v in the slow phase, want %v", w, txFastInterval)
	}
	f.next = now.Add(300 * time.Millisecond)
	if w := f.wait(now); w != 300*time.Millisecond {
		t.Fatalf("wait %v before a frame due in 300ms, want 300ms", w)
	}
}

// setCarrier writes the carrier and carrier_changes files of a NIC under
// sysClassNet, like the kernel.
func setCarrier(t *testing.T, iface string, up bool, changes int) {
	t.Helper()
	dir := filepath.Join(sysClassNet, iface)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	carrier := map[bool]string{false: "0\n", true: "1\n"}[up]
	if err := os.WriteFile(filepath.Join(dir, "carrier"), []byte(carrier), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "carrier_changes"), []byte(strconv.Itoa(changes)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// In every profile a NIC without carrier gets no frame, and its first
// frame goes out within a fast interval once the carrier is up: networkd
// starts sending on carrier gain and stops on carrier loss.
func TestEmitWaitsForCarrier(t *testing.T) {
	origSend, origSys := sendFrame, sysClassNet
	t.Cleanup(func() { sendFrame, sysClassNet = origSend, origSys })
	sysClassNet = t.TempDir()
	setCarrier(t, "eth0", false, 1)
	sent := make(chan struct{}, 16)
	sendFrame = func(iface string, info *Info, build func(string, net.HardwareAddr, *Info) ([]byte, error)) ([]byte, error) {
		b, err := build(iface, net.HardwareAddr{2, 0, 0, 0, 0, 1}, info)
		if err != nil {
			return nil, err
		}
		l, _ := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default).Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
		if l != nil && l.TTL != 0 { // not the shutdown LLDPDU
			sent <- struct{}{}
		}
		return b, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := Emit(ctx, []string{"eth0"}, &Info{MachineID: "m", SystemName: "host1"}, slog.Default())
	t.Cleanup(func() { cancel(); wait() })
	select {
	case <-sent:
		t.Fatal("frame sent on a NIC without carrier")
	case <-time.After(txFastInterval + txFastInterval/2):
	}
	setCarrier(t, "eth0", true, 2)
	select {
	case <-sent:
	case <-time.After(txFastInterval + txFastInterval/2):
		t.Fatal("no frame within a fast interval of the carrier")
	}
}
