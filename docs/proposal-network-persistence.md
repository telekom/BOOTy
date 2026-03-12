# Proposal: Network Configuration Persistence

## Status: Proposal

## Priority: P2

## Summary

Persist the provisioning-time network configuration into the target OS so
the machine's production network "just works" on first boot — without relying
on cloud-init or external orchestration to re-discover and configure networking.
This covers static IPs, bonding, VLANs, DNS, routes, and EVPN underlay settings.

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

After writing the OS image and before the final reboot, BOOTy writes network
configuration files into the target OS filesystem. The format depends on the
target OS's network manager.

### Supported Formats

| OS | Network Manager | Config Path | Format |
|----|----------------|-------------|--------|
| Ubuntu 20.04+ | netplan + systemd-networkd | `/etc/netplan/` | YAML |
| RHEL 8+ | NetworkManager | `/etc/NetworkManager/system-connections/` | INI keyfile |
| Flatcar | systemd-networkd | `/etc/systemd/network/` | INI unit file |

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
        return p.writeNetplan() // sensible default
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
func (o *Orchestrator) PersistNetworkConfig(ctx context.Context) error {
    if !o.cfg.PersistNetwork {
        return nil
    }

    netPersist := &configurator.NetworkPersistence{
        rootDir:  o.rootDir,
        osFamily: o.cfg.OSFamily,
        config:   o.currentNetworkConfig(),
    }
    return netPersist.Write()
}
```

### Configuration

```bash
# /deploy/vars
export PERSIST_NETWORK="true"
export OS_FAMILY="ubuntu"  # or "rhel", "flatcar"
# Network config is derived from current BOOTy networking state
```

## Affected Files

| File | Change |
|------|--------|
| `pkg/provision/configurator/network.go` | New — network config writer |
| `pkg/provision/configurator/network_test.go` | New — unit tests |
| `pkg/provision/orchestrator.go` | Add `PersistNetworkConfig()` step |
| `pkg/config/provider.go` | Add `PersistNetwork`, `OSFamily` fields |

## Risks

- **OS detection**: Incorrect OS family detection writes wrong format. Should
  be explicitly set or auto-detected from the OS image (check for
  `/etc/os-release` in the chroot).
- **Conflicts**: If cloud-init also configures networking, there may be
  conflicts. BOOTy's config should take lowest priority (filename `01-*`).
- **Complex topologies**: EVPN/BGP underlay config cannot be persisted as
  simple netplan — requires additional service configuration (FRR or GoBGP).

## Effort Estimate

- Netplan writer: **2 days**
- NetworkManager writer: **2 days**
- systemd-networkd writer: **1 day**
- OS detection + integration: **2 days**
- Testing: **2-3 days**
- Total: **9-12 days**
