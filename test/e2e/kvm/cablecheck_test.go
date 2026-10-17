//go:build e2e

package kvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

// cablecheckNICs are the MACs of the two NICs of the cable check VMs.
var cablecheckNICs = []string{"52:54:00:12:34:56", "52:54:00:12:34:57"}

// cablecheckBoot boots BOOTy from the test initramfs with the vars.
func cablecheckBoot(t *testing.T, vars map[string]string) []string {
	t.Helper()
	return []string{
		"-kernel", findKernel(t), "-initrd", buildProvisionInitramfs(t, vars),
		"-append", "console=ttyS0 panic=1 net.ifnames=0",
	}
}

// runCableCheckVM boots BOOTy (boot args) with two virtio NICs whose
// traffic QEMU writes to a pcap file each (filter-dump). It stops the VM
// after hold, or earlier when BOOTy reboots (-no-reboot), and returns the
// serial output, whether the VM was still running at the end, and the
// pcap paths.
func runCableCheckVM(t *testing.T, boot []string, hold time.Duration) (out []byte, running bool, pcaps []string) {
	t.Helper()
	dir := t.TempDir()
	args := append([]string{"-m", "1024", "-nographic", "-no-reboot"}, boot...)
	for i, mac := range cablecheckNICs {
		pcap := filepath.Join(dir, fmt.Sprintf("nic%d.pcap", i))
		pcaps = append(pcaps, pcap)
		// restrict=on: the user backend only sees the frames, the guest
		// gets no DHCP server or router from it.
		args = append(args,
			"-netdev", fmt.Sprintf("user,id=n%d,restrict=on", i),
			"-device", fmt.Sprintf("virtio-net-pci,netdev=n%d,mac=%s", i, mac),
			"-object", fmt.Sprintf("filter-dump,id=f%d,netdev=n%d,file=%s", i, i, pcap))
	}
	args = append(args, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)
	ctx, cancel := context.WithTimeout(context.Background(), hold)
	defer cancel()
	out, _ = exec.CommandContext(ctx, "qemu-system-x86_64", args...).CombinedOutput()
	return out, ctx.Err() != nil, pcaps
}

// cablecheckTraffic is what one NIC sent.
type cablecheckTraffic struct {
	lldp      int      // LLDP frames with the bm4x identity of the NIC
	otherLLDP []string // LLDP frames with another identity
	ipv4      int      // IPv4 or ARP frames (DHCP among them)
	globalV6  []string // IPv6 sources that are not link-local or ::
}

func readCableCheckPcap(t *testing.T, path, mac string) cablecheckTraffic {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	r, err := pcapgo.NewReader(f)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var tr cablecheckTraffic
	for {
		data, _, err := r.ReadPacketData()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return tr
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
		eth, _ := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
		if eth == nil || eth.SrcMAC.String() != mac {
			continue // only what the guest sent on this NIC
		}
		switch eth.EthernetType {
		case layers.EthernetTypeLinkLayerDiscovery:
			l, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscovery).(*layers.LinkLayerDiscovery)
			info, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
			if l != nil && info != nil && l.PortID.Subtype == layers.LLDPPortIDSubtypeMACAddr &&
				net.HardwareAddr(l.PortID.ID).String() == mac && info.SysName == "bare-minimal" {
				tr.lldp++
			} else {
				tr.otherLLDP = append(tr.otherLLDP, fmt.Sprintf("%x", data))
			}
		case layers.EthernetTypeIPv4, layers.EthernetTypeARP:
			tr.ipv4++
		case layers.EthernetTypeIPv6:
			ip, _ := pkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
			if ip != nil && !ip.SrcIP.IsLinkLocalUnicast() && !ip.SrcIP.IsUnspecified() {
				tr.globalV6 = append(tr.globalV6, ip.SrcIP.String())
			}
		}
	}
}

// NETWORK_MODE=links brings both NICs up with IPv6 link-local only: no
// DHCP and no IPv4 at all, no global IPv6 address, and with
// LLDP_PROFILE=bm4x-cablecheck LLDP frames with the bm4x identity on every
// NIC.
func TestLinksModeLLDPQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)

	vars := map[string]string{
		"MODE":         "check",
		"NETWORK_MODE": "links",
		"LLDP_PROFILE": "bm4x-cablecheck",
	}
	out, _, pcaps := runCableCheckVM(t, cablecheckBoot(t, vars), 3*time.Minute)
	if !strings.Contains(string(out), "using links network mode") {
		t.Fatalf("links mode not used. tail:\n%s", tail(out, 4000))
	}
	for i, mac := range cablecheckNICs {
		tr := readCableCheckPcap(t, pcaps[i], mac)
		if tr.lldp == 0 || len(tr.otherLLDP) > 0 {
			t.Errorf("NIC %s: %d bm4x LLDP frames, other LLDP frames %v", mac, tr.lldp, tr.otherLLDP)
		}
		if tr.ipv4 > 0 || len(tr.globalV6) > 0 {
			t.Errorf("NIC %s sent %d IPv4/ARP frames and IPv6 from %v, want link-local only", mac, tr.ipv4, tr.globalV6)
		}
	}
	if t.Failed() {
		t.Logf("serial tail:\n%s", tail(out, 4000))
	}
}

// checkISOVars reads hack/check-iso/vars, the vars of the cable check ISO.
func checkISOVars(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "hack", "check-iso", "vars"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && !strings.HasPrefix(k, "#") {
			vars[k] = v
		}
	}
	return vars
}

// requireHeldCableCheck checks a VM that ran the cable check ISO vars for
// hold: it still ran at the end (CHECK_HOLD=until-poweroff, no reboot),
// and each NIC sent the 4 fast LLDP frames and the next one 30 s later
// with the bm4x identity, and nothing on IPv4.
func requireHeldCableCheck(t *testing.T, out []byte, running bool, pcaps []string) {
	t.Helper()
	if !running || !strings.Contains(string(out), "checks done, holding until power off") {
		t.Fatalf("VM running %v at the end, want it holding until power off. tail:\n%s", running, tail(out, 4000))
	}
	for i, mac := range cablecheckNICs {
		tr := readCableCheckPcap(t, pcaps[i], mac)
		if tr.lldp < 5 || len(tr.otherLLDP) > 0 || tr.ipv4 > 0 || len(tr.globalV6) > 0 {
			t.Errorf("NIC %s: %d bm4x LLDP frames (want 5 or more), other LLDP %v, %d IPv4/ARP, IPv6 from %v",
				mac, tr.lldp, tr.otherLLDP, tr.ipv4, tr.globalV6)
		}
	}
	if t.Failed() {
		t.Logf("serial tail:\n%s", tail(out, 4000))
	}
}

// The vars of the cable check ISO (hack/check-iso/vars): BOOTy waits up to
// 90 s for LLDP neighbors (there are none here), runs the checks and keeps
// the links and LLDP up until the VM is powered off; the test powers it off
// after 130 s.
func TestCheckISOVarsHoldQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	out, running, pcaps := runCableCheckVM(t, cablecheckBoot(t, checkISOVars(t)), 130*time.Second)
	requireHeldCableCheck(t, out, running, pcaps)
}

// BOOTY_CHECK_ISO is a booty-check.iso built with make check-iso: booted
// from CD with nothing else, it runs the cable check and holds.
func TestCheckISOQEMU(t *testing.T) {
	iso := os.Getenv("BOOTY_CHECK_ISO")
	if iso == "" {
		t.Skip("BOOTY_CHECK_ISO not set (make check-iso)")
	}
	qemuAvailable(t)
	boot := []string{"-cdrom", iso, "-boot", "d"}
	out, running, pcaps := runCableCheckVM(t, boot, 150*time.Second)
	requireHeldCableCheck(t, out, running, pcaps)
}
