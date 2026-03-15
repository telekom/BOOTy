# Proposal: Cloud-Init Configuration Management

## Status: Phase 1 Implemented (PR #42)

## Priority: P1

## Summary

Add comprehensive cloud-init configuration generation and injection during
provisioning. BOOTy generates cloud-init `user-data`, `meta-data`, and
`network-config` from CAPRF-provided machine configuration, writes them to
the provisioned OS (NoCloud datasource or configdrive partition), and
validates cloud-init syntax before finalizing.

## Motivation

Cloud-init is the industry standard for first-boot configuration of Linux
servers. BOOTy currently provisions the OS image but doesn't manage
post-boot configuration:

| Configuration Need | Current Solution | Proposed |
|-------------------|-----------------|----------|
| Hostname | Set in image | cloud-init sets per machine |
| SSH keys | Not managed | Injected via user-data |
| Network (post-provision) | Static in image | cloud-init network-config v2 |
| Users/groups | Image default | Created via user-data |
| Package installation | Not managed | cloud-init run_cmd |
| Kubernetes bootstrap | Manual | kubeadm join via run_cmd |
| NTP configuration | Not managed | cloud-init ntp module |

### Industry Context

| Tool | Cloud-Init Support |
|------|-------------------|
| **Ironic** | Configdrive with cloud-init metadata |
| **MAAS** | Full cloud-init via curtin + NoCloud |
| **Tinkerbell** | Metadata service |
| **OpenStack** | HTTP metadata service + configdrive |

## Design

### Cloud-Init Architecture

```
CAPRF MachineConfig
  │
  ├─ hostname, sshKeys, users → user-data (YAML)
  ├─ serial, uuid, network → meta-data (YAML)
  └─ network config → network-config (YAML v2)
      │
      ▼
BOOTy Provisioning
  │
  ├─ Method 1: NoCloud datasource
  │   └─ Write to /var/lib/cloud/seed/nocloud/{user-data,meta-data,network-config}
  │
  └─ Method 2: Configdrive partition
      └─ Create FAT32 partition with openstack/latest/{user_data,meta_data.json}
```

### Cloud-Init Generator

```go
// pkg/cloudinit/generator.go
package cloudinit

import (
    "fmt"
    "gopkg.in/yaml.v3"
)

// UserData represents cloud-init user-data.
type UserData struct {
    Hostname         string         `yaml:"hostname,omitempty"`
    FQDN             string         `yaml:"fqdn,omitempty"`
    ManageEtcHosts   bool           `yaml:"manage_etc_hosts,omitempty"`
    Users            []User         `yaml:"users,omitempty"`
    SSHAuthorizedKeys []string      `yaml:"ssh_authorized_keys,omitempty"`
    Packages         []string       `yaml:"packages,omitempty"`
    PackageUpdate    bool           `yaml:"package_update,omitempty"`
    RunCmd           [][]string     `yaml:"runcmd,omitempty"`
    WriteFiles       []WriteFile    `yaml:"write_files,omitempty"`
    NTP              *NTPConfig     `yaml:"ntp,omitempty"`
    Timezone         string         `yaml:"timezone,omitempty"`
    PowerState       *PowerState    `yaml:"power_state,omitempty"`
}

type User struct {
    Name              string   `yaml:"name"`
    Groups            string   `yaml:"groups,omitempty"`
    Shell             string   `yaml:"shell,omitempty"`
    Sudo              string   `yaml:"sudo,omitempty"`
    SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
    LockPasswd        bool     `yaml:"lock_passwd"`
}

type WriteFile struct {
    Path        string `yaml:"path"`
    Content     string `yaml:"content"`
    Owner       string `yaml:"owner,omitempty"`
    Permissions string `yaml:"permissions,omitempty"`
}

type NTPConfig struct {
    Enabled bool     `yaml:"enabled"`
    Servers []string `yaml:"servers,omitempty"`
    Pools   []string `yaml:"pools,omitempty"`
}

type PowerState struct {
    Mode    string `yaml:"mode"`    // "reboot", "poweroff"
    Message string `yaml:"message"`
    Delay   string `yaml:"delay"`   // "+1", "now"
}

// MetaData represents cloud-init meta-data.
type MetaData struct {
    InstanceID    string `yaml:"instance-id"`
    LocalHostname string `yaml:"local-hostname"`
    Platform      string `yaml:"platform,omitempty"`
}

// NetworkConfig represents cloud-init network-config v2.
type NetworkConfig struct {
    Version   int                    `yaml:"version"` // 2
    Ethernets map[string]EthConfig   `yaml:"ethernets,omitempty"`
    Bonds     map[string]BondConfig  `yaml:"bonds,omitempty"`
    VLANs     map[string]VLANConfig  `yaml:"vlans,omitempty"`
}

type EthConfig struct {
    Match       *MatchConfig  `yaml:"match,omitempty"`
    DHCP4       bool          `yaml:"dhcp4,omitempty"`
    Addresses   []string      `yaml:"addresses,omitempty"`
    Gateway4    string        `yaml:"gateway4,omitempty"`
    Nameservers *NSConfig     `yaml:"nameservers,omitempty"`
    MTU         int           `yaml:"mtu,omitempty"`
}

type BondConfig struct {
    Interfaces  []string      `yaml:"interfaces"`
    Parameters  *BondParams   `yaml:"parameters,omitempty"`
    Addresses   []string      `yaml:"addresses,omitempty"`
    DHCP4       bool          `yaml:"dhcp4,omitempty"`
}

type BondParams struct {
    Mode              string `yaml:"mode"`                // "802.3ad"
    LACPRate          string `yaml:"lacp-rate,omitempty"`
    TransmitHashPolicy string `yaml:"transmit-hash-policy,omitempty"`
    MIIMonitorInterval int   `yaml:"mii-monitor-interval,omitempty"`
}

type MatchConfig struct {
    MACAddress string `yaml:"macaddress,omitempty"`
    Driver     string `yaml:"driver,omitempty"`
}

type NSConfig struct {
    Addresses []string `yaml:"addresses,omitempty"`
    Search    []string `yaml:"search,omitempty"`
}

type VLANConfig struct {
    ID    int    `yaml:"id"`
    Link  string `yaml:"link"`
    DHCP4 bool   `yaml:"dhcp4,omitempty"`
}

// Generate creates cloud-init configuration from CAPRF machine config.
func Generate(cfg *MachineCloudInit) (*UserData, *MetaData, *NetworkConfig, error) {
    userData := &UserData{
        Hostname:       cfg.Hostname,
        FQDN:           cfg.FQDN,
        ManageEtcHosts: true,
        Users:          cfg.Users,
        SSHAuthorizedKeys: cfg.SSHKeys,
        Packages:       cfg.Packages,
        RunCmd:         cfg.RunCommands,
        WriteFiles:     cfg.WriteFiles,
    }

    metaData := &MetaData{
        InstanceID:    cfg.Serial,
        LocalHostname: cfg.Hostname,
        Platform:      "booty",
    }

    networkConfig := generateNetworkConfig(cfg)

    return userData, metaData, networkConfig, nil
}

// Render serializes cloud-init config to YAML with #cloud-config header.
func (u *UserData) Render() ([]byte, error) {
    data, err := yaml.Marshal(u)
    if err != nil {
        return nil, fmt.Errorf("marshal user-data: %w", err)
    }
    return append([]byte("#cloud-config\n"), data...), nil
}
```

### NoCloud Injection

```go
// pkg/cloudinit/inject.go
package cloudinit

import (
    "fmt"
    "os"
    "path/filepath"
)

// InjectNoCloud writes cloud-init files to the NoCloud seed directory.
func InjectNoCloud(rootPath string, userData, metaData, networkConfig []byte) error {
    seedDir := filepath.Join(rootPath, "var", "lib", "cloud", "seed", "nocloud")
    if err := os.MkdirAll(seedDir, 0o755); err != nil {
        return fmt.Errorf("create nocloud seed dir: %w", err)
    }

    files := map[string][]byte{
        "user-data":      userData,
        "meta-data":      metaData,
        "network-config": networkConfig,
    }

    for name, data := range files {
        path := filepath.Join(seedDir, name)
        if err := os.WriteFile(path, data, 0o644); err != nil {
            return fmt.Errorf("write %s: %w", name, err)
        }
    }
    return nil
}
```

### CAPRF Configuration

```yaml
# RedfishHost CR — cloud-init config
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: RedfishHost
metadata:
  name: server-001
spec:
  cloudInit:
    datasource: nocloud  # "nocloud" or "configdrive"
    hostname: k8s-worker-001
    fqdn: k8s-worker-001.cluster.local
    sshKeys:
      - "ssh-ed25519 AAAA... admin@example.com"
    users:
      - name: kubelet
        groups: docker
        sudo: "ALL=(ALL) NOPASSWD:ALL"
    packages:
      - containerd
      - kubelet
      - kubeadm
    runCommands:
      - ["kubeadm", "join", "--token", "xxx", "10.0.0.1:6443"]
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `mkfs.vfat` | `dosfstools` | FAT32 configdrive partition | full, gobgp | **No — add** (configdrive only) |

**Note**: NoCloud injection requires no binaries (just file writes).
Configdrive requires a FAT32 partition which needs `mkfs.vfat`.

**Dockerfile change** (tools stage, only for configdrive support):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    dosfstools \
    && rm -rf /var/lib/apt/lists/*

COPY --from=tools /sbin/mkfs.vfat bin/mkfs.vfat
```

### Configuration

```bash
# /deploy/vars
export CLOUDINIT_ENABLED="true"
export CLOUDINIT_DATASOURCE="nocloud"  # "nocloud" or "configdrive"
export CLOUDINIT_HOSTNAME="k8s-worker-001"
export CLOUDINIT_FQDN="k8s-worker-001.cluster.local"
export CLOUDINIT_SSH_KEYS='["ssh-ed25519 AAAA... admin"]'
export CLOUDINIT_RUN_CMD='[["kubeadm","join","--token","xxx","10.0.0.1:6443"]]'
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/cloudinit/generator.go` | Cloud-init config generation |
| `pkg/cloudinit/inject.go` | NoCloud + configdrive injection |
| `pkg/cloudinit/validate.go` | Syntax validation |
| `pkg/cloudinit/types.go` | Configuration types |
| `pkg/provision/orchestrator.go` | `injectCloudInit()` step |
| `pkg/config/provider.go` | Cloud-init config fields |
| `initrd.Dockerfile` | Add `mkfs.vfat` for configdrive support |

## Testing

### Unit Tests

- `cloudinit/generator_test.go` — Generate from various machine configs.
  Table-driven: minimal (hostname only), full (all fields), Kubernetes
  bootstrap, Windows-style line endings.
- `cloudinit/inject_test.go` — NoCloud injection with `t.TempDir()`.
  Verify file paths, permissions, content.
- `cloudinit/validate_test.go` — Valid YAML, invalid YAML, missing required
  fields, large payload.

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - Provision with cloud-init → boot → verify hostname set correctly
  - Provision with SSH key injection → verify key in authorized_keys
  - Verify cloud-init runs on first boot (check `/var/lib/cloud/instance/boot-finished`)

## Risks

| Risk | Mitigation |
|------|------------|
| Different cloud-init versions | Target v22+ (network-config v2 support) |
| YAML serialization edge cases | Use gopkg.in/yaml.v3; comprehensive tests |
| Configdrive partition layout varies | Support OpenStack and NoCloud formats |
| Sensitive data in user-data | Encrypted in transit via TLS; warn in docs |

## Effort Estimate

6–10 engineering days (generator + injection + validation + configdrive +
KVM tests).
