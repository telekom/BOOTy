# SR-IOV

BOOTy reports the SR-IOV state of every physical NIC function (PF) in the
hardware inventory. This document describes the report and the handoff
contract: what BOOTy guarantees to the Kubernetes side (the SR-IOV network
operator, the DRA SR-IOV driver and the T-CaaS node tuning) when it hands
the host over to the installed OS.

The design is in [proposal-sriov-nic-config.md](proposal-sriov-nic-config.md).

## Report

`collect-inventory` (`INVENTORY_ENABLED=true`) adds an `sriov` object to
every NIC in the inventory JSON that is a PF port. A PF port is a netdev
with a parent device that is not a VF (no `physfn` link) and not a VF or SF
representor (`phys_port_name` such as `pf0vf3` or `c1pf0sf2`). BOOTy reads
only kernel interfaces: sysfs, devlink (generic netlink), rtnetlink and the
ethtool ioctl. It needs no vendor tool, so the report works for every NIC
family.

| Field | Source | Notes |
|-------|--------|-------|
| `interface`, `mac` | `/sys/class/net/<if>`, `address` | |
| `bus`, `pciAddress` | `device/subsystem`, `device` links | The devlink handle is `<bus>/<pciAddress>`. `bus` is `pci` on hardware. |
| `vendorId`, `deviceId` | `device/vendor`, `device/device` | Lower-case hex without `0x`, for example `15b3` and `101d`. |
| `driver` | `device/driver` link | `mlx5_core`, `ice`, `i40e`, `ixgbe`, `bnxt_en`, `qede`, `cxgb4`, `sfc`, ... |
| `firmware` | ethtool `ETHTOOL_GDRVINFO`, else devlink running `fw`, `fw.version` or `fw.mgmt` | |
| `numaNode` | `device/numa_node` | `-1` when the platform has no NUMA information. |
| `linkType` | `type` (ARPHRD) | `eth`, `ib`, or `arphrd-<n>`. |
| `sriovCapable` | `sriovTotalVFs > 0` | `false` when SR-IOV is off in the NIC firmware or the BIOS: the device then has no SR-IOV capability. |
| `sriovTotalVFs`, `sriovNumVFs` | `device/sriov_totalvfs`, `device/sriov_numvfs` | Total VFs is the limit after the last firmware reset. |
| `eswitch.capability` | devlink `DEVLINK_CMD_ESWITCH_GET` | `supported` when the driver answers, `unsupported` on `EOPNOTSUPP` or without a devlink instance, `unknown` on other errors. |
| `eswitch.mode` | devlink | `legacy` or `switchdev`. Also `inlineMode` and `encapMode` when the driver sets them. |
| `physSwitchId`, `physPortName` | `phys_switch_id`, `phys_port_name` | mlx5 sets `phys_switch_id` only in switchdev mode. |
| `portCount` | PF ports on the same PCI slot | For example 2 on a dual-port ConnectX. |
| `vfLag`, `vfLagReason` | driver, `portCount`, eSwitch | The kernel has no VF LAG query, see [VF LAG](#vf-lag). |
| `devlink` | devlink `DEVLINK_CMD_INFO_GET` | Handle, driver, serial number and the `fixed`, `running` and `stored` versions. A stored version that differs from the running one means a firmware reset is pending. |
| `vfs` | rtnetlink `IFLA_VFINFO_LIST`, `device/virtfn<N>` | Only when `sriovNumVFs > 0`: index, VF PCI address, MAC, VLAN, QoS, min and max TX rate (Mbit/s), spoof check, trust, link state. |
| `warnings` | | Each failed read. A failed read leaves its field empty; it never fails the inventory. |

Example (a ConnectX-6 Dx port in legacy mode):

```json
{"interface":"ens1f0np0","mac":"b8:ce:f6:00:00:01","bus":"pci",
 "pciAddress":"0000:3b:00.0","vendorId":"15b3","deviceId":"101d",
 "driver":"mlx5_core","firmware":"22.39.1002 (MT_0000000359)","numaNode":0,
 "linkType":"eth","sriovCapable":true,"sriovTotalVFs":64,"sriovNumVFs":0,
 "eswitch":{"capability":"supported","mode":"legacy"},
 "physPortName":"p0","portCount":2,"vfLag":"supported",
 "vfLagReason":"mlx5 VF LAG: bond both PF ports of the adapter (switchdev, or legacy mode with the LAG bond before the VFs)",
 "devlink":{"handle":"pci/0000:3b:00.0","driver":"mlx5_core",
            "running":[{"name":"fw.version","value":"22.39.1002"}]}}
```

### VF LAG

The kernel has no VF LAG capability query. BOOTy reports:

| Driver | `vfLag` |
|--------|---------|
| `mlx5_core` | `supported` with an Ethernet link, two PF ports on the adapter and a devlink eSwitch. `unsupported` otherwise, with the reason. |
| `ice` | `unknown` with two PF ports (SR-IOV LAG depends on the kernel, the NVM and the DDP package), `unsupported` with one. |
| `bnxt_en` | `unknown` with two PF ports and a devlink eSwitch (needs TruFlow firmware), `unsupported` otherwise. |
| Other drivers | `unsupported`. |

## Profiles

A profile is the desired SR-IOV state of one NIC kind in a host class.
CAPRF passes the profiles of the host class in `SRIOV_PROFILES` (a JSON
list) or YAML `provision.sriov.profiles`:

```yaml
provision:
  sriov:
    onDrift: fail          # SRIOV_ON_DRIFT: warn (default), fail, apply
    nicTools: opensource   # NIC_TOOLS: kernel, opensource (default), vendor
    profiles:
      - name: cx6dx
        match: {vendorId: "15b3", deviceId: "101d"}
        firmware:
          sriovEnabled: true
          maxVFs: 64
          linkType: eth
          nvConfig:
            - {name: CQE_COMPRESSION, value: "1"}
        runtime:
          numVFs: 8
          eswitchMode: switchdev
          vfDefaults: {spoofChk: true, trust: false}
          vfs:
            - {index: 0, mac: "02:00:00:00:00:10", vlan: 100, qos: 3, maxTxRate: 10000}
      - name: e810
        match: {driver: ice}
        optional: true
        firmware: {fwLLDPAgent: false}
```

| Field | Meaning |
|-------|---------|
| `name` | 1 to 63 letters, digits, `.`, `_`, `-`; unique. |
| `match` | `vendorId` and `deviceId` (four lower-case hex digits), `driver`, `mac`, `interface` (a name or a shell pattern such as `ens1f*`). Every set field must match; at least one is required. A PF port that matches two profiles fails the run. |
| `firmware.sriovEnabled` | SR-IOV in the NIC firmware. Active value: `sriovCapable`. |
| `firmware.maxVFs` | VF limit, 0 to 65535. Active value: `sriovTotalVFs`. |
| `firmware.linkType` | `eth` or `ib` (VPI adapters). Active value: `linkType`. |
| `firmware.fwLLDPAgent` | Intel firmware LLDP/DCBX agent; `false` lets the host run LLDP and DCBX. |
| `firmware.nvConfig` | More NVIDIA/Mellanox NV parameters as `{name, value}` with the `mstconfig` names. `SRIOV_EN`, `NUM_OF_VFS` and `LINK_TYPE_P<n>` are rejected: use the fields above. |
| `firmware.devlinkParams` | devlink parameters in the permanent (NIC-stored) mode as `{name, value}`. |
| `runtime.numVFs` | `sriov_numvfs`; not above `firmware.maxVFs`. |
| `runtime.eswitchMode` | `legacy` or `switchdev`. |
| `runtime.vfDefaults`, `runtime.vfs[]` | VF attributes: `trust`, `spoofChk`, `vlan` (0 to 4095), `qos` (0 to 7, needs a VLAN), `minTxRate`, `maxTxRate` (Mbit/s), `linkState` (`auto`, `enable`, `disable`); `vfs[]` also `index` and `mac` (unicast). They need `numVFs` > 0. |
| `runtime.handoff` | `reset` (default) or `keep`. |
| `onDrift` | Overrides `SRIOV_ON_DRIFT` for this profile. |
| `optional` | The profile may match no PF port; otherwise that is drift. |

### Drift check

The `configure-sriov` step runs after `setup-mellanox`. Without profiles it
does nothing. It compares every firmware setting of a profile with two
values:

- the stored value, which a firmware backend reads from the NIC (NV config
  or a permanent devlink parameter), and
- the active value, which the kernel shows (`sriovCapable`,
  `sriovTotalVFs`, `linkType`).

| Status | Meaning |
|--------|---------|
| `ok` | The setting has the wanted value. |
| `drift` | The stored value differs, or the active value differs and there is no stored value. |
| `pending-reset` | The stored value is right, the active one is not: a firmware reset or a power cycle is pending, or the BIOS blocks it (for example SR-IOV off in the BIOS). |
| `unverified` | Neither value is readable, for example no backend for the driver or `NIC_TOOLS` excludes it. |
| `applied` | BOOTy stored the value; it takes effect after the reboot. |
| `failed` | The apply failed or is not possible. |

`onDrift` decides what happens to a profile that is not compliant (any
status other than `ok` and `applied`):

- `warn` logs every finding (`SR-IOV drift`) and goes on.
- `fail` stops provisioning with the findings.
- `apply` stores the drifted settings through the backend of the driver.
  BOOTy then reboots instead of kexec, so the new values are active in the
  installed OS. A `pending-reset` finding is not applied again: a reboot
  already happened before BOOTy ran, so another one would loop. It fails the
  run and asks for a power cycle or a BIOS check.

BOOTy logs a summary line `SR-IOV compliance` with the finding counts.

### Firmware backends

A backend reads and stores the firmware settings of a NIC family. A PF port
can have several; each setting belongs to the first backend that reads a
value for it, and its apply goes there. `NIC_TOOLS` decides which backends
BOOTy may use.

| Backend | `NIC_TOOLS` | NICs | Settings | Takes effect |
|---------|-------------|------|----------|--------------|
| `mstconfig` (mstflint) | `opensource` | NVIDIA ConnectX-4 Lx to 8 and BlueField (`mlx5_core`), ConnectX-3 (`mlx4_core`) | `sriovEnabled` (`SRIOV_EN`), `maxVFs` (`NUM_OF_VFS`), `linkType` (`LINK_TYPE_P<port>`), any `nvConfig` parameter, for example `CQE_COMPRESSION`, `FLEX_PARSER_PROFILE_ENABLE`, `NUM_VF_MSIX`, or the BlueField `INTERNAL_CPU_OFFLOAD_ENGINE` (DPU or NIC mode) | after the reboot |
| `mlxconfig` (MFT) | `vendor` | same, when a site flavor ships MFT and mstconfig is missing | same | after the reboot |
| `devlink` | `kernel` | every driver with devlink parameters in the permanent mode, for example `bnxt_en` | `sriovEnabled` (generic `enable_sriov`), `devlinkParams` | after the reboot |
| `ethtool` | `opensource` | Intel `ice`, `i40e` | `fwLLDPAgent` (`fw-lldp-agent` on ice, `disable-fw-lldp` inverted on i40e) | at once, no reboot |

`mstconfig -e -d <pci> query` gives the default, the current (active) and
the next-boot (stored) value of every NV parameter, so BOOTy also sees a
pending reset of a parameter that the kernel does not show. All changes for
one port go in one `mstconfig -y -d <pci> set` call. mstflint 4.31 is in
the default ramdisk (Debian trixie package `mstflint`, licence BSD-2-clause
or GPL-2). The port of `LINK_TYPE_P<n>` comes from `phys_port_name`
(`p0` is port 1), else from the PCI function.

When no backend reads a setting, the finding names the vendor tool:

| Family | Driver | Firmware tool without a BOOTy backend |
|--------|--------|---------------------------------------|
| Intel E810, X710, X540/X550 | `ice`, `i40e`, `ixgbe` | VF limit and SR-IOV enable are in the NVM: `nvmupdate64e` (vendor flavor) or the Redfish NIC attributes of the BMC. The active value (`sriovTotalVFs`) is still checked. |
| Broadcom NetXtreme-E | `bnxt_en` | VF limit: `niccli` or `bnxtnvm` (vendor flavor) or Redfish; SR-IOV enable works through devlink. |
| Marvell/QLogic FastLinQ | `qede` | vendor NVM tool or Redfish |
| Chelsio T5/T6 | `cxgb4` | firmware configuration file and `cxgbtool` |
| AMD Solarflare | `sfc`, `sfc_ef100` | `sfboot` (vendor flavor) |
| AMD Pensando | `ionic` | vendor tooling |

With `onDrift: apply`, BOOTy stores the drifted settings and, when a
backend says so, reboots instead of kexec (`FirmwareChanged`). A NIC that
needs a cold power cycle for a setting shows `pending-reset` in the next
run; CAPRF should power-cycle the host then.

#### setup-mellanox and the profiles

When a profile sets `sriovEnabled` or `maxVFs` and can match a ConnectX
port (`vendorId` `15b3`, a `mlx*_core` driver, or neither in the match),
the profiles own `NUM_OF_VFS` and `setup-mellanox` (`NUM_VFS`,
`NUM_VFS_MODE`) does nothing. Otherwise the two would write different
values on every provision.

### Runtime settings

Runtime settings do not survive the kexec or the reboot into the installed
OS. BOOTy applies them in the ramdisk to prove that the PF ports can run
them, checks the result, and by default restores the state from before:

1. Group the PF ports by device (devlink handle): ports of one device share
   `sriov_numvfs` and the eSwitch. Two ports of a device that want
   different values fail.
2. eSwitch mode through devlink (`DEVLINK_CMD_ESWITCH_SET`), with the VFs
   off first. BOOTy never changes the eSwitch of a port the ramdisk uses (a
   master device, an address other than IPv6 link-local, or a unicast route
   through it, such as the BGP unnumbered routes): mlx5 replaces the PF
   netdev by the uplink representor in switchdev mode, which would drop the
   provisioning network.
3. `sriov_numvfs` through sysfs, through 0 when the count changes.
4. VF attributes through rtnetlink on the PF netdev, for each VF below
   `numVFs`: `vfDefaults` merged with the `vfs[]` entry of the index. MAC,
   VLAN and QoS, min and max rate, spoof check, trust and link state, in the
   order of `ip link set <pf> vf N`.
5. Read the ports again and compare `eswitchMode`, `numVFs` and each VF
   (`vfs[N]`, with the wanted and the read attributes).
6. `handoff: reset` (the default) turns the VFs off, restores the eSwitch
   mode if BOOTy changed it and restores an earlier VF count, then checks
   it (`handoff`). `handoff: keep` leaves the state, for modes in which the
   ramdisk keeps running (standby, agent).

Runtime findings are `ok` or `failed`. `onDrift` `warn` logs a failure,
`fail` and `apply` stop the run. The runtime settings use only kernel
interfaces, so they work on every driver that implements them: `mlx5_core`,
`ice`, `i40e`, `ixgbe`, `bnxt_en`, `qede`, `cxgb4`, `sfc` and `igb`. In
switchdev mode, mlx5 does not take VLAN, QoS, trust and spoof check through
the PF netdev; those fail with the driver error.

## Handoff contract

The handoff is the kexec into the installed OS, or the reboot when a
firmware setting changed. The Kubernetes side owns the runtime state after
it: it creates the VFs, sets the eSwitch mode and the VF attributes, and
runs the representors and tc-flower offload.

BOOTy guarantees at the handoff, for every PF port:

1. The inventory has the `sriov` report of the port, read after every
   change that BOOTy made.
2. BOOTy never leaves VFs or a switchdev eSwitch behind that it created
   (`handoff: reset`, the default): the installed OS starts with the state
   that the NIC firmware and the OS driver defaults give (`sriovNumVFs` 0,
   eSwitch `legacy`). With profiles, the runtime settings of every matched
   PF port worked in the ramdisk, or the run failed.
3. A firmware-level setting that BOOTy changed is active: BOOTy reboots
   instead of kexec after the change, so `sriovTotalVFs`, the link type and
   the SR-IOV enable are the new values in the installed OS.
4. With profiles, every firmware setting of every matched PF port is
   compliant, or the run failed (`onDrift` `fail` or `apply`), or the drift
   is in the log (`warn`).

BOOTy does not guarantee:

- Runtime settings in the installed OS: `sriov_numvfs`, the eSwitch mode,
  VF trust, spoof check, VLAN, QoS, rate, MAC and link state do not survive
  the kexec or the reboot. The SR-IOV network operator or the DRA driver
  sets them.
- Representors, tc-flower offload, OVS, bonding of PFs for VF LAG, and the
  DDP package of the OS.

### Consumers

| Consumer | Uses | Field |
|----------|------|-------|
| DRA SR-IOV driver (`k8snetworkplumbingwg/dra-driver-sriov`) | eSwitch mode and detection (`phys_switch_id`, upstream PR #120), PF name, vendor and device ID, NUMA node, link type | `eswitch.mode`, `physSwitchId`, `interface`, `vendorId`, `deviceId`, `numaNode`, `linkType` |
| DRA representor and tc-flower work (upstream issues #121, #122) | Whether the hardware can run switchdev | `eswitch.capability` |
| DRA VF attributes (upstream #123) and sriov-cni | VF limits | `sriovTotalVFs` |
| t-co `SRIOVIntegration` gate | The `SRIOVEnabled` bootstrap input that taints SR-IOV worker groups. The input should come from this report (`sriovCapable` and `sriovTotalVFs` of the profile PFs), not from a manual flag. | `sriovCapable`, `sriovTotalVFs` |
| CAPRF and bm4x | Host status and the host class | the whole `sriov` object |

The t-co draft MRs name the taint `sriov.t-caas.telekom.com/enabled`
(`api/v1alpha1/taints.go` on `feat/sriov-taint-const`); the proposal and
older notes name it `t-caas.telekom.com/sriov`. BOOTy sets no taint, so only
the t-co side has to settle the key.

### Ownership of firmware settings

| Setting | Owner |
|---------|-------|
| ConnectX `NUM_OF_VFS`, `SRIOV_EN` | BOOTy: the SR-IOV profiles when they set them, else `setup-mellanox` (`NUM_VFS`). |
| Other NV parameters, permanent devlink parameters, Intel firmware LLDP | BOOTy when a profile sets them. |
| ConnectX `FLEX_PARSER_PROFILE_ENABLE`, `CQE_COMPRESSION`, `NUM_OF_VFS` from the OS | The T-CaaS compute profile (`mellanox_cfg.service`, inputs `FlexParserProfileEnable`, `CqeCompression`, `NumOfVFs`) writes them with `mstconfig` on every OS boot. They take effect only after the next reboot. |
| Runtime state | SR-IOV network operator or DRA driver. |

The compute profile writes `NUM_OF_VFS` too. When both set it to different
values, each provision and each OS boot changes it back, and every change
needs a reboot to take effect. Set it in one place only.
