# Build BOOTy VM image for containerlab vrnetlab-style testing.
# Packages QEMU + kernel + BOOTy initramfs into a container that boots
# a real VM inside containerlab's EVPN fabric.
#
# Build:  docker build -t booty-vrnetlab:latest -f test/e2e/clab/vrnetlab/Dockerfile .

# ── Stage 1: Build BOOTy static binary ────────────────────────────────
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates gcc linux-headers musl-dev
COPY go.mod go.sum /src/
WORKDIR /src
RUN go mod download
COPY . /src
RUN CGO_ENABLED=1 GOOS=linux go build -a \
    -ldflags "-linkmode external -extldflags '-static' -s -w" \
    -o /booty

# ── Stage 2: Extract a bootable kernel ────────────────────────────────
FROM debian:bookworm-slim AS kernel
RUN apt-get update && \
    apt-get install -y --no-install-recommends kmod && \
    REAL_PKG=$(apt-cache depends linux-image-amd64 | awk '/Depends:/{print $2}' | head -1) && \
    apt-get download "$REAL_PKG" && \
    dpkg-deb -x linux-image-*.deb /tmp/kernel && \
    cp /tmp/kernel/boot/vmlinuz-* /vmlinuz && \
    KVER=$(ls /tmp/kernel/lib/modules/ | head -1) && \
    MDIR="/tmp/kernel/lib/modules/$KVER" && \
    mkdir -p /modules && \
    depmod -b /tmp/kernel "$KVER" && \
    for m in \
        virtio virtio_ring virtio_pci_modern_dev virtio_pci_legacy_dev \
        virtio_pci virtio_net failover net_failover \
        scsi_mod sd_mod virtio_blk virtio_scsi \
        ext4 jbd2 mbcache crc32c_generic xfs btrfs vfat \
        dummy vxlan udp_tunnel ip6_udp_tunnel bridge stp llc \
        e1000e igb igc ixgbe i40e ice iavf \
        tg3 bnxt_en \
        mlx4_core mlx4_en mlx5_core mlxfw \
        be2net; do \
        modprobe --show-depends -d /tmp/kernel -S "$KVER" "$m" 2>/dev/null \
            | awk '/^insmod /{print $2}' \
            | while read -r dep; do \
                [ -f "$dep" ] && cp -n "$dep" /modules/ 2>/dev/null || true; \
            done; \
        find "$MDIR" -name "${m}.ko*" -exec cp -n {} /modules/ \; 2>/dev/null || true; \
    done && \
    echo "Extracted $(ls /modules/*.ko* 2>/dev/null | wc -l) kernel modules" && \
    rm -rf /tmp/kernel *.deb /var/lib/apt/lists/*

# ── Stage 3: Assemble initramfs ───────────────────────────────────────
FROM debian:bookworm-slim AS initramfs
RUN apt-get update && apt-get install -y --no-install-recommends \
    cpio busybox-static curl gnupg lsb-release ca-certificates \
    e2fsprogs dosfstools gdisk parted lvm2 fdisk util-linux \
    && rm -rf /var/lib/apt/lists/*

# Install FRR for EVPN networking support inside the VM.
RUN curl -s https://deb.frrouting.org/frr/keys.gpg \
      | tee /usr/share/keyrings/frrouting.gpg > /dev/null && \
    echo "deb [signed-by=/usr/share/keyrings/frrouting.gpg] https://deb.frrouting.org/frr $(lsb_release -s -c) frr-stable" \
      > /etc/apt/sources.list.d/frr.list && \
    apt-get update && apt-get install -y --no-install-recommends frr && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /initramfs
RUN mkdir -p bin sbin dev proc sys etc tmp deploy lib/modules mnt \
    usr/lib/frr usr/bin etc/frr etc/pam.d var/run/frr lib && \
    echo 'root:x:0:0:root:/root:/bin/sh' > etc/passwd && \
    echo 'frr:x:101:104::/var/run/frr:/bin/sh' >> etc/passwd && \
    echo 'root:x:0:' > etc/group && \
    echo 'frr:x:104:' >> etc/group && \
    echo 'frrvty:x:103:frr' >> etc/group && \
    echo 'passwd: files' > etc/nsswitch.conf && \
    echo 'group: files' >> etc/nsswitch.conf && \
    chown -R 101:104 etc/frr var/run/frr && \
    printf 'auth sufficient pam_permit.so\naccount sufficient pam_permit.so\n' > etc/pam.d/frr && \
    touch etc/frr/vtysh.conf

# Busybox: static binary + essential symlinks
RUN cp /bin/busybox bin/busybox && \
    cd bin && \
    for cmd in sh mount umount insmod find ip ping cat echo ls mkdir rm \
               chmod sleep ln test grep awk sed head tail stty chroot; do \
        ln -sf busybox "$cmd"; \
    done

# BOOTy binary (exec'd by init wrapper)
COPY --from=builder /booty /initramfs/booty

# Kernel modules for networking (flat directory, loaded via insmod)
COPY --from=kernel /modules/ /initramfs/modules/

# FRR daemons and dependencies for EVPN networking.
# mgmtd is required in FRR 10.5+ for proper integrated config loading.
# Also include disk provisioning tools (sgdisk, mkfs, parted, lvm).
# Copy binaries and resolve all shared library dependencies.
RUN mkdir -p /initramfs/lib64 /initramfs/lib/x86_64-linux-gnu && \
    for bin in /usr/lib/frr/mgmtd /usr/lib/frr/zebra /usr/lib/frr/bgpd /usr/lib/frr/bfdd /usr/bin/vtysh \
               /usr/sbin/sgdisk /usr/sbin/mkfs.ext4 /usr/sbin/mkfs.vfat /usr/sbin/parted \
               /usr/sbin/pvcreate /usr/sbin/vgcreate /usr/sbin/lvcreate \
               /usr/sbin/wipefs /usr/sbin/sfdisk /usr/bin/lsblk /usr/sbin/blkid; do \
        dest="/initramfs$(dirname $bin)"; \
        mkdir -p "$dest"; \
        cp "$bin" "/initramfs${bin}"; \
        ldd "$bin" 2>/dev/null | grep -o '/[^ ]*' | while read -r lib; do \
            mkdir -p "/initramfs$(dirname $lib)"; \
            cp -n "$lib" "/initramfs${lib}" 2>/dev/null || true; \
        done; \
    done && \
    cp /lib64/ld-linux-x86-64.so.2 /initramfs/lib64/ 2>/dev/null || true && \
    cp /lib/x86_64-linux-gnu/libnss_files.so.2 /initramfs/lib/x86_64-linux-gnu/ 2>/dev/null || true && \
    mkdir -p /initramfs/lib/x86_64-linux-gnu/security && \
    cp /lib/x86_64-linux-gnu/security/pam_permit.so /initramfs/lib/x86_64-linux-gnu/security/ 2>/dev/null || true

# Init wrapper (PID 1 — sets up network then execs BOOTy)
COPY test/e2e/clab/vrnetlab/init-wrapper.sh /initramfs/init
RUN chmod +x /initramfs/init

# ── Stage 4: Runtime container with QEMU ──────────────────────────────
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-system-x86 qemu-utils iproute2 bridge-utils cpio gzip procps \
    && rm -rf /var/lib/apt/lists/*

COPY --from=kernel /vmlinuz /opt/vmlinuz
COPY --from=initramfs /initramfs /opt/initramfs

COPY test/e2e/clab/vrnetlab/boot.sh /opt/boot.sh
RUN chmod +x /opt/boot.sh

ENTRYPOINT ["/opt/boot.sh"]
