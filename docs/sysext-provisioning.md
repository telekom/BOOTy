# systemd-sysext provisioning

BOOTy can optionally copy systemd-sysext images into the provisioned OS while
the target root filesystem is mounted during provisioning.

This is disabled by default. When enabled, BOOTy runs the `apply-sysexts` step
after `mount-root`/`mount-boot` and before fstab, chroot bind mounts,
cloud-init, bootloader, and post-provision commands.

## Preload mode

`preload` is the default and recommended mode for immutable base images. BOOTy
copies selected `.raw` sysext images into an inactive catalog:

```text
/usr/lib/tcaas-sysext/preloaded/
/usr/lib/tcaas-sysext/preloaded/catalog.json
```

It does not place files in `/etc/extensions`, `/run/extensions`, or
`/var/lib/extensions`, and it does not enable `systemd-sysext.service`.
Boot-time config can later select layers from the catalog.

Example:

```yaml
provision:
  sysext:
    enabled: true
    defaultMode: preload
    layers:
      - name: node-tuning
        version: v1
        source: /deploy/sysext/node-tuning.raw
        fileName: node-tuning.raw
        sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      - name: cra-vsr
        version: v3.11.0
        source: /deploy/sysext/cra-vsr.raw
        fileName: cra-vsr.raw
        sha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
```

## Active mode

Use `active` only when the provisioned OS should boot with the sysext already in
a systemd-sysext search path. BOOTy copies the layer into `/var/lib/extensions`
by default. The OS image or boot config must still ensure `systemd-sysext` is
enabled and ordered correctly.

```yaml
provision:
  sysext:
    enabled: true
    defaultMode: active
    layers:
      - name: debug-tools
        source: /deploy/sysext/debug-tools.raw
        fileName: debug-tools.raw
        sha256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
```

Each layer can override the default:

```yaml
provision:
  sysext:
    enabled: true
    defaultMode: preload
    layers:
      - name: node-tuning
        source: /deploy/sysext/node-tuning.raw
      - name: debug-tools
        source: /deploy/sysext/debug-tools.raw
        mode: active
```

## Sources

`source` may be a local path in the initramfs, such as a file injected by CAPRF
under `/deploy/sysext`, an HTTPS URL, or an OCI registry reference using the
`oci://` scheme. Plain HTTP sysext sources require
`allowInsecureHTTP: true` or `SYSEXT_ALLOW_INSECURE_HTTP=true` and should be
limited to controlled provisioning networks and tests. OCI sources pull the
first layer with media type `application/vnd.systemd.sysext.image.v1+raw` from
the referenced image using BOOTy's normal registry keychain support. BOOTy
rejects ordinary container-image layers for sysext sources so multi-layer
artifacts cannot accidentally preload the wrong blob. For OCI sources, set
`fileName` explicitly when the active/catalog filename must be stable; otherwise
BOOTy uses `<name>.raw`.

```yaml
provision:
  sysext:
    enabled: true
    layers:
      - name: node-tuning
        version: v1
        source: oci://registry.example.com/tcaas/sysext-node-tuning:v1
        fileName: node-tuning.raw
        sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
```

Local provisioner files are preferred for reproducibility and to avoid a second
network dependency after the OS image has already streamed.

BOOTy streams each sysext through SHA256 while copying. Every enabled layer must
set `sha256`, unless the source is pinned as an OCI digest reference such as
`oci://registry.example/tcaas/node-tuning@sha256:<digest>`. A mismatch aborts
provisioning and removes the temporary target file.

## CAPRF vars

When BOOTy is launched through CAPRF, the same config can be delivered through
`/deploy/vars`:

```sh
export SYSEXT_ENABLED="true"
export SYSEXT_DEFAULT_MODE="preload"
export SYSEXT_CATALOG_DIR="/usr/lib/tcaas-sysext/preloaded"
export SYSEXT_LAYERS='[{"name":"node-tuning","version":"v1","source":"https://registry.example/sysext/node-tuning.raw","fileName":"node-tuning.raw","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","mode":"preload"}]'
```

`SYSEXT_LAYERS` is a JSON array using the same layer fields as the YAML
configuration. `SYSEXT_ACTIVE_DIR` is also supported for `active` mode. Use
`SYSEXT_ALLOW_INSECURE_HTTP=true` only for explicit plain-HTTP provisioning
networks.

## Best practices

- Prefer `defaultMode: preload`; activate layers through boot config or a
  higher-level controller only when needed.
- For A/B OS updates, keep sysexts independently versioned. BOOTy can preload
  selected sysexts into the target root slot while `IMAGE_MODE=ab` writes the
  inactive OS slot.
- Pin every layer with `name`, `version`, `fileName`, and `sha256` or an OCI
  digest reference.
- Prefer HTTPS, OCI, or local provisioner files. Plain HTTP requires explicit
  opt-in and is not a production default.
- Keep kernel, firmware, bootloader, users, `/etc`, `/var`, and provider state
  in the base image or regular provisioning config, not in sysext.
- Use BOOTy sysext provisioning for site-specific late binding, such as
  `node-tuning`, debugging tools, or optional VSR/CRA routing layers.
