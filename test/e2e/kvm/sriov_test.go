//go:build e2e

package kvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// inventorySink records the hardware inventory that BOOTy posts.
type inventorySink struct {
	url  string
	mu   sync.Mutex
	body []byte
}

func startInventorySink(t *testing.T) *inventorySink {
	t.Helper()
	s := &inventorySink{}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.body = body
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	s.url = fmt.Sprintf("http://10.0.2.2:%d/inventory", listener.Addr().(*net.TCPAddr).Port)
	return s
}

// sriovReport is the part of the inventory JSON that the test checks.
type sriovReport struct {
	NICs []struct {
		Name   string `json:"name"`
		Driver string `json:"driver"`
		SRIOV  *struct {
			Driver       string `json:"driver"`
			VendorID     string `json:"vendorId"`
			DeviceID     string `json:"deviceId"`
			SRIOVCapable bool   `json:"sriovCapable"`
			TotalVFs     int    `json:"sriovTotalVFs"`
			NumVFs       int    `json:"sriovNumVFs"`
			Eswitch      struct {
				Capability string `json:"capability"`
			} `json:"eswitch"`
		} `json:"sriov"`
	} `json:"nics"`
}

// igbSRIOVProfiles matches the igb PF: the VF limit of the 82576 (7) is
// checked against sriov_totalvfs, then two VFs with attributes are created
// and checked, and the handoff reset removes them again.
const igbSRIOVProfiles = `[{"name":"igb","match":{"vendorId":"8086","driver":"igb"},` +
	`"firmware":{"sriovEnabled":true,"maxVFs":7},` +
	`"runtime":{"numVFs":2,"vfDefaults":{"trust":true,"spoofChk":false},` +
	`"vfs":[{"index":1,"mac":"02:00:00:00:00:11","vlan":100,"qos":2}]}}]`

// TestSRIOVIgbProvisionQEMU provisions a q35 guest that has a QEMU igb NIC
// with SR-IOV behind a PCIe root port, next to the virtio NIC of the
// provisioning network. The inventory must report the igb PF with seven
// VFs, and configure-sriov must create two VFs with the profile attributes,
// check them through rtnetlink, and remove them before the handoff.
func TestSRIOVIgbProvisionQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)

	kernel := findKernel(t)
	real, err := filepath.EvalSymlinks(kernel)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	addKernelModules(t, files, productionTemplateKernelRelease(t, real), "igb")
	if _, ok := files["modules/igb.ko"]; !ok {
		t.Fatalf("igb module of the guest kernel not found; modules: %v", mapKeys(files))
	}

	inventory := startInventorySink(t)
	caprf := startFakeCAPRF(t, compressGzip(t, createChrootCapableTestDiskImage(t, 512)))
	disk := filepath.Join(t.TempDir(), "sriov-igb.qcow2")
	run(t, "create disk", "qemu-img", "create", "-f", "qcow2", disk, "2G")

	vars := caprf.nativeVars("provision")
	vars["IMAGE"] = caprf.baseURL + "/image.gz"
	vars["IMAGE_ALLOW_INSECURE_HTTP"] = "true"
	vars["DISK_DEVICE"] = "/dev/vda"
	vars["SUCCESS_ACTION"] = "poweroff"
	vars["INVENTORY_ENABLED"] = "true"
	vars["INVENTORY_URL"] = inventory.url
	vars["SRIOV_ON_DRIFT"] = "fail"
	vars["SRIOV_PROFILES"] = igbSRIOVProfiles

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	args := make([]string, 0, 32)
	args = append(args,
		"-M", "q35", "-m", "1024", "-nographic", "-no-reboot",
		"-kernel", kernel, "-initrd", buildProvisionInitramfsWithFiles(t, vars, files),
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", disk),
		// eth0: the provisioning network (virtio is loaded first by /init).
		"-netdev", "user,id=n0",
		"-device", "virtio-net-pci,netdev=n0,mac=52:54:00:12:34:56",
		// eth1: the SR-IOV PF.
		"-device", "pcie-root-port,id=rp1,chassis=1,slot=1",
		"-netdev", "user,id=n1,restrict=on",
		"-device", "igb,bus=rp1,netdev=n1,mac=52:54:00:00:01:00",
		"-append", "console=ttyS0 panic=1 net.ifnames=0",
	)
	args = append(args, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)
	out, _ := exec.CommandContext(ctx, "qemu-system-x86_64", args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("QEMU timed out. tail:\n%s", tail(out, 4000))
	}
	t.Logf("SR-IOV igb provision output tail:\n%s", tail(out, 3000))

	assertProvisionSucceeded(t, out)
	requirePowerOff(t, out)
	caprf.requireStatuses(t, "init", "success")
	requireIgbInventory(t, inventory)
	requireSRIOVFindings(t, string(out))
}

func requireIgbInventory(t *testing.T, s *inventorySink) {
	t.Helper()
	s.mu.Lock()
	body := s.body
	s.mu.Unlock()
	var inv sriovReport
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("inventory JSON: %v\n%s", err, body)
	}
	for _, n := range inv.NICs {
		if n.Driver != "igb" {
			continue
		}
		r := n.SRIOV
		if r == nil || !r.SRIOVCapable || r.TotalVFs != 7 || r.NumVFs != 0 || r.VendorID != "8086" ||
			r.DeviceID != "10c9" || r.Eswitch.Capability != "unsupported" {
			t.Fatalf("igb SR-IOV report = %+v", r)
		}
		return
	}
	t.Fatalf("no igb NIC in the inventory:\n%s", body)
}

// requireSRIOVFindings checks the "SR-IOV setting" log lines of every
// profile setting and the compliance summary.
func requireSRIOVFindings(t *testing.T, out string) {
	t.Helper()
	for _, setting := range []string{"setting=maxVFs", "setting=sriovEnabled", "setting=numVFs", "setting=vfs[0]", "setting=vfs[1]", "setting=handoff"} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "SR-IOV setting") && strings.Contains(line, setting+" ") && strings.Contains(line, "status=ok") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no ok finding for %s. tail:\n%s", setting, tail([]byte(out), 4000))
		}
	}
	if !strings.Contains(out, "SR-IOV compliance") || !strings.Contains(out, "failed=0") {
		t.Fatalf("no clean SR-IOV compliance summary. tail:\n%s", tail([]byte(out), 4000))
	}
	if !strings.Contains(out, `want="mac=02:00:00:00:00:11 trust=true spoofChk=false vlan=100 qos=2"`) {
		t.Errorf("VF 1 attributes not in the findings")
	}
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
