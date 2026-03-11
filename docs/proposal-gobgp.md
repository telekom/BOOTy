# Proposal: Replace FRR with GoBGP

## Summary

Replace the FRR (Free Range Routing) dependency with [GoBGP](https://github.com/osrg/gobgp) — a pure-Go BGP implementation — for the EVPN underlay network setup.

## Motivation

BOOTy currently shells out to the system FRR daemon for BGP/EVPN configuration. This has several drawbacks:

1. **Dependency**: FRR must be installed in the initramfs, adding ~50MB and complex packaging
2. **Configuration**: FRR config is rendered to `/etc/frr/frr.conf` and managed via `vtysh` commands — error-prone shell interaction
3. **Observability**: FRR runs as a separate process; monitoring BGP state requires parsing `vtysh` output
4. **Startup time**: FRR daemon startup adds 3-5 seconds to the provisioning flow
5. **Cross-platform testing**: FRR is Linux-only, making unit tests require build tags

## Design

### GoBGP Integration

GoBGP provides a Go library (`github.com/osrg/gobgp/v3/pkg/server`) for embedding a BGP speaker directly in the application.

```go
import "github.com/osrg/gobgp/v3/pkg/server"

func (m *Manager) Setup(ctx context.Context, cfg *network.Config) error {
    s := server.NewBgpServer()
    go s.Serve()

    // Configure global BGP
    s.StartBgp(ctx, &api.StartBgpRequest{
        Global: &api.Global{
            Asn:        cfg.ASN,
            RouterId:   underlayIP,
            ListenPort: 179,
        },
    })

    // Add EVPN address family and peers
    // ...
}
```

### Benefits

- **No external dependencies**: GoBGP links as a Go library, no daemon process needed
- **Smaller initramfs**: Eliminates ~50MB FRR installation
- **Direct API**: No shell-out to `vtysh`; direct Go API for configuration and monitoring
- **Better testing**: Pure Go, no build tags needed for unit/integration tests
- **Faster startup**: In-process BGP speaker starts in milliseconds
- **Type safety**: Go structs instead of text templates for configuration

### EVPN Capabilities

GoBGP supports the EVPN features BOOTy needs:
- BGP unnumbered (interface peering)
- L2VPN EVPN address family
- VXLAN VNI advertisement
- Route targets and route distinguishers
- IPv4/IPv6 underlay

### Migration Path

1. **Phase 1**: Add GoBGP as an alternative `network.Mode` implementation alongside FRR
2. **Phase 2**: Run both in CI, compare behavior
3. **Phase 3**: Default to GoBGP, deprecate FRR
4. **Phase 4**: Remove FRR code and initramfs dependency

### Interface Compatibility

Both implementations satisfy the existing `network.Mode` interface:

```go
type Mode interface {
    Setup(ctx context.Context, cfg *Config) error
    WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error
    Teardown(ctx context.Context) error
}
```

## Risks

- **BGP unnumbered**: GoBGP's support for interface-based peering (RFC 5549) needs verification
- **EVPN maturity**: GoBGP's EVPN implementation is less battle-tested than FRR's in production networks
- **Binary size**: GoBGP adds ~15MB to the BOOTy binary (vs ~50MB saved from removing FRR from initramfs)
- **Debugging**: FRR's `vtysh` is a familiar debugging tool for network engineers

## Alternatives

- **Keep FRR**: Accept the dependency and packaging complexity
- **ExaBGP**: Python-based, even heavier dependency
- **Custom BGP**: Write minimal BGP speaker — high risk, low reward given GoBGP exists

## Next Steps

1. Prototype GoBGP integration with the BOOTy `network.Mode` interface
2. Verify BGP unnumbered and EVPN Type-3 route support
3. Benchmark startup time and memory usage vs FRR
4. Run integration tests with containerlab topology
