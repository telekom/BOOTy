# Proposal: Local IPMI Management

## Status: Implemented (PR #45)

## Priority: P2

## Summary

Add local IPMI management capabilities to BOOTy: read/write BMC network
configuration, read sensor data (temperatures, fan speeds, voltages),
configure IPMI users, set boot order via IPMI, and send IPMI chassis
commands. Uses Go IPMI library for direct `/dev/ipmi0` access (preferred),
with `ipmitool` binary as fallback.

## Motivation

During bare-metal provisioning, BOOTy often needs to interact with the
server's BMC/IPMI for operations that can't be done via Redfish:

| Need | Why |
|------|-----|
| Read BMC IP | Discover BMC address for CAPRF reporting |
| Set BMC network | Configure BMC for out-of-band management |
| Read sensors | Temperature/power validation during provisioning |
| Set boot order | Ensure next boot is from disk, not PXE |
| Configure IPMI users | Bootstrap BMC access for monitoring systems |
| Chassis control | Graceful shutdown or power cycle |

### Industry Context

| Tool | IPMI Access |
|------|------------|
| **Ironic** | Remote IPMI via `ipmitool` for power management |
| **MAAS** | IPMI for power control and BMC discovery |
| **Tinkerbell** | IPMI for power management (tink-worker) |
| **Dell iDRAC** | racadm CLI for local BMC management |
| **HPE iLO** | hponcfg for local iLO configuration |

## Design

### IPMI Manager

```go
// pkg/ipmi/manager.go
package ipmi

import (
    "context"
    "fmt"
    "log/slog"
)

// Manager handles local IPMI operations via /dev/ipmi0.
type Manager struct {
    device string // "/dev/ipmi0"
    log    *slog.Logger
}

func New(log *slog.Logger) *Manager {
    return &Manager{
        device: "/dev/ipmi0",
        log:    log,
    }
}

// BMCNetConfig holds BMC network configuration.
type BMCNetConfig struct {
    IPAddress  string `json:"ipAddress"`
    Netmask    string `json:"netmask"`
    Gateway    string `json:"gateway"`
    MACAddress string `json:"macAddress"`
    DHCP       bool   `json:"dhcp"`
    VLANEnabled bool  `json:"vlanEnabled"`
    VLANID     int    `json:"vlanId"`
}

// GetBMCNetwork reads the BMC network configuration via IPMI.
func (m *Manager) GetBMCNetwork(ctx context.Context) (*BMCNetConfig, error) {
    // IPMI command: Get LAN Configuration Parameters (0x0C/0x02)
    // Channel 1, parameters: IP Address (3), Subnet Mask (6), Default GW (12), MAC (5)

    ip, err := m.getLANParam(ctx, 1, 3)
    if err != nil {
        return nil, fmt.Errorf("get BMC IP: %w", err)
    }

    mask, err := m.getLANParam(ctx, 1, 6)
    if err != nil {
        return nil, fmt.Errorf("get BMC netmask: %w", err)
    }

    gw, err := m.getLANParam(ctx, 1, 12)
    if err != nil {
        return nil, fmt.Errorf("get BMC gateway: %w", err)
    }

    mac, err := m.getLANParam(ctx, 1, 5)
    if err != nil {
        return nil, fmt.Errorf("get BMC MAC: %w", err)
    }

    return &BMCNetConfig{
        IPAddress:  formatIP(ip),
        Netmask:    formatIP(mask),
        Gateway:    formatIP(gw),
        MACAddress: formatMAC(mac),
    }, nil
}

// SetBMCNetwork configures the BMC network.
func (m *Manager) SetBMCNetwork(ctx context.Context, cfg BMCNetConfig) error {
    if cfg.DHCP {
        return m.setLANParam(ctx, 1, 4, []byte{1}) // IP Source = DHCP
    }
    // Static IP
    if err := m.setLANParam(ctx, 1, 4, []byte{0}); err != nil { // IP Source = static
        return fmt.Errorf("set IP source: %w", err)
    }
    if err := m.setLANParam(ctx, 1, 3, parseIP(cfg.IPAddress)); err != nil {
        return fmt.Errorf("set BMC IP: %w", err)
    }
    if err := m.setLANParam(ctx, 1, 6, parseIP(cfg.Netmask)); err != nil {
        return fmt.Errorf("set BMC netmask: %w", err)
    }
    return m.setLANParam(ctx, 1, 12, parseIP(cfg.Gateway))
}
```

### Sensor Reading

```go
// pkg/ipmi/sensors.go
package ipmi

import "context"

// SensorReading represents an IPMI sensor value.
type SensorReading struct {
    Name     string  `json:"name"`
    Value    float64 `json:"value"`
    Unit     string  `json:"unit"`     // "°C", "RPM", "V", "W"
    Status   string  `json:"status"`   // "ok", "warning", "critical"
    LowerWarn float64 `json:"lowerWarn,omitempty"`
    UpperWarn float64 `json:"upperWarn,omitempty"`
    LowerCrit float64 `json:"lowerCrit,omitempty"`
    UpperCrit float64 `json:"upperCrit,omitempty"`
}

// GetSensors reads all SDR (Sensor Data Record) entries.
func (m *Manager) GetSensors(ctx context.Context) ([]SensorReading, error) {
    // IPMI command: Get SDR Repository Info (0x0A/0x20)
    // Then iterate: Reserve SDR (0x0A/0x22) + Get SDR (0x0A/0x23)
    // For each sensor: Get Sensor Reading (0x04/0x2D)
    return m.readAllSensors(ctx)
}

// GetTemperatures returns only temperature sensors.
func (m *Manager) GetTemperatures(ctx context.Context) ([]SensorReading, error) {
    sensors, err := m.GetSensors(ctx)
    if err != nil {
        return nil, err
    }
    var temps []SensorReading
    for _, s := range sensors {
        if s.Unit == "°C" {
            temps = append(temps, s)
        }
    }
    return temps, nil
}
```

### Boot Order Management

```go
// pkg/ipmi/boot.go
package ipmi

import (
    "context"
    "fmt"
)

// BootDevice represents an IPMI boot target.
type BootDevice string

const (
    BootPXE    BootDevice = "pxe"
    BootDisk   BootDevice = "disk"
    BootCDROM  BootDevice = "cdrom"
    BootBIOS   BootDevice = "bios-setup"
)

// SetNextBoot sets the boot device for the next boot only.
func (m *Manager) SetNextBoot(ctx context.Context, device BootDevice) error {
    // IPMI command: Set System Boot Options (0x00/0x08)
    // Parameter 5: Boot Flags
    var bootFlags byte
    switch device {
    case BootPXE:
        bootFlags = 0x04
    case BootDisk:
        bootFlags = 0x08
    case BootCDROM:
        bootFlags = 0x14
    case BootBIOS:
        bootFlags = 0x18
    default:
        return fmt.Errorf("unknown boot device: %s", device)
    }

    // Set valid flag (bit 7) + boot device
    return m.setBootOptions(ctx, 5, []byte{0x80, bootFlags, 0x00, 0x00, 0x00})
}
```

### Low-Level IPMI Access

```go
// pkg/ipmi/driver.go
//go:build linux

package ipmi

import (
    "fmt"
    "os"
    "unsafe"

    "golang.org/x/sys/unix"
)

// ipmiRequest represents an IPMI request to /dev/ipmi0.
type ipmiRequest struct {
    addr    [32]byte
    msgid   int64
    msg     ipmiMsg
}

type ipmiMsg struct {
    netfn   byte
    cmd     byte
    dataLen uint16
    data    unsafe.Pointer
}

// sendCommand sends a raw IPMI command via /dev/ipmi0.
func (m *Manager) sendCommand(netfn, cmd byte, data []byte) ([]byte, error) {
    f, err := os.OpenFile(m.device, os.O_RDWR, 0)
    if err != nil {
        m.log.Info("cannot open IPMI device, falling back to ipmitool", "error", err)
        return m.sendViaIpmitool(netfn, cmd, data)
    }
    defer f.Close()

    // Build IPMI request structure
    // ioctl(fd, IPMICTL_SEND_COMMAND, &req)
    // poll for response
    // ioctl(fd, IPMICTL_RECEIVE_MSG, &resp)
    return m.ioctlSendReceive(f, netfn, cmd, data)
}

// sendViaIpmitool is the fallback using ipmitool binary.
func (m *Manager) sendViaIpmitool(netfn, cmd byte, data []byte) ([]byte, error) {
    // ipmitool raw <netfn> <cmd> [data bytes...]
    return nil, nil
}
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `ipmitool` | `ipmitool` | IPMI operations (fallback) | full, gobgp | **No — add** |

**Kernel modules needed**:

```dockerfile
# IPMI kernel modules
for m in ... \
    ipmi_msghandler ipmi_devintf ipmi_si ipmi_ssif; do \
    find "$MDIR" -name "${m}.ko*" -exec cp {} /modules/ \; 2>/dev/null || true; \
done
```

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    ipmitool \
    && rm -rf /var/lib/apt/lists/*

# IPMI tools (fallback)
COPY --from=tools /usr/bin/ipmitool bin/ipmitool
```

**Go-first approach**: BOOTy uses direct `/dev/ipmi0` ioctl for all IPMI
operations. The `ipmitool` binary is fallback only for hardware where the
Go driver doesn't work.

### Configuration

```bash
# /deploy/vars
export IPMI_ENABLED="true"
export IPMI_BMC_NETWORK_DHCP="false"
export IPMI_BMC_IP="10.0.100.1"
export IPMI_BMC_NETMASK="255.255.255.0"
export IPMI_BMC_GATEWAY="10.0.100.254"
export IPMI_SET_BOOT_DISK="true"  # set next boot to disk after provisioning
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/ipmi/manager.go` | IPMI manager (network, boot, chassis) |
| `pkg/ipmi/sensors.go` | Sensor data reading |
| `pkg/ipmi/boot.go` | Boot order management |
| `pkg/ipmi/driver.go` | Low-level /dev/ipmi0 access (linux-only) |
| `pkg/ipmi/ipmitool.go` | ipmitool binary fallback |
| `pkg/provision/orchestrator.go` | IPMI steps (read BMC, set boot order) |
| `pkg/caprf/client.go` | Report BMC network to CAPRF |
| `initrd.Dockerfile` | Add `ipmitool` binary, IPMI kernel modules |

## Testing

### Unit Tests

- `ipmi/manager_test.go` — BMC network config parsing/formatting.
  Table-driven: static IP, DHCP, VLAN.
- `ipmi/sensors_test.go` — SDR record parsing with raw byte fixtures.
- `ipmi/boot_test.go` — Boot flag encoding for each device type.

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with `-device ipmi-bmc-sim` (QEMU IPMI BMC simulator)
  - Verify: read BMC IP address
  - Verify: read sensor values
  - Verify: set boot device to disk

## Risks

| Risk | Mitigation |
|------|------------|
| /dev/ipmi0 not available | Load ipmi_si module; fall back to ipmitool |
| IPMI implementations vary by vendor | Test on Supermicro, Dell, HPE, Lenovo |
| ipmitool binary size (~1 MB) | Acceptable for full/gobgp flavors |
| Raw IPMI ioctl complex | Use existing Go IPMI libraries as reference |

## Effort Estimate

8–12 engineering days (Go IPMI driver + ipmitool fallback + sensors +
boot order + KVM tests).
