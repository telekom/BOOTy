# Proposal: Vendor-Specific BIOS Settings Management

## Status: Proposal

## Priority: P1

## Dependencies: [BIOS Settings (Redfish)](proposal-bios-settings.md)

## Summary

Extend the Redfish-based BIOS settings proposal with vendor-specific BIOS
capture, baseline, diff, set/unset from within BOOTy's initramfs for four
server vendors: **HPE ProLiant** (iLO), **Lenovo ThinkSystem** (XCC),
**Supermicro** (IPMI/Redfish), and **Dell PowerEdge** (iDRAC). A common
`BIOSManager` interface abstracts vendor differences with auto-detection
from DMI `sys_vendor`.

## Motivation

While Redfish provides a standard BIOS settings API
(`/redfish/v1/Systems/1/Bios`), real-world vendor implementations have
significant OEM quirks:

| Vendor | Quirk |
|--------|-------|
| HPE iLO 5/6 | BIOS attribute names are vendor-specific (`ProcHyperthreading`, not `HyperThreading`). Requires `HpeServerBootSettings` OEM resource for boot order. |
| Lenovo XCC | Uses `Oem.Lenovo` extensions for advanced settings. Some attributes require system profile changes (`OptimizedComputePerformance`). |
| Supermicro | Limited Redfish BIOS support on older BMCs (X11 generation). Many settings only available via raw IPMI OEM commands. |
| Dell iDRAC | Uses `Oem.Dell.DellBIOSAttributes` for extended attributes. BIOS token IDs differ from attribute names. Job queue required for BIOS changes. |

### Industry Context

| Tool | BIOS Management |
|------|----------------|
| **Ironic** | `bios` interface — vendor-neutral Redfish, no OEM handling |
| **MAAS** | Limited — commissioning scripts can modify BIOS |
| **Dell RACADM** | Proprietary CLI for iDRAC BIOS management |
| **HPE iLOrest** | Proprietary CLI for iLO BIOS management |
| **Lenovo LXCA** | Centralized XCC management (enterprise) |
| **Supermicro SUM** | Supermicro Update Manager — BIOS/BMC config |

BOOTy provides a unified, vendor-aware BIOS management layer embedded in
the provisioning pipeline — no separate management tools needed.

## Design

### Common Interface

```go
// pkg/bios/manager.go
package bios

import "context"

// Vendor represents a server vendor detected from DMI.
type Vendor string

const (
    VendorHPE        Vendor = "HPE"
    VendorLenovo     Vendor = "Lenovo"
    VendorSupermicro Vendor = "Supermicro"
    VendorDell       Vendor = "Dell Inc."
)

// BIOSManager provides vendor-specific BIOS operations.
type BIOSManager interface {
    // Vendor returns the server vendor this manager handles.
    Vendor() Vendor

    // Capture reads all BIOS settings and returns a snapshot.
    Capture(ctx context.Context) (*BIOSState, error)

    // Apply sets individual BIOS attributes. Returns list of attributes
    // that require a reboot to take effect.
    Apply(ctx context.Context, changes []SettingChange) (rebootRequired []string, err error)

    // Reset restores BIOS to factory defaults.
    Reset(ctx context.Context) error
}

// BIOSState represents the full BIOS configuration snapshot.
type BIOSState struct {
    Vendor     Vendor                `json:"vendor"`
    Model      string                `json:"model"`
    Version    string                `json:"biosVersion"`
    Settings   map[string]Setting    `json:"settings"`
    OEMData    map[string]string     `json:"oemData,omitempty"`
}

// Setting represents a single BIOS attribute.
type Setting struct {
    Name          string   `json:"name"`
    CurrentValue  string   `json:"currentValue"`
    DefaultValue  string   `json:"defaultValue,omitempty"`
    PendingValue  string   `json:"pendingValue,omitempty"`
    Type          string   `json:"type"` // "enum", "int", "string", "bool"
    AllowedValues []string `json:"allowedValues,omitempty"`
    ReadOnly      bool     `json:"readOnly,omitempty"`
}

// SettingChange requests a BIOS attribute modification.
type SettingChange struct {
    Name  string `json:"name"`
    Value string `json:"value"` // empty = reset to default
}
```

### Baseline and Diff

```go
// pkg/bios/baseline.go
package bios

type Baseline struct {
    Vendor   Vendor            `json:"vendor"`
    Model    string            `json:"model,omitempty"`
    Settings map[string]string `json:"settings"` // name → expected value
}

type Diff struct {
    Matches bool        `json:"matches"`
    Changes []DiffEntry `json:"changes,omitempty"`
}

type DiffEntry struct {
    Name     string `json:"name"`
    Expected string `json:"expected"`
    Actual   string `json:"actual"`
}

func Compare(baseline *Baseline, state *BIOSState) *Diff {
    diff := &Diff{Matches: true}
    for name, expected := range baseline.Settings {
        setting, ok := state.Settings[name]
        if !ok || setting.CurrentValue != expected {
            actual := ""
            if ok {
                actual = setting.CurrentValue
            }
            diff.Changes = append(diff.Changes, DiffEntry{
                Name: name, Expected: expected, Actual: actual,
            })
            diff.Matches = false
        }
    }
    return diff
}
```

### Vendor Auto-Detection

```go
// pkg/bios/detect.go
package bios

import (
    "fmt"
    "os"
    "strings"
)

// DetectVendor reads the system vendor from DMI sysfs.
func DetectVendor() (Vendor, error) {
    data, err := os.ReadFile("/sys/class/dmi/id/sys_vendor")
    if err != nil {
        return "", fmt.Errorf("read sys_vendor: %w", err)
    }
    vendor := strings.TrimSpace(string(data))
    switch {
    case strings.Contains(vendor, "HPE") || strings.Contains(vendor, "Hewlett"):
        return VendorHPE, nil
    case strings.Contains(vendor, "Lenovo"):
        return VendorLenovo, nil
    case strings.Contains(vendor, "Supermicro"):
        return VendorSupermicro, nil
    case strings.Contains(vendor, "Dell"):
        return VendorDell, nil
    default:
        return "", fmt.Errorf("unsupported BIOS vendor: %s", vendor)
    }
}
```

### Vendor Implementations

#### HPE ProLiant (iLO)

```go
// pkg/bios/hpe/manager.go — key HPE-specific BIOS attributes
var hpeCriticalSettings = map[string]string{
    "ProcHyperthreading":    "Enabled",
    "ProcVirtualization":    "Enabled",
    "Sriov":                 "Enabled",
    "BootMode":              "Uefi",
    "SecureBootStatus":      "Enabled",
    "WorkloadProfile":       "GeneralPowerEfficientCompute",
    "PowerRegulator":        "DynamicPowerSavings",
    "ThermalConfig":         "OptimalCooling",
    "IntelligentProvisioning": "Disabled", // skip iLO provisioning
    "EmbSata1Aspm":          "Disabled", // NVMe performance
}
```

#### Lenovo ThinkSystem (XCC)

```go
// pkg/bios/lenovo/manager.go — key Lenovo-specific BIOS attributes
var lenovoCriticalSettings = map[string]string{
    "OperatingMode":             "MaximumPerformance",
    "HyperThreading":            "Enable",
    "VirtualizationTechnology":  "Enable",
    "SRIOVSupport":              "Enable",
    "BootMode":                  "UEFIMode",
    "SecureBoot":                "Enable",
    "TurboMode":                 "Enable",
    "IntelSpeedStep":            "Enable",
    "ActiveProcessorCores":      "All",
    "PackageCState":             "C0/C1",
}
```

#### Supermicro

```go
// pkg/bios/supermicro/manager.go
// Supermicro older boards (X11 generation) have limited Redfish BIOS support.
// For those, BIOS capture uses IPMI raw OEM commands via /dev/ipmi0.
// Newer boards (X12+, H12+) have standard Redfish BIOS resources.
```

#### Dell PowerEdge (iDRAC)

```go
// pkg/bios/dell/manager.go — Dell requires job queue for BIOS changes
var dellCriticalSettings = map[string]string{
    "LogicalProc":       "Enabled",
    "VirtualizationTechnology": "Enabled",
    "SriovGlobalEnable": "Enabled",
    "BootMode":          "Uefi",
    "SecureBoot":        "Enabled",
    "SystemProfile":     "Performance",
    "WorkloadProfile":   "NotAvailable",
    "ProcTurboMode":     "Enabled",
    "ProcCStates":       "Disabled",
    "MemTest":           "Disabled", // reduces boot time
}
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `dmidecode` | `dmidecode` | DMI/SMBIOS table parsing, vendor detection | all | **Yes** |
| `curl` | `curl` | Redfish API calls (fallback) | all | **Yes** |
| `ipmitool` | `ipmitool` | Supermicro OEM BIOS commands via `/dev/ipmi0` | full, gobgp | **No — add** |

**Note**: The Go-first approach uses direct HTTP for Redfish and Go IPMI
via `/dev/ipmi0` ioctl for Supermicro. The `ipmitool` binary is a fallback
for edge cases where the Go IPMI stack doesn't handle a vendor-specific
OEM command.

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    ipmitool \
    && rm -rf /var/lib/apt/lists/*

# IPMI tool for Supermicro OEM BIOS commands (fallback)
COPY --from=tools /usr/bin/ipmitool bin/ipmitool
```

### Configuration

```bash
# /deploy/vars
export BIOS_MANAGEMENT_ENABLED="true"
export BIOS_MODE="capture"                # "capture", "baseline", "diff", "apply"
export BIOS_BASELINE='{"vendor":"HPE","settings":{"ProcHyperthreading":"Enabled","Sriov":"Enabled"}}'
export BIOS_CHANGES='[{"name":"ProcHyperthreading","value":"Enabled"},{"name":"Sriov","value":"Enabled"}]'
export BIOS_URL="https://caprf.example.com/status/bios"
```

### CAPRF Integration

```go
// pkg/caprf/client.go — new method
func (c *Client) ReportBIOS(ctx context.Context, data []byte) error {
    if c.cfg.BIOSURL == "" {
        c.log.Debug("No BIOS URL configured, skipping report")
        return nil
    }
    return c.postJSONWithAuth(ctx, c.cfg.BIOSURL, data)
}
```

### CAPRF-Side Redfish BIOS Flow (for apply mode)

When BIOS changes are applied, the typical workflow requires reboot
coordination with CAPRF:

1. BOOTy captures current BIOS state → reports to CAPRF
2. CAPRF compares against desired state (from RedfishHost CR)
3. CAPRF PATCHes Redfish `/Bios/Settings` with desired changes
4. CAPRF reboots machine (BIOS changes are pending until reboot)
5. Machine reboots → BIOS applies settings during POST
6. BOOTy boots again → captures BIOS → diffs against desired → reports success

### Redfish-less Flow (BOOTy-native BIOS Management)

For environments without Redfish access or where CAPRF should not touch the
BMC, BOOTy can handle the entire BIOS lifecycle locally using sysfs, EFI
variables, and IPMI raw commands — no Redfish dependency at all.

#### Architecture

```
┌──────────────────────────────────────────────────────┐
│ BOOTy (initrd) — Redfish-less BIOS Flow              │
│                                                      │
│  1. Detect vendor via /sys/class/dmi/id/sys_vendor   │
│  2. Capture BIOS settings:                           │
│     ├─ EFI variables (/sys/firmware/efi/efivars/)    │
│     ├─ SMBIOS/DMI (/sys/class/dmi/id/*)              │
│     ├─ IPMI raw OEM read (/dev/ipmi0)                │
│     └─ Fallback: dmidecode + sysfs parsing           │
│  3. Diff against baseline from /deploy/vars          │
│  4. Apply changes:                                   │
│     ├─ EFI variable write (efivarfs)                 │
│     ├─ IPMI raw OEM set (/dev/ipmi0)                 │
│     └─ Vendor-specific: ipmitool raw commands        │
│  5. Verify changes took effect (re-capture + diff)   │
│  6. Report to CAPRF (or log locally if offline)      │
│  7. Signal reboot-required if needed                 │
└──────────────────────────────────────────────────────┘
```

#### BIOS Access Methods (No Redfish)

| Method | Read | Write | Vendor | Notes |
|--------|------|-------|--------|-------|
| EFI variables (`efivarfs`) | UEFI settings | Some UEFI settings | All (UEFI mode) | Boot order, SecureBoot state, PXE settings |
| SMBIOS/DMI (`sysfs`) | BIOS version, vendor info | No | All | Read-only system identification |
| IPMI raw OEM (`/dev/ipmi0`) | Vendor BIOS attributes | Vendor BIOS attributes | Supermicro, Dell, HPE | Vendor-specific OEM command bytes |
| `dmidecode` | Full SMBIOS dump | No | All | Structured BIOS/system/memory info |

#### Go Implementation

```go
// pkg/bios/local/manager.go
package local

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LocalBIOSManager reads and writes BIOS settings without Redfish.
// Uses EFI variables, IPMI raw commands, and sysfs.
type LocalBIOSManager struct {
	log       *slog.Logger
	vendor    bios.Vendor
	ipmiDev   string // "/dev/ipmi0"
	efivarDir string // "/sys/firmware/efi/efivars"
}

func New(log *slog.Logger, vendor bios.Vendor) *LocalBIOSManager {
	return &LocalBIOSManager{
		log:       log,
		vendor:    vendor,
		ipmiDev:   "/dev/ipmi0",
		efivarDir: "/sys/firmware/efi/efivars",
	}
}

// CaptureEFIVars reads UEFI boot-related variables.
func (m *LocalBIOSManager) CaptureEFIVars(ctx context.Context) (map[string]string, error) {
	vars := make(map[string]string)

	// Read boot order
	bootOrder, err := readEFIVar(m.efivarDir, "BootOrder")
	if err == nil {
		vars["BootOrder"] = fmt.Sprintf("%x", bootOrder)
	}

	// Read SecureBoot state
	sbData, err := readEFIVar(m.efivarDir, "SecureBoot")
	if err == nil && len(sbData) > 0 {
		if sbData[len(sbData)-1] == 1 {
			vars["SecureBoot"] = "Enabled"
		} else {
			vars["SecureBoot"] = "Disabled"
		}
	}

	// Read SetupMode
	smData, err := readEFIVar(m.efivarDir, "SetupMode")
	if err == nil && len(smData) > 0 {
		if smData[len(smData)-1] == 1 {
			vars["SetupMode"] = "Enabled"
		} else {
			vars["SetupMode"] = "Disabled"
		}
	}

	return vars, nil
}

func readEFIVar(efivarDir, name string) ([]byte, error) {
	matches, err := filepath.Glob(filepath.Join(efivarDir, name+"-*"))
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("efi variable %s not found: %w", name, err)
	}
	// EFI variable format: 4-byte attributes + data
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, fmt.Errorf("read efi variable %s: %w", name, err)
	}
	if len(data) <= 4 {
		return nil, fmt.Errorf("efi variable %s too short", name)
	}
	return data[4:], nil // skip 4-byte attribute prefix
}
```

#### IPMI Raw BIOS Access (Vendor-Specific)

```go
// pkg/bios/local/ipmi.go
package local

import (
	"context"
	"fmt"
)

// IPMIBIOSReader reads BIOS settings via IPMI OEM raw commands.
// Each vendor has different OEM NetFn + command bytes.
type IPMIBIOSReader struct {
	dev string // "/dev/ipmi0"
}

// Supermicro BIOS read: NetFn 0x30, Cmd 0x20 (Get BIOS Configuration)
func (r *IPMIBIOSReader) readSupermicro(ctx context.Context, setting string) (string, error) {
	// Supermicro uses raw IPMI OEM commands via /dev/ipmi0 ioctl
	// NetFn: 0x30 (OEM), Cmd: 0x20 (Get BIOS Config)
	resp, err := ipmiRawCommand(r.dev, 0x30, 0x20, settingToBytes(setting))
	if err != nil {
		return "", fmt.Errorf("supermicro BIOS read %s: %w", setting, err)
	}
	return parseSupermicroResponse(resp), nil
}

// Dell iDRAC BIOS read: NetFn 0x30, Cmd 0xCE (Get System Config)
func (r *IPMIBIOSReader) readDell(ctx context.Context, setting string) (string, error) {
	resp, err := ipmiRawCommand(r.dev, 0x30, 0xCE, settingToBytes(setting))
	if err != nil {
		return "", fmt.Errorf("dell BIOS read %s: %w", setting, err)
	}
	return parseDellResponse(resp), nil
}

// HPE iLO BIOS read: NetFn 0x2E, Cmd 0x80 (OEM Get Configuration)
func (r *IPMIBIOSReader) readHPE(ctx context.Context, setting string) (string, error) {
	resp, err := ipmiRawCommand(r.dev, 0x2E, 0x80, settingToBytes(setting))
	if err != nil {
		return "", fmt.Errorf("hpe BIOS read %s: %w", setting, err)
	}
	return parseHPEResponse(resp), nil
}
```

#### IPMI Raw BIOS Write

```go
// pkg/bios/local/ipmi_write.go
package local

import (
	"context"
	"fmt"
)

// Supermicro BIOS write: NetFn 0x30, Cmd 0x21 (Set BIOS Configuration)
func (r *IPMIBIOSReader) writeSupermicro(ctx context.Context, setting, value string) error {
	payload := append(settingToBytes(setting), valueToBytes(value)...)
	_, err := ipmiRawCommand(r.dev, 0x30, 0x21, payload)
	if err != nil {
		return fmt.Errorf("supermicro BIOS write %s=%s: %w", setting, value, err)
	}
	return nil
}

// Dell iDRAC BIOS write: NetFn 0x30, Cmd 0xCF (Set System Config)
func (r *IPMIBIOSReader) writeDell(ctx context.Context, setting, value string) error {
	payload := append(settingToBytes(setting), valueToBytes(value)...)
	_, err := ipmiRawCommand(r.dev, 0x30, 0xCF, payload)
	if err != nil {
		return fmt.Errorf("dell BIOS write %s=%s: %w", setting, value, err)
	}
	return nil
}
```

#### Go-Native IPMI via ioctl

```go
// pkg/bios/local/ipmi_dev.go
//go:build linux

package local

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	ipmiIOCTLMagic = 'i'
	ipmiSendCommand = 0 // _IOWR(ipmiIOCTLMagic, 0, struct ipmi_req)
)

// ipmiReq matches the kernel's struct ipmi_req.
type ipmiReq struct {
	Addr     [32]byte
	AddrLen  uint32
	MsgID    int64
	Msg      ipmiMsg
}

type ipmiMsg struct {
	Netfn   uint8
	Cmd     uint8
	DataLen uint16
	Data    unsafe.Pointer
}

// ipmiRawCommand sends a raw IPMI command via /dev/ipmi0 ioctl.
func ipmiRawCommand(dev string, netfn, cmd uint8, data []byte) ([]byte, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open IPMI device %s: %w", dev, err)
	}
	defer f.Close()

	req := ipmiReq{}
	req.Msg.Netfn = netfn
	req.Msg.Cmd = cmd
	if len(data) > 0 {
		req.Msg.DataLen = uint16(len(data))
		req.Msg.Data = unsafe.Pointer(&data[0]) //nolint:gosec // intentional IPMI device ioctl
	}

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		f.Fd(),
		uintptr(unix.IOCTL(ipmiIOCTLMagic, ipmiSendCommand)),
		uintptr(unsafe.Pointer(&req)),
	)
	if errno != 0 {
		return nil, fmt.Errorf("IPMI ioctl failed: %w", errno)
	}

	// Read response from device
	respBuf := make([]byte, 1024)
	n, err := f.Read(respBuf)
	if err != nil {
		return nil, fmt.Errorf("read IPMI response: %w", err)
	}
	return respBuf[:n], nil
}
```

#### Configuration (Redfish-less Mode)

```bash
# /deploy/vars
export BIOS_MANAGEMENT_ENABLED="true"
export BIOS_BACKEND="local"               # "local" (no Redfish) or "redfish" (default)
export BIOS_MODE="capture"                 # "capture", "baseline", "diff", "apply"
export BIOS_BASELINE='{"vendor":"Supermicro","settings":{"HyperThreading":"Enabled"}}'
export BIOS_CHANGES='[{"name":"HyperThreading","value":"Enabled"}]'
export BIOS_REPORT_URL="https://caprf.example.com/status/bios"  # optional
```

#### Provisioning Orchestration (Redfish-less)

```go
// pkg/provision/orchestrator.go — Redfish-less BIOS step
func (o *Orchestrator) manageBIOSLocal(ctx context.Context) error {
	vendor, err := bios.DetectVendor()
	if err != nil {
		o.log.Warn("BIOS vendor detection failed, skipping BIOS management", "error", err)
		return nil
	}

	mgr := local.New(o.log, vendor)

	switch o.cfg.BIOSMode {
	case "capture":
		// Read everything we can from sysfs + efivarfs + IPMI
		state, err := mgr.Capture(ctx)
		if err != nil {
			return fmt.Errorf("capture local BIOS settings: %w", err)
		}
		return o.client.ReportBIOS(ctx, state)

	case "diff":
		state, err := mgr.Capture(ctx)
		if err != nil {
			return fmt.Errorf("capture for diff: %w", err)
		}
		diff := bios.Compare(o.cfg.BIOSBaseline, state)
		if !diff.Matches {
			o.log.Warn("BIOS drift detected", "changes", len(diff.Changes))
		}
		return o.client.ReportBIOS(ctx, diff)

	case "apply":
		// Apply via IPMI raw commands (vendor-specific)
		rebootRequired, err := mgr.Apply(ctx, o.cfg.BIOSChanges)
		if err != nil {
			return fmt.Errorf("apply local BIOS settings: %w", err)
		}
		if len(rebootRequired) > 0 {
			o.log.Info("BIOS changes require reboot", "settings", rebootRequired)
			// BOOTy itself triggers the reboot (no CAPRF/Redfish needed)
			return o.rebootForBIOSApply(ctx)
		}
		return nil

	case "baseline":
		// Capture + save as golden baseline
		state, err := mgr.Capture(ctx)
		if err != nil {
			return fmt.Errorf("capture baseline: %w", err)
		}
		return o.client.ReportBIOSBaseline(ctx, state)
	}

	return nil
}

// rebootForBIOSApply handles the self-managed reboot cycle.
// BOOTy sets a flag in /deploy/vars to track the reboot reason,
// so on next boot it re-captures and verifies the changes.
func (o *Orchestrator) rebootForBIOSApply(ctx context.Context) error {
	// Write breadcrumb for post-reboot verification
	_ = os.WriteFile("/deploy/bios-reboot-pending", []byte("true"), 0644)

	o.log.Info("rebooting to apply BIOS changes")
	return unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)
}
```

#### Post-Reboot Verification

```go
// pkg/provision/orchestrator.go — runs on boot, before provisioning
func (o *Orchestrator) checkBIOSRebootPending(ctx context.Context) error {
	if _, err := os.Stat("/deploy/bios-reboot-pending"); os.IsNotExist(err) {
		return nil // no pending BIOS reboot
	}

	o.log.Info("post-BIOS-reboot: verifying settings took effect")

	// Remove breadcrumb
	_ = os.Remove("/deploy/bios-reboot-pending")

	// Re-capture and diff
	vendor, _ := bios.DetectVendor()
	mgr := local.New(o.log, vendor)
	state, err := mgr.Capture(ctx)
	if err != nil {
		return fmt.Errorf("post-reboot BIOS capture: %w", err)
	}

	diff := bios.Compare(o.cfg.BIOSBaseline, state)
	if !diff.Matches {
		o.log.Error("BIOS settings did not apply correctly",
			"remaining_diffs", len(diff.Changes))
		return o.client.ReportBIOS(ctx, diff)
	}

	o.log.Info("BIOS settings verified successfully")
	return o.client.ReportBIOS(ctx, state)
}
```

#### Redfish-less vs Redfish Comparison

| Aspect | Redfish Flow | Redfish-less (BOOTy-native) |
|--------|-------------|----------------------------|
| Read BIOS | CAPRF → Redfish `/Bios` | BOOTy → sysfs + efivarfs + IPMI |
| Write BIOS | CAPRF → Redfish `/Bios/Settings` | BOOTy → IPMI raw OEM commands |
| Reboot | CAPRF → Redfish power control | BOOTy → `unix.Reboot()` |
| Verification | CAPRF re-reads Redfish | BOOTy re-captures on next boot |
| Vendor coverage | All Redfish-capable BMCs | Requires per-vendor IPMI OEM work |
| Dependencies | CAPRF + Redfish access | Only BOOTy + `/dev/ipmi0` |
| Attribute scope | Full BIOS attributes | Vendor-dependent subset |
| Offline support | No (needs BMC network) | Yes (local-only) |

## Files Changed

| File | Change |
|------|--------|
| `pkg/bios/manager.go` | Common `BIOSManager` interface, types |
| `pkg/bios/baseline.go` | Baseline/Diff types and `Compare()` |
| `pkg/bios/detect.go` | Vendor auto-detection from DMI sysfs |
| `pkg/bios/hpe/manager.go` | HPE iLO implementation |
| `pkg/bios/lenovo/manager.go` | Lenovo XCC implementation |
| `pkg/bios/supermicro/manager.go` | Supermicro implementation |
| `pkg/bios/dell/manager.go` | Dell iDRAC implementation |
| `pkg/bios/local/manager.go` | Redfish-less local BIOS read/write via efivarfs + IPMI |
| `pkg/bios/local/ipmi.go` | IPMI raw OEM BIOS commands per vendor |
| `pkg/bios/local/ipmi_write.go` | IPMI raw OEM BIOS write commands |
| `pkg/bios/local/ipmi_dev.go` | Go-native IPMI ioctl via `/dev/ipmi0` (linux build tag) |
| `pkg/caprf/client.go` | `ReportBIOS()`, `ReportBIOSBaseline()` methods |
| `pkg/config/provider.go` | BIOS config fields + `BIOSBackend` |
| `pkg/provision/orchestrator.go` | `manageBIOS()` + `manageBIOSLocal()` + `checkBIOSRebootPending()` steps |
| `initrd.Dockerfile` | Add `ipmitool` binary, ensure `/dev/ipmi0` accessible |

## Testing

### Unit Tests

- `bios/detect_test.go` — Vendor detection with mock DMI sysfs for each
  vendor. Table-driven: HPE, Lenovo, Supermicro, Dell, unknown.
- `bios/baseline_test.go` — `Compare()` with table-driven cases: exact
  match, single drift, multiple drifts, missing attributes.
- `bios/hpe/manager_test.go` — HPE-specific Redfish response parsing.
  Mock HTTP server returning HPE BIOS attributes JSON.
- `bios/lenovo/manager_test.go` — Lenovo XCC Redfish response parsing.
- `bios/supermicro/manager_test.go` — IPMI OEM command encoding/decoding.
- `bios/dell/manager_test.go` — Dell iDRAC job queue flow testing.

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with OVMF (UEFI) firmware — provides basic UEFI variable access
  - Mock Redfish BMC (Go HTTP server implementing Redfish BIOS resources)
  - Scenario 1: Capture BIOS state → verify JSON structure
  - Scenario 2: Diff against baseline → verify delta detection
  - Scenario 3: Apply change → verify Redfish PATCH request sent

### Integration Tests

- **ContainerLab** (tag `e2e_integration`): BOOTy boots and runs BIOS
  capture. Expected: vendor detection fails (container, not bare metal)
  → graceful skip.

## Risks

| Risk | Mitigation |
|------|------------|
| Redfish BIOS attribute names differ wildly between vendors | Per-vendor attribute mappings in vendor packages |
| BIOS changes require reboot | CAPRF coordination or BOOTy self-reboot; pre/post verification |
| Supermicro IPMI OEM commands undocumented | Reverse-engineer from SUM tool; document known commands |
| Dell job queue timeout | Configurable timeout; retry logic |
| Some attributes are read-only | Mark in Setting type; skip in Apply |
| IPMI raw command bytes differ between firmware versions | Version-specific command tables; fail-safe on unknown version |
| EFI variable write can brick boot (wrong variable) | Whitelist writable variables; never touch PK/KEK directly |
| Redfish-less flow has narrower attribute coverage | Document supported settings per vendor; graceful skip for unsupported |
| `/dev/ipmi0` not available on all servers | Detect device availability; fall back to sysfs-only capture |

## Effort Estimate

16–24 engineering days (common framework + 4 vendor Redfish implementations +
Redfish-less local BIOS flow + IPMI raw command R&D + CAPRF integration +
KVM tests).
