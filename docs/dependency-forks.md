# Dependency forks

BOOTy fixes bugs in its Go dependencies in forks under
[github.com/MaxRink](https://github.com/MaxRink). A fork is a branch
`booty/<version line>` from the upstream version that BOOTy used, with the
fixes on top, tagged `<upstream version>-booty.N`. The fork keeps the
upstream module path in its `go.mod`; BOOTy selects it with a `replace`
directive:

| Module | Replace in `go.mod` | Fork branch |
|---|---|---|
| `github.com/osrg/gobgp/v4` | `github.com/MaxRink/gobgp/v4 v4.10.0-booty.10` | `booty/v4.10`, see [GoBGP version and fork](networking.md#gobgp-version-and-fork) |
| `github.com/insomniacslk/dhcp` | `github.com/MaxRink/dhcp v0.0.0-20260901064844-234b97448fae-booty.1` | [`booty/master`](https://github.com/MaxRink/dhcp/tree/booty/master) |

Dependabot ignores the forked modules (`.github/dependabot.yml`): an
upstream bump does not change the build while the `replace` is in place.

## insomniacslk/dhcp

The DHCPv4 and DHCPv6 clients encode and decode their messages with
`github.com/insomniacslk/dhcp` ([message encoding](networking.md#message-encoding)).
The module has no releases; the fork starts at master 234b97448fae
(2026-09-01), so its version line is `master` and its tags carry the
pseudo-version of that commit.

| Fix | Effect without it | Upstream (master c6bb46ad3f3a, 2026-10-14) |
|---|---|---|
| `rfc1035label` follows a compression pointer to a name that ends with a pointer itself (RFC 1035 section 4.1.4) | A DHCPv4 search list (option 119) from a server that compresses names like `c.b.example`, `b.example`, `example` is rejected as a whole: no search domains | Present; not reported yet |
| `rfc1035label` rejects a pointer that does not point before its name and before the previous pointer of the name (the jump barrier of systemd-networkd), a pointer past the end, the reserved label types 01 and 10, and names over 255 octets | Forward pointers, pointers past the end (read as a partial name) and label lengths 64 to 191 are accepted, unlike in networkd | Present; not reported yet |
| `OptElapsedTime` sends 0xffff for 655.35 s and more (RFC 8415 section 21.9) | The elapsed time wraps around: 700 s goes out as 44.64 s. BOOTy does not depend on it: the client starts a new transaction long before | Present; not reported yet |

## When to drop a fork

Drop a `replace` when an upstream release contains all fixes of its
fork, then run the unit tests. `TestLeaseSearchDomains`,
`TestMalformedRouteAndSearchOptions` and `FuzzDomainSearch` (in
`pkg/network`) fail without the `rfc1035label` fixes; `TestGolden` in
`pkg/network/dhcp6` covers the elapsed time.
