# systemd-sysext provisioning

BOOTy can optionally copy systemd-sysext images into the provisioned OS while
the target root filesystem is mounted during provisioning.

This is disabled by default. When enabled, BOOTy runs the `apply-sysexts` step
after `mount-root` and before fstab, chroot bind mounts, cloud-init, bootloader,
and post-provision commands.

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
under `/deploy/sysext`, or an HTTP(S) URL. Local provisioner files are preferred
for reproducibility and to avoid a second network dependency after the OS image
has already streamed.

BOOTy streams each sysext through SHA256 while copying. If `sha256` is set, a
mismatch aborts provisioning and removes the temporary target file.

## Best practices

- Prefer `defaultMode: preload`; activate layers through boot config or a
  higher-level controller only when needed.
- Pin every layer with `name`, `version`, `fileName`, and `sha256`.
- Keep kernel, firmware, bootloader, users, `/etc`, `/var`, and provider state
  in the base image or regular provisioning config, not in sysext.
- Use BOOTy sysext provisioning for site-specific late binding, such as
  `node-tuning`, debugging tools, or optional VSR/CRA routing layers.
