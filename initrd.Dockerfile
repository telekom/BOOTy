# syntax=docker/dockerfile:experimental

# Build LVM2 as an init
FROM gcc:14 AS lvm
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
FROM gcc:14 AS sfdisk
RUN apt-get update -y && apt-get install -y bison autopoint gettext flex
RUN git clone https://github.com/karelzak/util-linux.git
WORKDIR util-linux
RUN ./autogen.sh && ./configure --enable-static-programs=sfdisk && make

# Build BOOTy as an init
FROM golang:1.26-alpine AS dev
RUN apk add --no-cache git ca-certificates gcc linux-headers musl-dev
COPY . /go/src/github.com/telekom/BOOTy/
WORKDIR /go/src/github.com/telekom/BOOTy
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

# Build disk and system tools
FROM debian:bookworm-slim AS tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    mdadm util-linux e2fsprogs xfsprogs parted gdisk kpartx dosfstools \
    efibootmgr dmidecode ethtool curl iproute2 bridge-utils \
    && rm -rf /var/lib/apt/lists/*

# Build Busybox
FROM gcc:14 AS busybox
RUN apt-get update && apt-get install -y cpio
RUN curl -O https://busybox.net/downloads/busybox-1.37.0.tar.bz2
RUN tar -xf busybox*bz2
WORKDIR busybox-1.37.0
RUN make defconfig \
    && sed -i 's/CONFIG_TC=y/# CONFIG_TC is not set/' .config \
    && make LDFLAGS=-static CONFIG_PREFIX=./initramfs install

WORKDIR initramfs
RUN curl -fsSL https://github.com/canonical/cloud-utils/archive/refs/tags/0.33.tar.gz | tar -xz -C /tmp && mv /tmp/cloud-utils-0.33/bin/growpart ./bin

# Copy build contents from previous builds
COPY --from=lvm /LVM2.2.03.27/tools/lvm sbin
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
COPY --from=tools /usr/sbin/parted bin/parted
COPY --from=tools /usr/sbin/sgdisk bin/sgdisk
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr
COPY --from=tools /usr/sbin/dmidecode bin/dmidecode
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /usr/bin/curl bin/curl
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /sbin/bridge bin/bridge

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

# Fetch a kernel — use the Debian cloud kernel (lightweight, no initramfs deps)
RUN apt-get update && \
    apt-get install -y --no-install-recommends linux-image-cloud-amd64 && \
    cp /boot/vmlinuz-* /iso/boot/vmlinuz && \
    rm -rf /var/lib/apt/lists/*

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

# Copy ONLY busybox binary + symlinks (not FRR/LVM binaries from busybox stage)
COPY --from=busybox /busybox-1.37.0/initramfs/bin/busybox bin/busybox
RUN for cmd in sh mount umount insmod ash ls cat echo grep mkdir rm cp mv \
      sleep date df du find head wc sort uniq tr sed awk ping wget ifconfig \
      route telnet vi chmod chown ln test expr; do \
      ln -sf busybox bin/$cmd; \
    done
COPY --from=busybox /busybox-1.37.0/initramfs/bin/growpart bin/growpart

# BOOTy init binary (static, CGO-enabled)
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .

# Minimal networking tools (DHCP mode — no FRR, no BGP)
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /usr/bin/curl bin/curl

# Basic disk tools (filesystem check + resize only)
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /sbin/e2fsck sbin/e2fsck
COPY --from=tools /sbin/resize2fs sbin/resize2fs

# Package slim initramfs
RUN find . -print0 | cpio --null -ov --format=newc > ../initramfs.cpio \
    && gzip ../initramfs.cpio && mv ../initramfs.cpio.gz /

FROM scratch AS slim
COPY --from=slim-builder /initramfs.cpio.gz .

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
