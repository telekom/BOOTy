# Proposal: Hardware Inventory and Inspection

## Status: Proposal

## Priority: P1

## Summary

Implement hardware inventory collection in BOOTy's initrd phase, reporting
detailed system specs back to the CAPRF controller. This enables automated
hardware classification, capacity planning, and scheduling decisions based
on actual hardware capabilities rather than static labels.

## Motivation

Currently, CAPRF and BOOTy have no insight into the physical hardware beyond
what the Redfish API exposes at the BMC level. Key gaps:

- **CPU topology**: Core count, thread count, model, microcode version
- **Memory**: DIMM layout, ECC status, speed, rank
- **Storage**: Disk models, firmware versions, SMART health, NVMe namespaces
- **Network**: NIC models, firmware, link speed, SR-IOV VF count
- **GPU/Accelerators**: Presence and model of any accelerator cards
- **PCIe topology**: Full device tree for debugging slot assignments

### Industry Context

| Tool | Hardware Inventory |
|------|-------------------|
| **Ironic** | Full inspection via `ironic-inspector` — collects CPU, RAM, disk, NIC, LLDP data; stores in node `extra` and `properties` fields |
| **MAAS** | Commissioning phase runs `lshw`, `lldpd`, collects detailed BMC data |
| **Tinkerbell** | Minimal — relies on user-provided hardware data |

Ironic's inspection is considered the gold standard. BOOTy can achieve
similar coverage while running inside the provisioning initrd — no separate
inspection boot cycle needed.

## Design

### Collection Architecture

```
┌──────────────────────────────────────────────┐
│ BOOTy (initrd)                               │
│                                              │
│  Inventory Collector                         │
│   ├─ /proc/cpuinfo → CPU model, cores       │
│   ├─ /sys/class/dmi/id/* → vendor, serial    │
│   ├─ /sys/block/*/device → disk inventory    │
│   ├─ /sys/class/net/*/device → NIC details   │
│   ├─ /sys/bus/pci/devices/* → PCIe tree      │
│   ├─ lsblk --json → all block devices       │
│   ├─ SMART data (smartctl or sysfs)          │
│   └─ /sys/class/nvme/* → NVMe info          │
│                                              │
│  Report as JSON → POST to CAPRF /inventory   │
└──────────────────────────────────────────────┘
```

### Data Model

```go
// pkg/inventory/types.go
type HardwareInventory struct {
    Timestamp    time.Time       `json:"timestamp"`
    System       SystemInfo      `json:"system"`
    CPUs         []CPUInfo       `json:"cpus"`
    Memory       MemoryInfo      `json:"memory"`
    Disks        []DiskInfo      `json:"disks"`
    NICs         []NICInfo       `json:"nics"`
    PCIDevices   []PCIDevice     `json:"pciDevices,omitempty"`
    Accelerators []Accelerator   `json:"accelerators,omitempty"`
}

type SystemInfo struct {
    Vendor       string `json:"vendor"`
    Product      string `json:"product"`
    SerialNumber string `json:"serialNumber"`
    UUID         string `json:"uuid"`
    BIOSVersion  string `json:"biosVersion"`
    BMCVersion   string `json:"bmcVersion,omitempty"`
}

type CPUInfo struct {
    Model      string `json:"model"`
    Cores      int    `json:"cores"`
    Threads    int    `json:"threads"`
    Socket     int    `json:"socket"`
    FreqMHz    int    `json:"freqMHz"`
    Flags      string `json:"flags"`
    Microcode  string `json:"microcode"`
}

type MemoryInfo struct {
    TotalBytes uint64     `json:"totalBytes"`
    DIMMs      []DIMMInfo `json:"dimms"`
}

type DIMMInfo struct {
    Slot     string `json:"slot"`
    SizeGB   int    `json:"sizeGB"`
    Type     string `json:"type"`     // DDR4, DDR5
    SpeedMHz int    `json:"speedMHz"`
    ECC      bool   `json:"ecc"`
}

type DiskInfo struct {
    Name         string `json:"name"`         // sda, nvme0n1
    Model        string `json:"model"`
    Serial       string `json:"serial"`
    SizeBytes    uint64 `json:"sizeBytes"`
    Type         string `json:"type"`         // SSD, HDD, NVMe
    Transport    string `json:"transport"`    // SATA, SAS, NVMe, USB
    Firmware     string `json:"firmware"`
    SMARTHealthy bool   `json:"smartHealthy"`
    Rotational   bool   `json:"rotational"`
}

type NICInfo struct {
    Name      string `json:"name"`
    Driver    string `json:"driver"`
    MAC       string `json:"mac"`
    PCIAddr   string `json:"pciAddr"`
    Speed     string `json:"speed"`
    SRIOVVFs  int    `json:"sriovVfs"`
    Firmware  string `json:"firmware"`
}
```

### Collection Methods

All data is read from sysfs/procfs — no external tools required:

| Data | Source | Parsing |
|------|--------|---------|
| System info | `/sys/class/dmi/id/{sys_vendor,product_name,product_serial}` | Read file |
| CPU | `/proc/cpuinfo` | Parse key-value pairs |
| Memory total | `/proc/meminfo` | Parse `MemTotal` |
| DIMM details | `/sys/devices/system/memory/` or `dmidecode` via sysfs | Parse sysfs |
| Disks | `/sys/block/*/device/{model,serial,firmware_rev}` | Iterate sysfs |
| Disk type | `/sys/block/*/queue/rotational` | `0` = SSD, `1` = HDD |
| NIC | `/sys/class/net/*/device/{vendor,device,driver}` | PCI ID lookup |
| NIC firmware | `/sys/class/net/*/device/firmware_version` | Read file |
| SR-IOV VF count | `/sys/class/net/*/device/sriov_totalvfs` | Read file |
| PCIe | `/sys/bus/pci/devices/*/` | Enumerate + read vendor/device |

### Reporting

BOOTy posts the inventory JSON to the CAPRF server:

```go
// pkg/caprf/client.go
func (c *Client) ReportInventory(ctx context.Context, inv *inventory.HardwareInventory) error {
    data, err := json.Marshal(inv)
    if err != nil {
        return fmt.Errorf("marshal inventory: %w", err)
    }
    return c.postWithAuth(ctx, c.cfg.InventoryURL, data)
}
```

CAPRF stores the inventory in the `RedfishHost` status:

```go
// api/v1alpha1/redfishhost_types.go
type RedfishHostStatus struct {
    Conditions []metav1.Condition    `json:"conditions,omitempty"`
    Inventory  *HardwareInventory    `json:"inventory,omitempty"`
    LastInspection metav1.Time       `json:"lastInspection,omitempty"`
}
```

### Integration with Provisioning

Inventory collection runs as an early provisioning step and can gate
provisioning on hardware requirements:

```go
// pkg/provision/orchestrator.go
steps := []Step{
    {Name: "collect-inventory", Fn: o.CollectInventory},
    {Name: "validate-hardware", Fn: o.ValidateHardware},  // optional
    // ... existing steps ...
}
```

## Affected Files

| File | Change |
|------|--------|
| `pkg/inventory/types.go` | New — data model structs |
| `pkg/inventory/collector.go` | New — sysfs/procfs parsing |
| `pkg/inventory/collector_test.go` | New — unit tests with mock sysfs |
| `pkg/caprf/client.go` | Add `ReportInventory()` |
| `pkg/config/provider.go` | Add `InventoryURL` field |
| `pkg/provision/orchestrator.go` | Add `CollectInventory()` step |
| CAPRF `internal/server/` | Add inventory endpoint |
| CAPRF `api/v1alpha1/redfishhost_types.go` | Add `Inventory` to status |

## Risks

- **DIMM details**: Full DIMM layout requires `dmidecode` or SMBIOS parsing,
  which needs root and may not work in all initrd environments.
- **Data size**: Full PCIe inventory can be large (>50 KB JSON). Consider
  optional verbosity levels.
- **Privacy**: Serial numbers and UUIDs are sensitive. Ensure they're stored
  securely and not logged.

## Effort Estimate

- Core collector (sysfs parsing): **3-4 days**
- CAPRF server endpoint + storage: **2-3 days**
- CRD extension + status display: **2 days**
- Testing: **2-3 days**
- Total: **9-12 days**
