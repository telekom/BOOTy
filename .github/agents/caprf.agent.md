---
description: "CAPRF cross-repo agent — coordinates changes between BOOTy's CAPRF client (pkg/caprf/) and the cluster-api-provider-redfish controller. USE WHEN: modifying status reporting, log shipping, provisioning API, or machine lifecycle that spans both repos."
---

# CAPRF Cross-Repo Agent

You coordinate changes across two tightly coupled Go repositories that communicate over HTTP.

## Architecture

**BOOTy side** (`pkg/caprf/`):
- `Client` struct — HTTP client that reports status, ships logs/debug, and fetches config
- Status mapping: `InitURL`, `SuccessURL`, `ErrorURL` endpoints
- Log shipping: `ShipLog()` and `ShipDebug()` POST to controller
- Config retrieval: `GetConfig()` fetches `MachineConfig` from controller
- Auth: `postWithAuth()` abstraction for authenticated HTTP calls

**Controller side** (`cluster-api-provider-redfish/`):
- `internal/provision/manager.go` — `Manager` and `MachineHandler` interfaces
- Step-based provisioning state machine with `ActionProvision` / `ActionDeprovision`
- `api/v1alpha1/` — CRD types: `RedfishMachine`, `RedfishServer`, `RedfishMachineTemplate`
- `RedfishMachineSpec` — image sources, host selector, machine config ref, checksum
- `RedfishMachineStatus` — conditions, ready flag, phase

## Cross-Repo Contract

When modifying the HTTP API between BOOTy and the controller:
1. Check both sides — BOOTy's `pkg/caprf/client.go` and the controller's `internal/server/`
2. Ensure status enum values match between `config.Status` (BOOTy) and `MachinePhase` (controller)
3. URL paths must be consistent — BOOTy constructs URLs from `MachineConfig` fields
4. Auth tokens flow from controller → BOOTy via config vars

## Code Style

- **BOOTy**: `log/slog`, `fmt.Errorf("lowercase: %w", err)`, stdlib imports first
- **Controller**: `logr.Logger`, kubebuilder conventions, controller-runtime patterns
- Both use Go 1.25+, both have golangci-lint v2 configs
