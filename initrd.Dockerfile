# syntax=docker/dockerfile:experimental

# Build LVM2 as an init
FROM gcc:15 AS lvm
RUN wget https://mirrors.kernel.org/sourceware/lvm2/LVM2.2.03.27.tgz
RUN tar -xf LVM2.2.03.27.tgz
WORKDIR LVM2.2.03.27
RUN apt-get update && apt-get install -y libaio-dev libdevmapper-dev
RUN ./configure --enable-static_link --disable-selinux
RUN sed -i '/DMLIBS = -ldevmapper/ s/$/ -lm -lpthread/' libdm/dm-tools/Makefile
RUN make; exit 0
WORKDIR tools
RUN gcc -O2 -fPIC -static -L command.o dumpconfig.o formats.o lvchange.o lvconvert.o lvconvert_poll.o lvcreate.o lvdisplay.o lvextend.o lvmcmdline.o lvmdiskscan.o lvpoll.o lvreduce.o lvremove.o lvrename.o lvresize.o lvscan.o polldaemon.o pvchange.o pvck.o pvcreate.o pvdisplay.o pvmove.o pvmove_poll.o pvremove.o pvresize.o pvscan.o reporter.o segtypes.o tags.o toollib.o vgcfgbackup.o vgcfgrestore.o vgchange.o vgck.o vgcreate.o vgdisplay.o vgexport.o vgextend.o vgimport.o vgimportclone.o vgmerge.o vgmknodes.o vgreduce.o vgremove.o vgrename.o vgscan.o vgsplit.o lvm-static.o ../lib/liblvm-internal.a ../libdaemon/client/libdaemonclient.a ../device_mapper/libdevice-mapper.a ../base/libbase.a -lm -lblkid -laio -o lvm -lpthread -luuid ./liblvm2cmd.a

# Build scripted fdisk (sfdisk)
FROM gcc:15 AS sfdisk
RUN apt-get update -y && apt-get install -y bison autopoint gettext flex
RUN git clone https://github.com/karelzak/util-linux.git
WORKDIR util-linux
RUN ./autogen.sh && ./configure --enable-static-programs=sfdisk && make

# Build BOOTy as an init
FROM golang:1.26-alpine AS dev
RUN apk add --no-cache git ca-certificates gcc linux-headers musl-dev
COPY go.mod go.sum /go/src/github.com/telekom/BOOTy/
WORKDIR /go/src/github.com/telekom/BOOTy
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    go mod download
COPY . /go/src/github.com/telekom/BOOTy/
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    --mount=type=cache,sharing=locked,id=goroot,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -a -ldflags "-linkmode external -extldflags '-static' -s -w" -o init

# Build FRR (BGP/BFD/Zebra) for EVPN networking — use FRR official stable repo
FROM debian:bookworm-slim AS frr
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl gnupg lsb-release ca-certificates && \
    curl -s https://deb.frrouting.org/frr/keys.gpg \
      | tee /usr/share/keyrings/frrouting.gpg > /dev/null && \
    echo "deb [signed-by=/usr/share/keyrings/frrouting.gpg] https://deb.frrouting.org/frr $(lsb_release -s -c) frr-stable" \
      > /etc/apt/sources.list.d/frr.list && \
    apt-get update && apt-get install -y --no-install-recommends \
    frr frr-pythontools && \
    rm -rf /var/lib/apt/lists/*

# Collect shared libraries for FRR binaries (same rationale as tools stage).
RUN mkdir -p /frr-libs && \
    ldd /usr/lib/frr/bgpd /usr/lib/frr/zebra /usr/lib/frr/bfdd \
        /usr/bin/vtysh /usr/lib/frr/watchfrr 2>/dev/null \
    | awk '{for (i=1;i<=NF;i++) if ($i ~ /^\//) print $i}' \
    | sort -u | while read -r lib; do \
        [ -n "$lib" ] && [ -f "$lib" ] && cp -L --parents "$lib" /frr-libs/ 2>/dev/null || true; \
    done

# Extract kernel, storage, and NIC driver modules for bare-metal servers
FROM debian:bookworm-slim AS kernel
ARG TARGETARCH
RUN apt-get update && \
    apt-get install -y --no-install-recommends kmod && \
    KERNEL_PKG=$([ "$TARGETARCH" = "arm64" ] && echo "linux-image-arm64" || echo "linux-image-amd64") && \
    REAL_PKG=$(apt-cache depends "$KERNEL_PKG" | awk '/Depends:/{print $2}' | head -1) && \
    apt-get download "$REAL_PKG" && \
    dpkg-deb -x linux-image-*.deb /tmp/kernel && \
    cp /tmp/kernel/boot/vmlinuz-* /vmlinuz && \
    KVER=$(ls /tmp/kernel/lib/modules/ | head -1) && \
    MDIR="/tmp/kernel/lib/modules/$KVER" && \
    mkdir -p /modules && \
    # Generate modules.dep for modprobe dependency resolution
    depmod -b /tmp/kernel "$KVER" && \
    # Target modules — transitive dependencies resolved via modprobe --show-depends.
    # Module categories:
    #   QEMU/KVM virtio:  virtio virtio_ring virtio_pci_modern_dev virtio_pci_legacy_dev virtio_pci virtio_net failover net_failover
    #   Storage (SCSI):   scsi_mod sd_mod virtio_blk virtio_scsi
    #   Filesystems:      ext4 jbd2 mbcache crc32c_generic xfs btrfs vfat
    #   VXLAN/bridge:     dummy vxlan udp_tunnel ip6_udp_tunnel bridge stp llc
    #   Intel NICs:       e1000e igb igc ixgbe i40e ice iavf
    #   Broadcom NICs:    tg3 bnxt_en
    #   Mellanox/NVIDIA:  mlx4_core mlx4_en mlx5_core mlxfw
    #   Emulex/Broadcom:  be2net
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
        # Copy module + all transitive dependencies via modprobe
        modprobe --show-depends -d /tmp/kernel -S "$KVER" "$m" 2>/dev/null \
            | awk '/^insmod /{print $2}' \
            | while read -r dep; do \
                [ -f "$dep" ] && cp -n "$dep" /modules/ 2>/dev/null || true; \
            done; \
        # Fallback: direct find if modprobe doesn't resolve (builtin or alias)
        find "$MDIR" -name "${m}.ko*" -exec cp -n {} /modules/ \; 2>/dev/null || true; \
    done && \
    echo "Extracted $(ls /modules/*.ko* 2>/dev/null | wc -l) kernel modules" && \
    rm -rf /tmp/kernel *.deb /var/lib/apt/lists/*

# Build disk, system, and firmware tools
FROM debian:bookworm-slim AS tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    mdadm util-linux e2fsprogs xfsprogs btrfs-progs parted gdisk kpartx dosfstools \
    efibootmgr dmidecode ethtool curl iproute2 bridge-utils \
    hdparm nvme-cli mstflint lldpd \
    dropbear-bin \
    && rm -rf /var/lib/apt/lists/*

# Collect shared libraries for all tool binaries while their packages are
# installed.  Running ldd here (instead of in the final assembly stage)
# ensures every transitive dependency is resolved — the assembly stages
# only have base Debian libs and would report package-specific libs as
# "not found", silently dropping them from the initramfs.
RUN mkdir -p /tool-libs && \
    ldd /sbin/mdadm /usr/sbin/wipefs /sbin/resize2fs /sbin/e2fsck \
        /usr/sbin/xfs_growfs /usr/bin/btrfs /usr/sbin/parted /usr/sbin/sgdisk \
        /sbin/partprobe /usr/bin/efibootmgr /usr/sbin/dmidecode /usr/sbin/ethtool \
        /usr/bin/curl /sbin/ip /sbin/bridge /sbin/hdparm /usr/sbin/nvme \
        /usr/bin/mstconfig /usr/bin/mstflint /usr/sbin/lldpcli /usr/sbin/lldpd \
        /usr/sbin/dropbear /usr/bin/dropbearkey /bin/lsblk 2>/dev/null \
    | awk '{for (i=1;i<=NF;i++) if ($i ~ /^\//) print $i}' \
    | sort -u | while read -r lib; do \
        [ -n "$lib" ] && [ -f "$lib" ] && cp -L --parents "$lib" /tool-libs/ 2>/dev/null || true; \
    done

# Busybox static binary — sourced from Docker Hub for reliability and
# Dependabot version tracking.  Eliminates the fragile busybox.net download
# that caused CI failures when the site was unreachable.
FROM busybox:1.37.0-musl AS busybox-bin

FROM debian:bookworm-slim AS busybox
RUN apt-get update && apt-get install -y --no-install-recommends cpio curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build/initramfs

# Pre-built static busybox binary from Docker Hub (cached by Docker layer cache)
COPY --from=busybox-bin /bin/busybox bin/busybox

# Create standard applet symlinks into bin/ using BusyBox's built-in installer
RUN bin/busybox --install -s bin

# cloud-utils growpart
RUN curl -fsSL https://github.com/canonical/cloud-utils/archive/refs/tags/0.33.tar.gz | tar -xz -C /tmp \
    && mv /tmp/cloud-utils-0.33/bin/growpart bin/

# Copy build contents from previous builds
COPY --from=lvm /LVM2.2.03.27/tools/lvm sbin/
COPY --from=sfdisk /util-linux/sfdisk.static bin/sfdisk
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .

# FRR binaries for EVPN networking
COPY --from=frr /usr/lib/frr/bgpd sbin/bgpd
COPY --from=frr /usr/lib/frr/zebra sbin/zebra
COPY --from=frr /usr/lib/frr/bfdd sbin/bfdd
COPY --from=frr /usr/bin/vtysh bin/vtysh
COPY --from=frr /usr/lib/frr/watchfrr sbin/watchfrr

# Disk and system tools
COPY --from=tools /sbin/mdadm sbin/mdadm
COPY --from=tools /usr/sbin/wipefs bin/wipefs
COPY --from=tools /sbin/resize2fs sbin/resize2fs
COPY --from=tools /sbin/e2fsck sbin/e2fsck
COPY --from=tools /usr/sbin/xfs_growfs sbin/xfs_growfs
COPY --from=tools /usr/bin/btrfs bin/btrfs
COPY --from=tools /usr/sbin/parted bin/parted
COPY --from=tools /usr/sbin/sgdisk bin/sgdisk
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr
COPY --from=tools /usr/sbin/dmidecode bin/dmidecode
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /usr/bin/curl bin/curl
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /sbin/bridge bin/bridge

# Secure erase tools
COPY --from=tools /sbin/hdparm bin/hdparm
COPY --from=tools /usr/sbin/nvme bin/nvme

# Firmware tools (Mellanox ConnectX SR-IOV config)
COPY --from=tools /usr/bin/mstconfig bin/mstconfig
COPY --from=tools /usr/bin/mstflint bin/mstflint

# LLDP daemon for switch topology discovery
COPY --from=tools /usr/sbin/lldpcli bin/lldpcli
COPY --from=tools /usr/sbin/lldpd sbin/lldpd

# Dropbear lightweight SSH server for rescue mode (~110 KB)
COPY --from=tools /usr/sbin/dropbear bin/dropbear
COPY --from=tools /usr/bin/dropbearkey bin/dropbearkey

# Copy pre-collected shared libraries from stages where packages are installed.
# This replaces the previous ldd scan that ran in this stage — that approach
# missed package-specific libs (libefivar, libmnl, etc.) because they were
# not installed here and ldd reported them as "not found".
COPY --from=tools /tool-libs/ .
COPY --from=frr /frr-libs/ .

# Kernel modules for common server NICs (flat directory, loaded via insmod)
COPY --from=kernel /modules/ modules/

# Package initramfs
RUN find . -print0 | cpio --null -ov --format=newc > ../initramfs.cpio
RUN gzip ../initramfs.cpio
RUN mv ../initramfs.cpio.gz /

# ── ISO build stage (optional, triggered by --target=iso) ──────────────────
FROM debian:bookworm-slim AS iso-builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    xorriso syslinux syslinux-common isolinux curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=busybox /initramfs.cpio.gz /iso/boot/initrd.img

# Use the standard Debian kernel (has all common NIC drivers as modules)
COPY --from=kernel /vmlinuz /iso/boot/vmlinuz

# ISOLINUX bootloader
RUN mkdir -p /iso/isolinux && \
    cp /usr/lib/ISOLINUX/isolinux.bin /iso/isolinux/ && \
    cp /usr/lib/syslinux/modules/bios/ldlinux.c32 /iso/isolinux/

RUN printf 'DEFAULT booty\nLABEL booty\n  KERNEL /boot/vmlinuz\n  APPEND initrd=/boot/initrd.img console=tty0 console=ttyS0,115200n8\n' \
    > /iso/isolinux/isolinux.cfg

RUN xorriso -as mkisofs \
    -o /booty.iso \
    -b isolinux/isolinux.bin \
    -c isolinux/boot.cat \
    -no-emul-boot -boot-load-size 4 -boot-info-table \
    -isohybrid-mbr /usr/lib/ISOLINUX/isohdpfx.bin \
    /iso

FROM scratch AS iso
COPY --from=iso-builder /booty.iso .

# ── Slim target: BOOTy + busybox shell + minimal tools, no FRR/LVM ────────
FROM debian:bookworm-slim AS slim-builder
RUN apt-get update && apt-get install -y --no-install-recommends cpio \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build/initramfs

# Copy busybox binary and create all applet symlinks (same as default variant).
# This ensures utilities like tail, grep, dmesg, hostname, ps, and xargs are
# available for debug diagnostics and shell commands.
COPY --from=busybox-bin /bin/busybox bin/busybox
RUN for cmd in $(bin/busybox --list); do if [ "$cmd" != "busybox" ]; then ln -sf busybox "bin/$cmd"; fi; done
COPY --from=busybox /build/initramfs/bin/growpart bin/growpart

# BOOTy init binary (static, CGO-enabled)
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .

# Minimal networking tools (DHCP mode — no FRR, no BGP)
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /usr/bin/curl bin/curl

# Basic disk tools (filesystem check + resize only)
RUN mkdir -p sbin
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /sbin/e2fsck sbin/e2fsck
COPY --from=tools /sbin/resize2fs sbin/resize2fs

# Copy pre-collected shared libraries (slim was previously missing lib copying
# entirely, breaking all dynamically linked tool binaries at runtime).
COPY --from=tools /tool-libs/ .

# Package slim initramfs
RUN find . -print0 | cpio --null -ov --format=newc > ../initramfs.cpio \
    && gzip ../initramfs.cpio && mv ../initramfs.cpio.gz /

FROM scratch AS slim
COPY --from=slim-builder /initramfs.cpio.gz .

# ── GoBGP target: like default but without FRR (GoBGP is in-process Go) ───
FROM debian:bookworm-slim AS gobgp-builder
RUN apt-get update && apt-get install -y --no-install-recommends cpio \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build/initramfs

# Copy busybox binary and create all applet symlinks (same as default variant).
# This ensures utilities like tail, pgrep, dmesg, lsblk, hostname, ps, grep,
# kill, id, and xargs are available for debug diagnostics and shell commands.
COPY --from=busybox-bin /bin/busybox bin/busybox
RUN for cmd in $(bin/busybox --list); do if [ "$cmd" != "busybox" ]; then ln -sf busybox "bin/$cmd"; fi; done
COPY --from=busybox /build/initramfs/bin/growpart bin/growpart

# BOOTy init binary (with GoBGP compiled in)
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .

# LVM + sfdisk for disk management
RUN mkdir -p sbin
COPY --from=lvm /LVM2.2.03.27/tools/lvm sbin/lvm
COPY --from=sfdisk /util-linux/sfdisk.static bin/sfdisk

# Disk and system tools (same as default)
COPY --from=tools /sbin/mdadm sbin/mdadm
COPY --from=tools /usr/sbin/wipefs bin/wipefs
COPY --from=tools /sbin/resize2fs sbin/resize2fs
COPY --from=tools /sbin/e2fsck sbin/e2fsck
COPY --from=tools /usr/sbin/xfs_growfs sbin/xfs_growfs
COPY --from=tools /usr/bin/btrfs bin/btrfs
COPY --from=tools /usr/sbin/parted bin/parted
COPY --from=tools /usr/sbin/sgdisk bin/sgdisk
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr
COPY --from=tools /usr/sbin/dmidecode bin/dmidecode
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /usr/bin/curl bin/curl
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /sbin/bridge bin/bridge

# Secure erase tools
COPY --from=tools /sbin/hdparm bin/hdparm
COPY --from=tools /usr/sbin/nvme bin/nvme

# Firmware tools (Mellanox ConnectX SR-IOV config)
COPY --from=tools /usr/bin/mstconfig bin/mstconfig
COPY --from=tools /usr/bin/mstflint bin/mstflint

# LLDP daemon for switch topology discovery
COPY --from=tools /usr/sbin/lldpcli bin/lldpcli
COPY --from=tools /usr/sbin/lldpd sbin/lldpd

# Dropbear lightweight SSH server for rescue mode (~110 KB)
COPY --from=tools /usr/sbin/dropbear bin/dropbear
COPY --from=tools /usr/bin/dropbearkey bin/dropbearkey

# lsblk for rescue mode disk auto-mount
COPY --from=tools /bin/lsblk bin/lsblk

# Copy pre-collected shared libraries from the tools stage.
COPY --from=tools /tool-libs/ .

# Kernel modules for common server NICs (flat directory, loaded via insmod)
COPY --from=kernel /modules/ modules/

# Package GoBGP initramfs (no FRR binaries — GoBGP runs in-process)
RUN find . -print0 | cpio --null -ov --format=newc > ../initramfs.cpio \
    && gzip ../initramfs.cpio && mv ../initramfs.cpio.gz /

FROM scratch AS gobgp
COPY --from=gobgp-builder /initramfs.cpio.gz .

# ── GoBGP ISO target ──────────────────────────────────────────────────────
FROM debian:bookworm-slim AS gobgp-iso-builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    xorriso syslinux syslinux-common isolinux curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=gobgp-builder /initramfs.cpio.gz /iso/boot/initrd.img
COPY --from=kernel /vmlinuz /iso/boot/vmlinuz

RUN mkdir -p /iso/isolinux && \
    cp /usr/lib/ISOLINUX/isolinux.bin /iso/isolinux/ && \
    cp /usr/lib/syslinux/modules/bios/ldlinux.c32 /iso/isolinux/

RUN printf 'DEFAULT booty\nLABEL booty\n  KERNEL /boot/vmlinuz\n  APPEND initrd=/boot/initrd.img console=tty0 console=ttyS0,115200n8\n' \
    > /iso/isolinux/isolinux.cfg

RUN xorriso -as mkisofs \
    -o /booty-gobgp.iso \
    -b isolinux/isolinux.bin \
    -c isolinux/boot.cat \
    -no-emul-boot -boot-load-size 4 -boot-info-table \
    -isohybrid-mbr /usr/lib/ISOLINUX/isohdpfx.bin \
    /iso

FROM scratch AS gobgp-iso
COPY --from=gobgp-iso-builder /booty-gobgp.iso .

# ── Micro target: pure-Go BOOTy only, no external binaries ────────────────
FROM golang:1.26-alpine AS micro-dev
RUN apk add --no-cache git ca-certificates
COPY . /go/src/github.com/telekom/BOOTy/
WORKDIR /go/src/github.com/telekom/BOOTy
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    --mount=type=cache,sharing=locked,id=goroot,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -a -ldflags "-s -w" -o init

FROM debian:bookworm-slim AS micro-builder
RUN apt-get update && apt-get install -y --no-install-recommends cpio \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build/initramfs
RUN mkdir -p bin sbin dev proc sys tmp etc
COPY --from=micro-dev /go/src/github.com/telekom/BOOTy/init .
COPY --from=micro-dev /etc/ssl/certs/ca-certificates.crt etc/ssl/certs/

# Package micro initramfs
RUN find . -print0 | cpio --null -ov --format=newc > ../initramfs.cpio \
    && gzip ../initramfs.cpio && mv ../initramfs.cpio.gz /

FROM scratch AS micro
COPY --from=micro-builder /initramfs.cpio.gz .

# ── Default target: initramfs ──────────────────────────────────────────────
FROM scratch
COPY --from=busybox /initramfs.cpio.gz .
