// debug package provides structured debug dump collection.
package debug

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// Dump holds a structured debug snapshot collected on failure.
type Dump struct {
	System  SystemSnapshot  `json:"system"`
	Disk    DiskSnapshot    `json:"disk"`
	Network NetworkSnapshot `json:"network"`
	Kernel  KernelSnapshot  `json:"kernel"`
}

// SystemSnapshot captures basic system information.
type SystemSnapshot struct {
	Hostname string `json:"hostname"`
	Uptime   string `json:"uptime"`
	MemInfo  string `json:"meminfo"`
	LoadAvg  string `json:"loadavg"`
	Vendor   string `json:"vendor"`
	Product  string `json:"product"`
}

// DiskSnapshot captures disk layout and state.
type DiskSnapshot struct {
	Lsblk  string `json:"lsblk"`
	Mounts string `json:"mounts"`
	Fdisk  string `json:"fdisk"`
	Df     string `json:"df"`
}

// NetworkSnapshot captures network configuration.
type NetworkSnapshot struct {
	IPAddr     string `json:"ipAddr"`
	IPRoute    string `json:"ipRoute"`
	IPNeigh    string `json:"ipNeigh"`
	ResolvConf string `json:"resolvConf"`
}

// KernelSnapshot captures kernel and boot state.
type KernelSnapshot struct {
	Dmesg      string `json:"dmesg"`
	Lsmod      string `json:"lsmod"`
	Cmdline    string `json:"cmdline"`
	EFIBootmgr string `json:"efibootmgr"`
}

// Collect gathers a debug dump from the running system.
func Collect() *Dump {
	d := &Dump{}
	d.System = collectSystem()
	d.Disk = collectDisk()
	d.Network = collectNetwork()
	d.Kernel = collectKernel()
	return d
}

// Marshal returns the dump as JSON.
func (d *Dump) Marshal() ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshaling debug dump: %w", err)
	}
	return data, nil
}

func collectSystem() SystemSnapshot {
	hostname, _ := os.Hostname()
	return SystemSnapshot{
		Hostname: hostname,
		Uptime:   readFile("/proc/uptime"),
		MemInfo:  readFile("/proc/meminfo"),
		LoadAvg:  readFile("/proc/loadavg"),
		Vendor:   readFile("/sys/class/dmi/id/sys_vendor"),
		Product:  readFile("/sys/class/dmi/id/product_name"),
	}
}

func collectDisk() DiskSnapshot {
	return DiskSnapshot{
		Lsblk:  runCmd("lsblk", "--json"),
		Mounts: readFile("/proc/mounts"),
		Fdisk:  runCmd("fdisk", "-l"),
		Df:     runCmd("df", "-h"),
	}
}

func collectNetwork() NetworkSnapshot {
	return NetworkSnapshot{
		IPAddr:     runCmd("ip", "addr"),
		IPRoute:    runCmd("ip", "route"),
		IPNeigh:    runCmd("ip", "neigh"),
		ResolvConf: readFile("/etc/resolv.conf"),
	}
}

func collectKernel() KernelSnapshot {
	return KernelSnapshot{
		Dmesg:      runCmd("dmesg", "--level=err,warn"),
		Lsmod:      runCmd("lsmod"),
		Cmdline:    readFile("/proc/cmdline"),
		EFIBootmgr: runCmd("efibootmgr", "-v"),
	}
}

func readFile(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // trusted system paths
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func runCmd(name string, args ...string) string {
	out, err := exec.Command(name, args...).CombinedOutput() //nolint:gosec // trusted internal commands
	if err != nil {
		slog.Debug("debug dump command failed", "cmd", name, "error", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
