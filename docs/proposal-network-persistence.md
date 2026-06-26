# Proposal: Network Configuration Persistence

## Status: Implemented with limitations

Implemented as explicit opt-in network persistence without target OS
auto-detection. Current CI proves renderer unit behavior and synthetic KVM
wiring only; it does not prove first boot of real Ubuntu, Debian, RHEL-like,
Flatcar, Fedora, SUSE, Windows, or VMware ESXi target images.

## Priority: P2

## Summary

Persist selected provisioning-time network configuration into the target OS
filesystem when operators explicitly enable it with `PERSIST_NETWORK=true` and
set `OS_FAMILY`. This reduces reliance on cloud-init or external orchestration
to re-discover basic target networking, but first-boot behavior remains
target-OS-specific and must not be claimed without real image validation.

The implemented scope covers DHCP interfaces, static interfaces, DNS, gateways,
static routes, bonds, and VLANs only where the selected writer supports them. It
does not persist EVPN/BGP underlay state, does not auto-detect the target OS,
and intentionally rejects known unsupported layouts such as RHEL/Flatcar bonds
or VLANs.

## Motivation

Currently, BOOTy configures networking in the initrd (DHCP, static, bonds,
VLANs) but none of this configuration is carried into the provisioned OS.
The target OS boots with default networking and relies on cloud-init or
Kubernetes node setup to re-configure. This creates a gap where:

1. **No network on first boot**: Machine can't reach API server
2. **IP address change**: DHCP may assign a different IP after reboot
3. **Bond/VLAN loss**: Complex network setups must be re-created
4. **Race condition**: cloud-init may not run before kubelet starts

### Industry Context

| Tool | Network Persistence |
|------|-------------------|
| **Ironic** | Writes Neutron port config via configdrive; networking persists via cloud-init |
| **MAAS** | Writes full netplan config to provisioned OS |
| **Tinkerbell** | No built-in network persistence |

## Design

### Approach

After writing the OS image and before the final reboot, BOOTy can write network
configuration files into the target OS filesystem when `PERSIST_NETWORK=true`
and `OS_FAMILY` is set. The format depends on the selected target OS network
manager.

### Supported Formats

| OS family | Network manager | Config path | Format | Implemented scope |
|-----------|-----------------|-------------|--------|-------------------|
| Ubuntu | netplan + systemd-networkd | `/etc/netplan/` | YAML | Interfaces, DHCP, static addresses, gateways, DNS, static routes, bonds, and VLANs where configured |
| RHEL-like | NetworkManager | `/etc/NetworkManager/system-connections/` | INI keyfile | Interfaces, DHCP, static addresses, gateways, DNS, and static routes; bonds and VLANs are rejected |
| Flatcar | systemd-networkd | `/etc/systemd/network/` | INI unit file | Interfaces, DHCP, static addresses, gateways, DNS, and static routes; bonds and VLANs are rejected |

No current CI job proves first boot of a real distro image applying these files.

### Implementation

```go
// pkg/provision/configurator/network.go
package configurator

type NetworkPersistence struct {
    rootDir  string
    osFamily string  // "ubuntu", "rhel", "flatcar"
    config   NetworkConfig
}

type NetworkConfig struct {
    Interfaces []InterfaceConfig
    Bonds      []BondConfig
    VLANs      []VLANConfig
    DNS        DNSConfig
    Routes     []RouteConfig
}

type InterfaceConfig struct {
    Name    string
    MAC     string
    DHCP    bool
    Address string   // CIDR
    Gateway string
    MTU     int
}

func (p *NetworkPersistence) Write() error {
    switch p.osFamily {
    case "ubuntu":
        return p.writeNetplan()
    case "rhel":
        return p.writeNetworkManager()
    case "flatcar":
        return p.writeSystemdNetworkd()
    default:
        return fmt.Errorf("unsupported OS family %q", p.osFamily)
    }
}
```

### Netplan Output (Ubuntu)

```go
func (p *NetworkPersistence) writeNetplan() error {
    cfg := map[string]interface{}{
        "network": map[string]interface{}{
            "version":  2,
            "renderer": "networkd",
            "ethernets": p.buildEthernets(),
            "bonds":     p.buildBonds(),
            "vlans":     p.buildVLANs(),
        },
    }

    data, err := yaml.Marshal(cfg)
    if err != nil {
        return fmt.Errorf("marshal netplan: %w", err)
    }

    netplanDir := filepath.Join(p.rootDir, "etc", "netplan")
    if err := os.MkdirAll(netplanDir, 0755); err != nil {
        return err
    }
    return os.WriteFile(
        filepath.Join(netplanDir, "01-booty-provisioned.yaml"),
        data, 0600,
    )
}
```

### Integration with Provisioning

```go
// pkg/provision/orchestrator.go
func (o *Orchestrator) persistNetworkConfig() error {
    if !o.cfg.PersistNetwork {
        return nil
    }
    family, err := persist.ParseOSFamily(o.cfg.OSFamily)
    if err != nil {
        return err
    }
    cfg, err := o.targetNetworkConfig()
    if err != nil {
        return err
    }
    return persist.Write(o.config.rootDir, family, cfg)
}
```

### Configuration

```bash
# /deploy/vars
export PERSIST_NETWORK="true"
export OS_FAMILY="ubuntu"  # or "rhel", "flatcar"
# Network config is derived from current BOOTy networking state
```

`OS_FAMILY` is required. BOOTy fails closed for unsupported OS families instead
of guessing a renderer.

## Required Binaries in Initramfs

No additional binaries needed. Network config file generation is pure Go
(YAML/INI marshaling). The provisioned OS network tools are used at boot
time, not by BOOTy:

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `ip` | `iproute2` | Read current network state for config generation | all | **Yes** |

## Affected Files

| File | Change |
|------|--------|
| `pkg/network/persist/persist.go` | Network config writer |
| `pkg/network/persist/persist_test.go` | Writer unit tests |
| `pkg/provision/orchestrator.go` | Persist target network configuration from the existing `configure-dns` step |
| `pkg/config/config.go` | Add `PersistNetwork`, `OSFamily` fields |
| `pkg/caprf/client.go` | Parse `PERSIST_NETWORK`, `OS_FAMILY` vars |

## Risks

- **OS family selection**: `OS_FAMILY` must be explicitly set. BOOTy currently
  fails closed rather than auto-detecting the OS image from `/etc/os-release`.
- **Conflicts**: If cloud-init also configures networking, there may be
  conflicts. BOOTy's config should take lowest priority (filename `01-*`).
- **Complex topologies**: EVPN/BGP underlay config cannot be persisted as
  simple netplan — requires additional service configuration (FRR or GoBGP).
- **Writer gaps**: RHEL/NetworkManager and Flatcar/systemd-networkd persistence
  currently reject bonds and VLANs. VLAN gateway persistence is not implemented.
- **Coverage gaps**: CI exercises renderer unit tests and synthetic KVM wiring,
  not real distro first boot.

## Effort Estimate

- Implemented: explicit opt-in wiring, netplan, NetworkManager, and
  systemd-networkd writers with unit coverage.
- Remaining: real-image first-boot tests, target OS auto-detection if desired,
  RHEL/Flatcar bond/VLAN support, VLAN gateway persistence, and EVPN/BGP
  service persistence.
