# BOOTy Feature Roadmap

Feature proposals for extending BOOTy and CAPRF beyond current capabilities.
Each proposal follows a standard format: Status, Priority, Summary, Motivation,
Design, Affected Files, Risks, and Effort Estimate.

## Priority Legend

| Priority | Meaning |
|----------|---------|
| **P0** | Critical — blocks production use or security |
| **P1** | High — significant operational value |
| **P2** | Medium — improves reliability or usability |
| **P3** | Nice to have — advanced capabilities |
| **P4** | Future — exploratory or long-term |

## Proposals by Priority

### P0 — Critical

| Proposal | Summary | Est. Effort |
|----------|---------|-------------|
| [SecureBoot Lifecycle](proposal-secureboot.md) | Re-enable SecureBoot post-provisioning + MOK enrollment | 2-10 days |
| [Vendor Integrations (HPE)](proposal-vendor-integrations.md) | Re-enable HPE iLO support (disabled code exists) | 1 day |

### P1 — High

| Proposal | Summary | Est. Effort |
|----------|---------|-------------|
| [Hardware Inventory](proposal-hardware-inventory.md) | Collect CPU, memory, disk, NIC specs from sysfs | 9-12 days |
| [Health Pre-Checks](proposal-health-checks.md) | Validate hardware before provisioning | 8-10 days |
| [VLAN Support](proposal-vlan-support.md) | 802.1Q VLAN tagging for provisioning traffic | 6-8 days |
| [Observability (Debug Dump)](proposal-observability.md) | Comprehensive debug state capture on failure | 3 days |

### P2 — Medium

| Proposal | Summary | Est. Effort |
|----------|---------|-------------|
| [Vendor Integrations (Lenovo)](proposal-vendor-integrations.md) | Lenovo XCC support + vendor quirks | 3-5 days |
| [BIOS Settings](proposal-bios-settings.md) | Declarative BIOS config via Redfish | 8-10 days |
| [Firmware Reporting](proposal-firmware-reporting.md) | Collect and validate firmware versions | 8-10 days |
| [Rescue Mode](proposal-rescue-mode.md) | Interactive debug shell with SSH | 5-7 days |
| [Network Persistence](proposal-network-persistence.md) | Write network config into provisioned OS | 9-12 days |
| [Observability (Metrics)](proposal-observability.md) | Prometheus metrics + event stream | 7-10 days |

### P3 — Nice to Have

| Proposal | Summary | Est. Effort |
|----------|---------|-------------|
| [Image Signatures](proposal-image-signatures.md) | Checksum + GPG + Cosign verification | 10-14 days |
| [Custom Disk Partitioning](proposal-disk-partitioning.md) | Declarative partition layouts + LVM | 14-17 days |
| [Dry-Run Mode](proposal-dry-run.md) | Simulate provisioning without writes | 9-12 days |
| [Webhooks](proposal-webhooks.md) | Push notifications to Slack, PagerDuty, etc. | 11-14 days |

### P4 — Future

| Proposal | Summary | Est. Effort |
|----------|---------|-------------|
| [TPM Attestation](proposal-tpm-attestation.md) | Measured boot + remote attestation | 17-22 days |
| [ARM64 Support](proposal-arm64-support.md) | Multi-architecture builds and provisioning | 10-14 days |
| [NVMe Namespaces](proposal-nvme-namespaces.md) | NVMe namespace CRUD for advanced storage | 8-11 days |

## Existing Proposals (Implemented or In Progress)

| Proposal | Status |
|----------|--------|
| [Agent / Standby Mode](proposal-agent-mode.md) | Implemented |
| [GoBGP Network Stack](proposal-gobgp.md) | Proposal |
| [CAPRF Integration](caprf-integration-plan.md) | Implemented |

## Total Estimated Effort

~130-180 engineering days across all proposals.
Recommended implementation order follows the priority table above.
