package netname

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeSysfs builds the sysfs entries net_id reads.
type fakeSysfs struct {
	t    *testing.T
	root string
}

func (f fakeSysfs) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f fakeSysfs) link(rel, target string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.root, target), p); err != nil {
		f.t.Fatal(err)
	}
}

// pciDev creates a PCI device below devices/ and its bus/pci/devices link.
// multifunction sets the bit in the configuration header.
func (f fakeSysfs) pciDev(path string, multifunction bool) {
	cfg := make([]byte, 64)
	if multifunction {
		cfg[0x0e] = 0x80
	}
	f.write(filepath.Join("devices", path, "config"), string(cfg))
	f.link(filepath.Join("bus", "pci", "devices", filepath.Base(path)), filepath.Join("devices", path))
}

// nic creates class/net/<name> pointing at a device directory.
func (f fakeSysfs) nic(name, devicePath, typ string) {
	f.write(filepath.Join("class", "net", name, "type"), typ+"\n")
	if devicePath != "" {
		f.link(filepath.Join("class", "net", name, "device"), filepath.Join("devices", devicePath))
	}
}

func TestNames(t *testing.T) {
	f := fakeSysfs{t: t, root: t.TempDir()}

	// QEMU q35: virtio-net behind a pcie-root-port whose slot is "0-1".
	f.pciDev("pci0000:00/0000:00:1c.0", true)
	f.pciDev("pci0000:00/0000:00:1c.0/0000:01:00.0", false)
	f.write("devices/pci0000:00/0000:00:1c.0/0000:01:00.0/virtio1/x", "")
	f.write("bus/pci/slots/0-1/address", "0000:01:00\n")
	f.nic("eth0", "pci0000:00/0000:00:1c.0/0000:01:00.0/virtio1", "1")

	// QEMU i440fx: ACPI hotplug slot 3.
	f.pciDev("pci0000:00/0000:00:03.0", false)
	f.write("devices/pci0000:00/0000:00:03.0/virtio0/x", "")
	f.write("bus/pci/slots/3/address", "0000:00:03\n")
	f.nic("eth1", "pci0000:00/0000:00:03.0/virtio0", "1")

	// Dual port ConnectX in switchdev mode in slot 1, second port.
	f.pciDev("pci0000:3a/0000:3a:00.0", false)
	f.pciDev("pci0000:3a/0000:3a:00.0/0000:3b:00.0", true)
	f.pciDev("pci0000:3a/0000:3a:00.0/0000:3b:00.1", true)
	f.write("bus/pci/slots/1/address", "0000:3b:00\n")
	f.nic("eth2", "pci0000:3a/0000:3a:00.0/0000:3b:00.1", "1")
	f.write("class/net/eth2/phys_port_name", "p1\n")

	// Onboard NIC with an ACPI index, second port by dev_port, domain 1.
	f.pciDev("pci0001:00/0001:02:00.0", false)
	f.write("devices/pci0001:00/0001:02:00.0/acpi_index", "2\n")
	f.nic("eth3", "pci0001:00/0001:02:00.0", "1")
	f.write("class/net/eth3/dev_port", "1\n")

	// SR-IOV VF, InfiniBand and a virtual link get no names.
	f.pciDev("pci0000:3a/0000:3a:00.0/0000:3b:00.2", false)
	f.write("devices/pci0000:3a/0000:3a:00.0/0000:3b:00.2/physfn/x", "")
	f.nic("eth4", "pci0000:3a/0000:3a:00.0/0000:3b:00.2", "1")
	f.nic("ib0", "pci0000:3a/0000:3a:00.0/0000:3b:00.0", "32")
	f.nic("dummy0", "", "1")

	tests := map[string][]string{
		"eth0":   {"enp1s0"},
		"eth1":   {"ens3", "enp0s3"},
		"eth2":   {"ens1f1np1", "enp59s0f1np1"},
		"eth3":   {"eno2d1", "enP1p2s0d1"},
		"eth4":   nil,
		"ib0":    nil,
		"dummy0": nil,
		"absent": nil,
	}
	for link, want := range tests {
		if got := Names(f.root, link); !reflect.DeepEqual(got, want) {
			t.Errorf("Names(%s) = %v, want %v", link, got, want)
		}
	}
}

func TestPlan(t *testing.T) {
	f := fakeSysfs{t: t, root: t.TempDir()}
	f.pciDev("pci0000:00/0000:00:1c.0/0000:01:00.0", false)
	f.nic("eth0", "pci0000:00/0000:00:1c.0/0000:01:00.0", "1")
	f.pciDev("pci0000:00/0000:00:1c.1/0000:02:00.0", false)
	f.nic("eth1", "pci0000:00/0000:00:1c.1/0000:02:00.0", "1")
	links := []string{"lo", "eth0", "eth1"}

	tests := []struct {
		name   string
		wanted []string
		want   map[string]string
	}{
		{name: "bm4x frr.conf names", wanted: []string{"enp1s0", "enp2s0"}, want: map[string]string{"eth0": "enp1s0", "eth1": "enp2s0"}},
		{name: "kernel names stay", wanted: []string{"eth0", "eth1"}, want: map[string]string{}},
		{name: "unknown name is left to the network setup", wanted: []string{"ens9", "enp2s0", "enp2s0"}, want: map[string]string{"eth1": "enp2s0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Plan(f.root, links, tt.wanted); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Plan() = %v, want %v", got, tt.want)
			}
		})
	}
}
