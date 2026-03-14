# Proposal: Full Server Inventory — Extended Collection

## Status: Implemented

## Priority: P1

## Dependencies: [Hardware Inventory](proposal-hardware-inventory.md)

## Summary

Extend the existing hardware inventory collection (Phases 1–2 already
implemented) with GPU/accelerator enumeration, storage controller details,
firmware version matrix, thermal management data, power supply information,
PCI topology mapping, and USB device enumeration. Report extended inventory
to CAPRF for capacity planning, warranty tracking, and compliance auditing.

## Motivation

The existing inventory collects: system info (SMBIOS), CPUs, memory (DIMMs),
disks, NICs, PCI devices, and accelerators. This companion proposal adds
operational data needed for fleet management:

| Missing Data | Use Case |
|-------------|----------|
| GPU details (VRAM, driver) | GPU cluster capacity planning |
| Storage controller model | RAID compatibility, firmware management |
| Fan/thermal data | Proactive cooling failure detection |
| PSU status | Redundancy verification |
| USB devices | Security audit (unauthorized devices) |
| Complete PCI topology | NIC NUMA affinity for network performance |
| Cable/SFP info | Transceiver inventory, link quality |
| Chassis/enclosure | Physical location tracking |

### Industry Context

| Tool | Inventory Depth |
|------|----------------|
| **Ironic** | SMBIOS, driver-level via introspection |
| **MAAS** | SMBIOS, lshw-full, network discovery |
| **Tinkerbell** | Basic HW detection via OSIE |
| **Dell OpenManage** | Full SMBIOS, IPMI, storage controllers, PSU, fans |
| **HPE OneView** | Full inventory, firmware, thermal, power |

## Design

### Extended Inventory Types

```go
// pkg/inventory/extended.go
package inventory

// ExtendedInventory adds operational data to the base HardwareInventory.
type ExtendedInventory struct {
    HardwareInventory                                 // embed base inventory

    GPUs               []GPUInfo              `json:"gpus,omitempty"`
    StorageControllers []StorageControllerInfo `json:"storageControllers,omitempty"`
    Thermal            ThermalInfo             `json:"thermal,omitempty"`
    PowerSupplies      []PSUInfo              `json:"powerSupplies,omitempty"`
    USBDevices         []USBDeviceInfo        `json:"usbDevices,omitempty"`
    PCITopology        []PCIBridgeInfo        `json:"pciTopology,omitempty"`
    Transceivers       []TransceiverInfo      `json:"transceivers,omitempty"`
    Chassis            ChassisInfo            `json:"chassis,omitempty"`
}

// GPUInfo captures GPU/accelerator details.
type GPUInfo struct {
    Name         string `json:"name"`          // "NVIDIA A100"
    Vendor       string `json:"vendor"`        // "NVIDIA"
    PCIAddr      string `json:"pciAddr"`       // "0000:3b:00.0"
    VRAM         uint64 `json:"vram"`          // bytes
    Driver       string `json:"driver"`        // "nvidia" or "amdgpu"
    DriverVersion string `json:"driverVersion"` // "535.129.03"
    Architecture string `json:"architecture"`  // "Ampere"
    NUMANode     int    `json:"numaNode"`
    SRIOVCapable bool   `json:"sriovCapable"`
}

// StorageControllerInfo captures RAID/HBA controller details.
type StorageControllerInfo struct {
    Name        string `json:"name"`         // "MegaRAID SAS-3"
    Vendor      string `json:"vendor"`       // "Broadcom / LSI"
    Model       string `json:"model"`        // "9460-16i"
    PCIAddr     string `json:"pciAddr"`      // "0000:18:00.0"
    FWVersion   string `json:"fwVersion"`    // "50.8.0-3075"
    Driver      string `json:"driver"`       // "megaraid_sas"
    RAIDLevels  string `json:"raidLevels"`   // "0,1,5,6,10,50,60"
    Ports       int    `json:"ports"`
    CacheSize   uint64 `json:"cacheSize"`    // bytes
    BBU         bool   `json:"bbu"`          // battery backup unit
}

// ThermalInfo captures temperature sensor data.
type ThermalInfo struct {
    CPUTemps   []SensorReading `json:"cpuTemps,omitempty"`
    InletTemp  *SensorReading  `json:"inletTemp,omitempty"`
    ExhaustTemp *SensorReading `json:"exhaustTemp,omitempty"`
    Fans       []FanInfo       `json:"fans,omitempty"`
}

type SensorReading struct {
    Name    string  `json:"name"`
    TempC   float64 `json:"tempC"`
    Warning float64 `json:"warningC,omitempty"`
    Critical float64 `json:"criticalC,omitempty"`
}

type FanInfo struct {
    Name   string `json:"name"`
    RPM    int    `json:"rpm"`
    Status string `json:"status"` // "ok", "warning", "failed"
}

// PSUInfo captures power supply details.
type PSUInfo struct {
    Name     string `json:"name"`
    Status   string `json:"status"`    // "ok", "failed", "not-present"
    Watts    int    `json:"watts"`     // rated wattage
    Model    string `json:"model"`
    Serial   string `json:"serial"`
}

// USBDeviceInfo captures USB device details.
type USBDeviceInfo struct {
    Bus       int    `json:"bus"`
    Device    int    `json:"device"`
    VendorID  string `json:"vendorId"`  // "0781"
    ProductID string `json:"productId"` // "5583"
    Name      string `json:"name"`      // "SanDisk Ultra Fit"
    Class     string `json:"class"`     // "Mass Storage"
}

// PCIBridgeInfo captures PCI topology for NUMA affinity.
type PCIBridgeInfo struct {
    Bus      string          `json:"bus"`       // "0000:00"
    NUMANode int             `json:"numaNode"`
    Children []PCIDeviceInfo `json:"children"`
}

type PCIDeviceInfo struct {
    Addr     string `json:"addr"`     // "0000:3b:00.0"
    Vendor   string `json:"vendor"`
    Device   string `json:"device"`
    Class    string `json:"class"`
    NUMANode int    `json:"numaNode"`
}

// TransceiverInfo captures SFP/QSFP module data.
type TransceiverInfo struct {
    Interface  string  `json:"interface"`   // "eth0"
    Type       string  `json:"type"`        // "SFP28", "QSFP28", "QSFP-DD"
    Vendor     string  `json:"vendor"`      // "Finisar"
    PartNumber string  `json:"partNumber"`
    Serial     string  `json:"serial"`
    Speed      string  `json:"speed"`       // "25G", "100G"
    TempC      float64 `json:"tempC"`
    TxPower    float64 `json:"txPowerdBm"`
    RxPower    float64 `json:"rxPowerdBm"`
}

// ChassisInfo captures physical chassis/enclosure data.
type ChassisInfo struct {
    Type         string `json:"type"`          // "rack-mount", "blade"
    Manufacturer string `json:"manufacturer"`
    Model        string `json:"model"`
    Serial       string `json:"serial"`
    AssetTag     string `json:"assetTag"`
    Height       int    `json:"heightU"`       // rack units (1U, 2U, etc.)
}
```

### Collection Implementation

```go
// pkg/inventory/collector.go
package inventory

// CollectExtended gathers all inventory data.
func (c *Collector) CollectExtended(ctx context.Context) (*ExtendedInventory, error) {
    inv := &ExtendedInventory{}

    // Base inventory (existing implementation)
    base, err := c.Collect(ctx)
    if err != nil {
        return nil, fmt.Errorf("collect base inventory: %w", err)
    }
    inv.HardwareInventory = *base

    // Extended data (best-effort — log and continue on errors)
    inv.GPUs = c.collectGPUs(ctx)
    inv.StorageControllers = c.collectStorageControllers(ctx)
    inv.Thermal = c.collectThermal(ctx)
    inv.PowerSupplies = c.collectPSUs(ctx)
    inv.USBDevices = c.collectUSBDevices(ctx)
    inv.PCITopology = c.collectPCITopology(ctx)
    inv.Transceivers = c.collectTransceivers(ctx)
    inv.Chassis = c.collectChassis(ctx)

    return inv, nil
}

// All collection methods use sysfs/procfs first, binary tools as fallback.
// GPU: /sys/bus/pci/devices/*/class (0x0300 = display controller)
// Storage: /sys/class/scsi_host/ + SMBIOS Type 7
// Thermal: /sys/class/thermal/ + /sys/class/hwmon/
// PSU: SMBIOS Type 39
// USB: /sys/bus/usb/devices/
// PCI: /sys/bus/pci/devices/ (already used in base inventory)
// Transceivers: ethtool --module-info <iface>
// Chassis: SMBIOS Type 3
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `dmidecode` | `dmidecode` | SMBIOS tables (chassis, PSU) | full, gobgp | **Yes** |
| `ethtool` | `ethtool` | Transceiver/SFP info | full, gobgp | **Yes** |
| `lspci` | `pciutils` | PCI topology (fallback) | full, gobgp | **No — add** (from kernel-drivers proposal) |
| `nvidia-smi` | — | NVIDIA GPU details | none (not in initramfs) | N/A |

**Note**: No new binaries needed beyond what's already proposed in the
kernel-drivers proposal. All collection is Go-first using sysfs/procfs.

## Files Changed

| File | Change |
|------|--------|
| `pkg/inventory/extended.go` | Extended inventory types |
| `pkg/inventory/gpu.go` | GPU collection |
| `pkg/inventory/storage_controller.go` | Storage controller collection |
| `pkg/inventory/thermal.go` | Thermal/fan collection |
| `pkg/inventory/psu.go` | PSU collection |
| `pkg/inventory/usb.go` | USB device collection |
| `pkg/inventory/pci_topology.go` | PCI NUMA topology |
| `pkg/inventory/transceiver.go` | SFP/QSFP transceiver data |
| `pkg/inventory/chassis.go` | Chassis/enclosure data |
| `pkg/inventory/collector.go` | `CollectExtended()` method |
| `pkg/caprf/client.go` | Extended inventory reporting |

## Testing

### Unit Tests

- Per-subsystem tests with mock sysfs trees (`t.TempDir()` + fake sysfs):
  - `gpu_test.go` — PCI class filtering for GPUs
  - `thermal_test.go` — hwmon sensor parsing
  - `psu_test.go` — SMBIOS Type 39 parsing
  - `usb_test.go` — USB sysfs enumeration
  - `transceiver_test.go` — ethtool module info parsing

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with multiple virtio devices → verify PCI topology
  - QEMU with emulated NVMe controller → verify storage controller detection
  - QEMU with USB pass-through → verify USB enumeration

## Risks

| Risk | Mitigation |
|------|------------|
| sysfs paths differ between kernels | Kernel 5.15+ targeted; graceful fallback |
| Thermal sensors vary by vendor | Best-effort with hwmon; IPMI as backup |
| GPU data unavailable in initramfs | Collect what sysfs provides; skip driver-level |
| Large inventory JSON payload | Compress; CAPRF accepts gzipped body |

## Effort Estimate

10–14 engineering days (8 collection subsystems + types + CAPRF
integration + tests).
