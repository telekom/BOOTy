# syntax=docker/dockerfile:1.27.1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# Dependabot reads only FROM lines, not this syntax directive
# (dependabot/dependabot-core#4078): update the frontend pin by hand. The build
# needs only RUN --mount=type=cache, stable since dockerfile:1.2.

# Build LVM2 as an init
FROM gcc:16@sha256:9af8c7003ad451f09496c7182f320c58554e10ec080e7038e0aa5e41146c916b AS lvm
ARG LVM2_VERSION=2.03.27
ARG LVM2_SHA256=3133415905b9b46d152d064865d52f32eee4fcbeb0e8a69e3510caeaae0c56a9
# One mirror that is down or fails TLS must not fail the whole build: try
# each mirror in turn, and accept a download only if it matches the pinned
# checksum, so any mirror is safe.
RUN set -eu; \
    f="LVM2.${LVM2_VERSION}.tgz"; \
    for url in \
      "https://mirrors.kernel.org/sourceware/lvm2/${f}" \
      "https://sourceware.org/pub/lvm2/${f}" \
      "https://sourceware.org/pub/lvm2/releases/${f}" \
      "https://ftp.gwdg.de/pub/linux/sources.redhat.com/lvm2/${f}"; do \
      if wget --tries=3 --waitretry=5 --retry-connrefused --timeout=30 \
           --retry-on-http-error=429,500,502,503,504 -O "${f}" "${url}" \
         && echo "${LVM2_SHA256}  ${f}" | sha256sum -c -; then \
        exit 0; \
      fi; \
      echo "LVM2 download from ${url} failed, trying the next mirror" >&2; \
      rm -f "${f}"; \
    done; \
    echo "LVM2 ${LVM2_VERSION}: no mirror delivered a file with the pinned checksum" >&2; \
    exit 1
RUN tar -xf "LVM2.${LVM2_VERSION}.tgz"
WORKDIR LVM2.${LVM2_VERSION}
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y libaio-dev libdevmapper-dev
RUN ./configure --enable-static_link --disable-selinux
RUN sed -i '/DMLIBS = -ldevmapper/ s/$/ -lm -lpthread/' libdm/dm-tools/Makefile
RUN make
WORKDIR tools
RUN gcc -O2 -fPIC -static -L command.o dumpconfig.o formats.o lvchange.o lvconvert.o lvconvert_poll.o lvcreate.o lvdisplay.o lvextend.o lvmcmdline.o lvmdiskscan.o lvpoll.o lvreduce.o lvremove.o lvrename.o lvresize.o lvscan.o polldaemon.o pvchange.o pvck.o pvcreate.o pvdisplay.o pvmove.o pvmove_poll.o pvremove.o pvresize.o pvscan.o reporter.o segtypes.o tags.o toollib.o vgcfgbackup.o vgcfgrestore.o vgchange.o vgck.o vgcreate.o vgdisplay.o vgexport.o vgextend.o vgimport.o vgimportclone.o vgmerge.o vgmknodes.o vgreduce.o vgremove.o vgrename.o vgscan.o vgsplit.o lvm-static.o ../lib/liblvm-internal.a ../libdaemon/client/libdaemonclient.a ../device_mapper/libdevice-mapper.a ../base/libbase.a -lm -lblkid -laio -o lvm -lpthread -luuid ./liblvm2cmd.a
RUN strip --strip-all lvm
# The ramdisk SBOM lists this source build as a component (--target
# sbom-inputs); it matches the file by its SHA-256.
RUN printf '{"name":"lvm2","version":"%s","purl":"pkg:generic/lvm2@%s?checksum=sha256:%s&download_url=https://sourceware.org/pub/lvm2/LVM2.%s.tgz","sha256":"%s"}\n' \
      "${LVM2_VERSION}" "${LVM2_VERSION}" "${LVM2_SHA256}" "${LVM2_VERSION}" "$(sha256sum lvm | cut -d' ' -f1)" > /sbom-source.json

# Build scripted fdisk (sfdisk)
FROM gcc:16@sha256:9af8c7003ad451f09496c7182f320c58554e10ec080e7038e0aa5e41146c916b AS sfdisk
ARG UTIL_LINUX_REF=5305e6c70b274f679329b79c0e1ef5a07e9dc1a6
RUN apt-get -o Acquire::Retries=5 update -y && apt-get -o Acquire::Retries=5 install -y bison autopoint gettext flex
RUN set -eu; \
    n=0; \
    until git clone --depth 1 --filter=blob:none --no-checkout \
      https://github.com/util-linux/util-linux.git; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "git clone failed after $n attempts" >&2; exit 1; fi; \
      rm -rf util-linux; \
      sleep 10; \
    done
WORKDIR util-linux
RUN set -eu; \
    n=0; \
    until git fetch --depth 1 origin "${UTIL_LINUX_REF}"; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "git fetch failed after $n attempts" >&2; exit 1; fi; \
      sleep 10; \
    done; \
    git checkout --detach "${UTIL_LINUX_REF}"
RUN ./autogen.sh && ./configure --enable-static-programs=sfdisk && make
RUN strip --strip-all sfdisk.static
# The ramdisk SBOM lists this source build as a component (--target
# sbom-inputs); it matches the file by its SHA-256.
RUN printf '{"name":"util-linux","version":"%s","purl":"pkg:github/util-linux/util-linux@%s","sha256":"%s"}\n' \
      "${UTIL_LINUX_REF}" "${UTIL_LINUX_REF}" "$(sha256sum sfdisk.static | cut -d' ' -f1)" > /sbom-source.json

# Build BOOTy as an init
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS dev-packages
RUN set -eu; \
    n=0; \
    until apk add --no-cache git ca-certificates gcc linux-headers musl-dev; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "apk add failed after $n attempts" >&2; exit 1; fi; \
      sleep 10; \
    done
# The packages on their own stage, so package-versions can export their
# database without the flavor-dependent layers below.
FROM dev-packages AS dev
COPY go.mod go.sum /go/src/github.com/telekom/BOOTy/
WORKDIR /go/src/github.com/telekom/BOOTy
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    set -eu; \
    n=0; \
    until go mod download; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "go mod download failed after $n attempts" >&2; exit 1; fi; \
      sleep 10; \
    done
COPY . /go/src/github.com/telekom/BOOTy/
# Declared after the package and module downloads: a build argument changes
# the cache key of every later RUN, and the flavors share these layers.
ARG BOOTY_VERSION=dev
ARG BOOTY_BUILD=unknown
ARG BOOTY_COMMIT_TIME=
ARG BOOTY_FLAVOR=full
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    --mount=type=cache,sharing=locked,id=goroot,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -a -trimpath -tags grpcnotrace -ldflags "-linkmode external -extldflags '-static' -s -w \
      -X main.Version=${BOOTY_VERSION} \
      -X main.Build=${BOOTY_BUILD} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.version=${BOOTY_VERSION} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.commit=${BOOTY_BUILD} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.commitTime=${BOOTY_COMMIT_TIME} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.flavor=${BOOTY_FLAVOR}" -o init
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o esp-sync ./cmd/booty-esp-sync

# Build a removable UEFI fallback loader that BOOTy can copy into a target ESP.
# The embedded config loads EFI/BOOT/grub.cfg from the ESP; BOOTy writes that
# handoff config at provisioning time so the selected A/B root slot is explicit.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS efi-fallback
ARG TARGETARCH
RUN set -eux; \
    apt-get -o Acquire::Retries=5 update; \
    case "${TARGETARCH:-amd64}" in \
      arm64) grub_pkg="grub-efi-arm64-bin" ;; \
      amd64) grub_pkg="grub-efi-amd64-bin" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
      grub-common "${grub_pkg}" ca-certificates; \
    rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      arm64) grub_platform="arm64-efi"; efi_loader="BOOTAA64.EFI" ;; \
      amd64) grub_platform="x86_64-efi"; efi_loader="BOOTX64.EFI" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /out && \
    printf '%s\n' \
      'search --no-floppy --set=esp --file /EFI/BOOT/grub.cfg' \
      'configfile ($esp)/EFI/BOOT/grub.cfg' \
      > /tmp/booty-efi-fallback.cfg && \
    grub-mkstandalone -O "${grub_platform}" -o "/out/${efi_loader}" \
      --locales="" --fonts="" \
      --modules="part_gpt part_msdos fat ext2 search search_fs_file configfile normal" \
      "boot/grub/grub.cfg=/tmp/booty-efi-fallback.cfg"
# The ramdisk SBOM lists the loader as the GRUB package it is built from
# (--target sbom-inputs); it matches the file by its SHA-256.
RUN set -eu; \
    . /etc/os-release; \
    pkg=$(dpkg-query -W -f '${Package}\n' 'grub-efi-*-bin'); \
    ver=$(dpkg-query -W -f '${Version}' "$pkg"); \
    printf '{"name":"%s","version":"%s","purl":"pkg:deb/debian/%s@%s?arch=%s&distro=debian-%s","sha256":"%s"}\n' \
      "$pkg" "$ver" "$pkg" "$(printf %s "$ver" | sed 's/%/%25/g; s/:/%3A/g; s/+/%2B/g')" \
      "$(dpkg --print-architecture)" "$VERSION_ID" "$(sha256sum /out/*.EFI | cut -d' ' -f1)" > /sbom-source.json

# Build FRR (BGP/BFD/Zebra) for EVPN networking — use FRR official stable repo
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS frr
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    curl gnupg lsb-release ca-certificates && \
    curl --fail --location --show-error --silent \
      --retry 5 --retry-delay 5 --retry-connrefused --connect-timeout 30 \
      https://deb.frrouting.org/frr/keys.gpg \
      -o /tmp/frrouting.downloaded.gpg && \
    printf '%s\n' \
      3D9968AC9AE7BE1169288DDB1FD5839895F57FDA \
      4A56C7738BB3F81595A805D2A832769908F13ED1 \
      A90FC36D9429409798E9C2D874DEED43AB194DBF \
      BBC9ACA9D13025A2C186FF7F741E92A1F6E3975B \
      > /tmp/frrouting.expected-fingerprints && \
    export GNUPGHOME="$(mktemp -d)" && \
    chmod 700 "$GNUPGHOME" && \
    gpg --batch --import /tmp/frrouting.downloaded.gpg && \
    gpg --batch --export $(cat /tmp/frrouting.expected-fingerprints) \
      > /usr/share/keyrings/frrouting.gpg && \
    gpg --show-keys --with-colons /usr/share/keyrings/frrouting.gpg \
      | awk -F: '$1 == "pub" { primary=1; next } $1 == "sub" { primary=0 } $1 == "fpr" && primary { print $10; primary=0 }' \
      | sort -u > /tmp/frrouting.fingerprints && \
    sort -u /tmp/frrouting.expected-fingerprints -o /tmp/frrouting.expected-fingerprints && \
    diff -u /tmp/frrouting.expected-fingerprints /tmp/frrouting.fingerprints && \
    rm -rf "$GNUPGHOME" /tmp/frrouting.downloaded.gpg \
      /tmp/frrouting.expected-fingerprints /tmp/frrouting.fingerprints && \
    echo "deb [signed-by=/usr/share/keyrings/frrouting.gpg] https://deb.frrouting.org/frr $(lsb_release -s -c) frr-stable" \
      > /etc/apt/sources.list.d/frr.list && \
    apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    frr frr-pythontools && \
    # The packages of the FRR repository, for their purls in the ramdisk SBOM.
    apt-get indextargets --format '$(FILENAME)' 'Created-By: Packages' 'Site: https://deb.frrouting.org/frr' \
      | xargs -r -n1 /usr/lib/apt/apt-helper cat-file | awk '/^Package:/ {print $2}' | sort -u > /frrouting-packages && \
    grep -qx frr /frrouting-packages && \
    rm -rf /var/lib/apt/lists/*

# Collect shared libraries for FRR binaries (same rationale as tools stage).
RUN mkdir -p /frr-libs && \
    ldd /usr/lib/frr/bgpd /usr/lib/frr/zebra /usr/lib/frr/bfdd \
        /usr/bin/vtysh /usr/lib/frr/watchfrr 2>/dev/null \
    | awk '{for (i=1;i<=NF;i++) if ($i ~ /^\//) print $i}' \
    | sort -u | while read -r lib; do \
        [ -n "$lib" ] && [ -f "$lib" ] && cp -L --parents "$lib" /frr-libs/ 2>/dev/null || true; \
    done

# Extract kernel, storage, and NIC driver modules for bare-metal servers.
# The kernel is the Debian-signed linux-image-amd64 (or -arm64) of Debian 13
# trixie: Linux 6.12 LTS with its security updates. It chains to the Debian CA
# through shim, so it boots with Secure Boot, and its modules are signed with
# the key built into the kernel, so they load under lockdown.
# KERNEL_SUITE=trixie-backports selects the newest Debian-signed kernel (7.x).
# Do not make it the default: md arrays created on Linux 6.19 or later store a
# logical block size in the superblock, and kernels up to 6.18 (all supported
# target OS kernels today) refuse to assemble them. BOOTy creates the
# DISK_MIRRORING_MODE and explicit RAID arrays that the installed OS assembles.
# Upstream keeps this by design (commit a4166f1c4893) and has no opt-out.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS kernel
ARG TARGETARCH
ARG KERNEL_SUITE=
RUN if [ -n "$KERNEL_SUITE" ]; then \
        echo "deb http://deb.debian.org/debian $KERNEL_SUITE main" > /etc/apt/sources.list.d/kernel-suite.list; \
    fi && \
    apt-get -o Acquire::Retries=5 update && \
    apt-get -o Acquire::Retries=5 install -y --no-install-recommends kmod xz-utils && \
    KERNEL_META=$([ "$TARGETARCH" = "arm64" ] && echo "linux-image-arm64" || echo "linux-image-amd64") && \
    KERNEL_PKG=$(apt-cache depends ${KERNEL_SUITE:+-t "$KERNEL_SUITE"} "$KERNEL_META" | awk '/Depends: linux-image-/{print $2; exit}') && \
    test -n "$KERNEL_PKG" && \
    # Newer Debian kernels split linux-image-<ver> into linux-binary (signed
    # vmlinuz), linux-modules and linux-base packages; fetch whichever exist.
    KPKG_VER=${KERNEL_PKG#linux-image-} && \
    mkdir -p /tmp/debs && cd /tmp/debs && \
    for p in linux-image linux-binary linux-modules linux-base; do \
        if apt-cache show "$p-$KPKG_VER" >/dev/null 2>&1; then \
            apt-get -o Acquire::Retries=5 download "$p-$KPKG_VER"; \
        fi; \
    done && \
    for d in *.deb; do dpkg-deb -x "$d" /tmp/kernel; done && \
    cd / && \
    # usrmerge: trixie kernels install modules under /usr/lib/modules.
    MROOT=/tmp/kernel/usr && { [ -d "$MROOT/lib/modules" ] || MROOT=/tmp/kernel; } && \
    KVER=$(ls "$MROOT/lib/modules/" | head -1) && \
    MDIR="$MROOT/lib/modules/$KVER" && \
    # The vmlinuz is /boot/vmlinuz-<ver> or, in the split packages, <MDIR>/vmlinuz.
    VMLINUZ=$(ls /tmp/kernel/boot/vmlinuz-* "$MDIR/vmlinuz" 2>/dev/null | head -1) && \
    cp "$VMLINUZ" /vmlinuz && \
    mkdir -p /modules && \
    # Generate modules.dep for modprobe dependency resolution
    depmod -b "$MROOT" "$KVER" && \
    # Target modules — transitive dependencies resolved via modprobe --show-depends.
    # Module categories:
    #   QEMU/KVM virtio:  virtio virtio_ring virtio_pci_modern_dev virtio_pci_legacy_dev virtio_pci virtio_net failover net_failover
    #   Storage (SCSI):   scsi_mod sd_mod sg virtio_blk virtio_scsi (sg: SCSI generic for sg3-utils)
    #   Storage (HW):     nvme ahci megaraid_sas mpt3sas smartpqi hpsa loop
    #   Software RAID:    md_mod raid0 raid1 raid10 raid456 (mdadm; RAID 5/6 need raid456)
    #   EFI variables:    efivarfs (efibootmgr)
    #   Filesystems:      ext4 jbd2 mbcache crc32c_generic crc32c-cryptoapi xfs btrfs fat vfat nls_cp437 nls_iso8859-1 nls_ascii
    #   VRF/VXLAN/bridge: vrf dummy vxlan udp_tunnel ip6_udp_tunnel bridge stp llc
    #   VLAN:             8021q (bonding is left out: loading it creates bond0)
    #   Socket diag:      inet_diag tcp_diag udp_diag (ss and other sock_diag users)
    #   Intel NICs:       e1000e igb igc ixgbe i40e ice iavf igbvf ixgbevf (iavf, igbvf, ixgbevf: SR-IOV VFs)
    #   Broadcom NICs:    tg3 bnxt_en
    #   Mellanox/NVIDIA:  mlx4_core mlx4_en mlx5_core mlxfw
    #   Emulex/Broadcom:  be2net
    #   Other 10-100G:    qede (QLogic FastLinQ) cxgb4 (Chelsio T4-T6) sfc (AMD/Solarflare) ionic (AMD Pensando)
    #   Device-mapper:    dm_mod dm_crypt (LUKS encryption)
    #   Software RAID:    md_mod raid1 (DISK_MIRRORING_MODE, explicit RAID arrays)
    #   IPMI:             ipmi_msghandler ipmi_devintf ipmi_si ipmi_ssif
    #   Watchdog:         in /modules/watchdog, see the first loop below
    #
    # Hardware watchdog drivers go to /modules/watchdog. BOOTy loads them only
    # with WATCHDOG_ENABLE, so hosts without it keep their firmware watchdog
    # behaviour: Intel TCO (iTCO_wdt, with lpc_ich and i2c_i801 that create
    # its device), AMD (sp5100_tco), the BMC (ipmi_watchdog), ACPI WDAT
    # (wdat_wdt) and the QEMU i6300esb. The modprobe root comes from MDIR, so
    # the loop also works for a kernel package that installs under /usr.
    mkdir -p /modules/watchdog && \
    for w in iTCO_wdt lpc_ich i2c_i801 sp5100_tco ipmi_watchdog wdat_wdt i6300esb; do \
        modprobe --show-depends -d "${MDIR%/lib/modules/*}" -S "$KVER" "$w" 2>/dev/null \
            | awk '/^insmod /{print $2}' \
            | while read -r dep; do \
                [ -f "$dep" ] && cp -n "$dep" /modules/watchdog/ 2>/dev/null || true; \
            done; \
        find "$MDIR" -name "${w}.ko*" -exec cp -n {} /modules/watchdog/ \; 2>/dev/null || true; \
    done && \
    { [ "$TARGETARCH" != "amd64" ] || ls /modules/watchdog/iTCO_wdt.ko* /modules/watchdog/sp5100_tco.ko* \
        /modules/watchdog/ipmi_watchdog.ko* /modules/watchdog/i6300esb.ko* >/dev/null; } && \
    # The modules of the categories above:
    for m in \
        virtio virtio_ring virtio_pci_modern_dev virtio_pci_legacy_dev \
        virtio_pci virtio_net failover net_failover \
        scsi_mod sd_mod sg virtio_blk virtio_scsi \
        nvme ahci megaraid_sas mpt3sas smartpqi hpsa loop \
        md_mod raid0 raid1 raid10 raid456 \
        efivarfs \
        ext4 jbd2 mbcache crc32c_generic crc32c-cryptoapi xfs btrfs fat vfat nls_cp437 nls_iso8859-1 nls_ascii \
        vrf dummy vxlan udp_tunnel ip6_udp_tunnel bridge stp llc \
        8021q \
        inet_diag tcp_diag udp_diag \
        e1000e igb igc ixgbe i40e ice iavf igbvf ixgbevf \
        tg3 bnxt_en \
        mlx4_core mlx4_en mlx5_core mlxfw \
        be2net \
        qede cxgb4 sfc ionic \
        dm_mod dm_crypt \
        md_mod raid1 \
        ipmi_msghandler ipmi_devintf ipmi_si ipmi_ssif; do \
        # Copy module + all transitive dependencies via modprobe
        modprobe --show-depends -d "$MROOT" -S "$KVER" "$m" 2>/dev/null \
            | awk '/^insmod /{print $2}' \
            | while read -r dep; do \
                [ -f "$dep" ] && cp -n "$dep" /modules/ 2>/dev/null || true; \
            done; \
        # Fallback: direct find if modprobe doesn't resolve (builtin or alias)
        find "$MDIR" -name "${m}.ko*" -exec cp -n {} /modules/ \; 2>/dev/null || true; \
    done && \
    # Trixie kernels ship xz-compressed modules. BOOTy loads modules with
    # finit_module(fd, "", 0), which takes plain ELF objects, and the
    # initramfs is zstd-compressed as a whole. The signature stays appended.
    find /modules -name '*.ko.xz' -exec xz -d {} + && \
    if ls /modules/*.ko.* >/dev/null 2>&1; then echo "compressed modules left in /modules" >&2; exit 1; fi && \
    echo "Extracted $(ls /modules/*.ko 2>/dev/null | wc -l) kernel modules of $KVER" && \
    # The kernel packages are unpacked, not installed: record them in dpkg
    # status and .list form for the ramdisk SBOM (--target sbom-inputs).
    mkdir -p /sbom-dpkg/info && \
    for d in /tmp/debs/*.deb; do \
        { dpkg-deb -f "$d"; echo "Status: install ok installed"; echo; } >> /sbom-dpkg/status; \
        dpkg-deb --fsys-tarfile "$d" | tar -t | sed 's|^\.||' > "/sbom-dpkg/info/$(dpkg-deb -f "$d" Package).list"; \
    done && \
    rm -rf /tmp/kernel /tmp/debs /var/lib/apt/lists/*

# Build disk, system, and firmware tools
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS tools
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    binutils \
    mdadm util-linux fdisk e2fsprogs xfsprogs btrfs-progs parted gdisk kpartx dosfstools \
    efibootmgr dmidecode ethtool iproute2 bridge-utils \
    hdparm nvme-cli sg3-utils mstflint gpgv \
    dropbear-bin cryptsetup-bin \
    && rm -rf /var/lib/apt/lists/*

# Collect shared libraries for all tool binaries while their packages are
# installed.  Running ldd here (instead of in the final assembly stage)
# ensures every transitive dependency is resolved — the assembly stages
# only have base Debian libs and would report package-specific libs as
# "not found", silently dropping them from the initramfs.
RUN mkdir -p /tool-libs /tool-libs-full && \
    ldd /sbin/mdadm /usr/sbin/wipefs /sbin/resize2fs /sbin/e2fsck \
        /usr/sbin/mkfs.ext4 /usr/sbin/mkfs.vfat /usr/sbin/mkfs.xfs \
        /usr/sbin/xfs_growfs /usr/sbin/xfs_repair \
        /usr/bin/btrfs /usr/sbin/parted /usr/sbin/sgdisk /usr/sbin/sfdisk \
        /sbin/partprobe /usr/bin/partx /usr/sbin/kpartx /usr/bin/efibootmgr /usr/sbin/dmidecode /usr/sbin/ethtool \
        /sbin/ip /sbin/bridge /sbin/hdparm /usr/sbin/nvme /usr/bin/sg_sanitize /usr/bin/sg_format \
        /usr/bin/mstconfig /usr/bin/mstflint \
        /usr/sbin/dropbear /usr/bin/dropbearkey /bin/lsblk /sbin/blkid /usr/sbin/losetup \
        /sbin/cryptsetup 2>/dev/null \
    | awk '{for (i=1;i<=NF;i++) if ($i ~ /^\//) print $i}' \
    | sort -u | while read -r lib; do \
        [ -n "$lib" ] && [ -f "$lib" ] && cp -L --parents "$lib" /tool-libs/ 2>/dev/null || true; \
    done && \
    ldd /usr/bin/gpgv 2>/dev/null \
    | awk '{for (i=1;i<=NF;i++) if ($i ~ /^\//) print $i}' \
    | sort -u | while read -r lib; do \
        [ -n "$lib" ] && [ -f "$lib" ] && cp -L --parents "$lib" /tool-libs-full/ 2>/dev/null || true; \
    done

# Strip debug symbols from tool binaries to reduce initramfs size (~20-40% per binary).
# ldd must run first (above) so dynamic-library resolution is not affected by stripping.
# Shared libs in /tool-libs are intentionally NOT stripped — that can corrupt them.
RUN strip --strip-all \
    /sbin/mdadm /usr/sbin/wipefs /sbin/resize2fs /sbin/e2fsck \
        /usr/sbin/mkfs.ext4 /usr/sbin/mkfs.vfat /usr/sbin/mkfs.xfs \
        /usr/sbin/xfs_growfs /usr/sbin/xfs_repair \
        /usr/bin/btrfs /usr/sbin/parted /usr/sbin/sgdisk /usr/sbin/sfdisk \
        /sbin/partprobe /usr/bin/partx /usr/sbin/kpartx /usr/bin/efibootmgr /usr/sbin/dmidecode /usr/sbin/ethtool \
        /sbin/ip /sbin/bridge /sbin/hdparm /usr/sbin/nvme /usr/bin/sg_sanitize /usr/bin/sg_format \
        /usr/bin/mstconfig /usr/bin/mstflint \
        /usr/sbin/dropbear /usr/bin/dropbearkey /bin/lsblk /sbin/blkid /usr/sbin/losetup /usr/bin/gpgv \
        /sbin/cryptsetup

# Busybox static binary — sourced from Docker Hub for reliability and
# Dependabot version tracking.  Eliminates the fragile busybox.net download
# that caused CI failures when the site was unreachable.
FROM busybox:1.38.0-musl@sha256:80e38f9a1b39188b8bdb28879178edb322a4402b081a9aedda1924609decd6f6 AS busybox-bin

# The packages on their own stage, so sbom-inputs can export their database
# without building the ramdisk.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS busybox-packages
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    cpio ca-certificates zstd cloud-guest-utils \
    && rm -rf /var/lib/apt/lists/*

FROM busybox-packages AS busybox
WORKDIR /build/initramfs
RUN mkdir -p dev proc run sys tmp etc && \
    mknod -m 600 dev/console c 5 1 && \
    mknod -m 666 dev/null c 1 3 && \
    mknod -m 600 dev/ttyS0 c 4 64
# Users and groups: root, and frr and frrvty, which the FRR daemons switch
# to (zebra: "could not lookup user frr" without them). Every flavor gets
# them, so a ramdisk with FRR never lacks them. --chmod: the mode must not
# depend on the umask of the checkout (reproducible builds).
COPY --chmod=0644 hack/initramfs-etc/passwd hack/initramfs-etc/group etc/

# Pre-built static busybox binary from Docker Hub (cached by Docker layer cache)
COPY --from=busybox-bin /bin/busybox bin/busybox

# Create relative applet symlinks into bin/. `busybox --install -s` links to
# the absolute build path /build/initramfs/bin/busybox, which does not exist
# in the booted ramdisk, so every applet (sh, sync, blockdev, setsid) failed.
RUN for cmd in $(bin/busybox --list); do if [ "$cmd" != "busybox" ]; then ln -sf busybox "bin/$cmd"; fi; done

# Docker COPY follows destination symlinks: if bin/X -> busybox exists, COPY
# writes the source file content into the busybox binary instead of replacing
# the symlink.  Remove busybox symlinks that collide with real tool binaries.
RUN rm -f bin/partprobe bin/hdparm bin/ip bin/mkfs.vfat bin/mkfs.fat bin/mkdosfs bin/lsblk bin/blkid bin/losetup

# cloud-utils growpart
RUN cp /usr/bin/growpart bin/growpart

# Copy build contents from previous builds
COPY --from=lvm /LVM2.2.03.27/tools/lvm sbin/
RUN for cmd in pvcreate vgs vgcreate lvcreate; do ln -sf lvm "sbin/${cmd}"; done
COPY --from=sfdisk /util-linux/sfdisk.static bin/sfdisk
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .
# "booty lldp ..." and other commands in the rescue shell run the agent binary.
RUN ln -sf ../init bin/booty
RUN mkdir -p usr/lib/booty
COPY --from=dev /go/src/github.com/telekom/BOOTy/esp-sync usr/lib/booty/booty-esp-sync
COPY --from=efi-fallback /out/ usr/lib/booty/efi/

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
COPY --from=tools /sbin/blkid sbin/blkid
COPY --from=tools /usr/sbin/losetup bin/losetup
COPY --from=tools /usr/sbin/mkfs.ext4 sbin/mkfs.ext4
COPY --from=tools /usr/sbin/mkfs.vfat sbin/mkfs.vfat
COPY --from=tools /usr/sbin/mkfs.xfs sbin/mkfs.xfs
COPY --from=tools /usr/sbin/xfs_growfs sbin/xfs_growfs
COPY --from=tools /usr/sbin/xfs_repair sbin/xfs_repair
COPY --from=tools /usr/bin/btrfs bin/btrfs
COPY --from=tools /usr/sbin/parted bin/parted
COPY --from=tools /usr/sbin/sgdisk bin/sgdisk
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /usr/bin/partx bin/partx
COPY --from=tools /bin/lsblk bin/lsblk
COPY --from=tools /usr/sbin/kpartx bin/kpartx
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr
COPY --from=tools /usr/sbin/dmidecode bin/dmidecode
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /sbin/bridge bin/bridge

# Secure erase tools (sg3-utils: SCSI SANITIZE and FORMAT UNIT for SAS disks)
COPY --from=tools /sbin/hdparm bin/hdparm
COPY --from=tools /usr/sbin/nvme bin/nvme
COPY --from=tools /usr/bin/sg_sanitize bin/sg_sanitize
COPY --from=tools /usr/bin/sg_format bin/sg_format

# LUKS disk encryption
COPY --from=tools /sbin/cryptsetup sbin/cryptsetup

# Firmware tools (Mellanox ConnectX SR-IOV config)
COPY --from=tools /usr/bin/mstconfig bin/mstconfig
COPY --from=tools /usr/bin/mstflint bin/mstflint


# Dropbear lightweight SSH server for rescue mode (~110 KB)
COPY --from=tools /usr/sbin/dropbear bin/dropbear
COPY --from=tools /usr/bin/dropbearkey bin/dropbearkey

# Trust store and signature verification for HTTPS image/CAPRF/token/sysext
# fetches and IMAGE_SIGNATURE_URL verification.
RUN mkdir -p etc/ssl/certs && cp /etc/ssl/certs/ca-certificates.crt etc/ssl/certs/
COPY --from=tools /usr/bin/gpgv bin/gpgv

# Copy pre-collected shared libraries from stages where packages are installed.
# This replaces the previous ldd scan that ran in this stage — that approach
# missed package-specific libs (libefivar, libmnl, etc.) because they were
# not installed here and ldd reported them as "not found".
COPY --from=tools /tool-libs/ .
COPY --from=tools /tool-libs-full/ .
COPY --from=frr /frr-libs/ .

# Kernel modules for common server NICs (flat directory, loaded via insmod)
COPY --from=kernel /modules/ modules/

# pack-initramfs.sh stamps every entry with this time (reproducible builds).
ARG SOURCE_DATE_EPOCH=315532800

# Drop the libraries that no shipped executable loads, then check in a chroot
# that every dynamic executable resolves its libraries from the tree alone.
# Nothing may change the tree between this step and cpio.
COPY hack/prune-initramfs-libs.sh hack/pack-initramfs.sh /usr/local/bin/
# The FRR daemons run as frr with the vty group frrvty: both must resolve
# in the tree (busybox id reads its /etc/passwd and /etc/group).
RUN chroot . /bin/busybox id frr | tee /dev/stderr | grep -q '(frrvty)' && \
    chroot . /bin/busybox id root >/dev/null
RUN prune-initramfs-libs.sh .

# Package initramfs
RUN pack-initramfs.sh . /initramfs.cpio.zst

# ── Split ramdisk (optional, --target=split) ───────────────────────────────
# The default tree as a base ramdisk plus one archive per optional payload
# (FRR), which the boot loader loads only for the runs
# that need them. See docs/proposal-ramdisk-split.md and pkg/payload.
# The kernel comes from the same build as the modules of the base, and
# SHA256SUMS lists every file, so one pin covers the set.
FROM busybox AS split-builder
ARG BOOTY_BUILD=unknown
ARG SOURCE_DATE_EPOCH=315532800
COPY hack/split-initramfs.sh /usr/local/bin/
RUN split-initramfs.sh /build/initramfs /split "$BOOTY_BUILD"
COPY --from=kernel /vmlinuz /split/vmlinuz
RUN cd /split && sha256sum base.cpio.zst frr.cpio.zst vmlinuz > SHA256SUMS

FROM scratch AS split
COPY --from=split-builder /split/ .

# ── Ramdisk SBOM inputs (optional, --target=sbom-inputs) ─────────────────
# What hack/ramdisk_sbom.py needs besides an extracted ramdisk: the package
# databases of the stages whose Debian files the ramdisks ship, the kernel
# packages, and the components built in the build (LVM2, sfdisk, the GRUB
# EFI loader), each with the SHA-256 of its file. Nothing here goes into a
# ramdisk. The busybox stage ships growpart (cloud-guest-utils); slim-builder
# installs the same package from the same mirror.
FROM scratch AS sbom-inputs
COPY --from=tools /usr/lib/os-release os-release
COPY --from=tools /var/lib/dpkg/status dpkg/tools/status
COPY --from=tools /var/lib/dpkg/info/ dpkg/tools/info/
COPY --from=busybox-packages /var/lib/dpkg/status dpkg/busybox/status
COPY --from=busybox-packages /var/lib/dpkg/info/ dpkg/busybox/info/
COPY --from=frr /var/lib/dpkg/status dpkg/frr/status
COPY --from=frr /var/lib/dpkg/info/ dpkg/frr/info/
COPY --from=frr /frrouting-packages dpkg/frr/frrouting-packages
COPY --from=kernel /sbom-dpkg/ dpkg/kernel/
COPY --from=lvm /sbom-source.json source/lvm2.json
COPY --from=sfdisk /sbom-source.json source/util-linux.json
COPY --from=efi-fallback /sbom-source.json source/grub-efi.json

# ── ISO build stage (optional, triggered by --target=iso) ──────────────────
# UEFI boot image: Debian shim (Microsoft UEFI CA) + Debian-signed GRUB,
# which verifies the Debian-signed kernel, so the ISO boots with Secure Boot.
# The package versions are pinned; bump them together when trixie updates
# shim or GRUB (an old version leaves the mirror, so the build fails loudly).
# The manifest records the versions and SHA-256 sums, and the ISO ships it as
# /boot/efi-manifest.txt. amd64 only, like ISOLINUX.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS efi-boot
ARG SHIM_SIGNED_VERSION=1.51~1+deb13u1+16.1-2~deb13u1
ARG SHIM_HELPERS_VERSION=1+16.1+2~deb13u1
ARG GRUB_SIGNED_VERSION=1+2.12+9+deb13u2
ARG SOURCE_DATE_EPOCH=315532800
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    "shim-signed=${SHIM_SIGNED_VERSION}" "shim-helpers-amd64-signed=${SHIM_HELPERS_VERSION}" \
    "grub-efi-amd64-signed=${GRUB_SIGNED_VERSION}" dosfstools mtools \
    && rm -rf /var/lib/apt/lists/*
COPY hack/make-efiboot.sh /usr/local/bin/
RUN make-efiboot.sh /efiboot.img /efi-manifest.txt

# Shared by every ISO target (and reusable for other ISOs): BIOS ISOLINUX +
# UEFI El Torito + isohybrid MBR + GPT with an ESP, see hack/make-iso.sh.
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS iso-tools
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    xorriso isolinux syslinux-common \
    && rm -rf /var/lib/apt/lists/*
COPY hack/make-iso.sh /usr/local/bin/
COPY --from=efi-boot /efiboot.img /efiboot.img
COPY --from=efi-boot /efi-manifest.txt /efi-manifest.txt

FROM iso-tools AS iso-builder
ARG ISO_VOLID=BOOTY
ARG SOURCE_DATE_EPOCH=315532800
COPY --from=efi-boot /efi-manifest.txt /iso/boot/efi-manifest.txt
COPY --from=busybox /initramfs.cpio.zst /iso/boot/initrd.img
# Use the standard Debian kernel (has all common NIC drivers as modules)
COPY --from=kernel /vmlinuz /iso/boot/vmlinuz
RUN make-iso.sh /iso /efiboot.img /booty.iso "$ISO_VOLID"

FROM scratch AS iso
COPY --from=iso-builder /booty.iso .

# ── Slim target: BOOTy + busybox shell + minimal tools, no FRR/LVM ────────
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS slim-builder-packages
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends cpio zstd ca-certificates cloud-guest-utils \
    && rm -rf /var/lib/apt/lists/*
# The packages on their own stage, so package-versions can export their
# database without the flavor-dependent layers below.
FROM slim-builder-packages AS slim-builder
WORKDIR /build/initramfs
RUN mkdir -p dev proc run sys tmp etc && \
    mknod -m 600 dev/console c 5 1 && \
    mknod -m 666 dev/null c 1 3 && \
    mknod -m 600 dev/ttyS0 c 4 64
# Users and groups: root, and frr and frrvty, which the FRR daemons switch
# to (zebra: "could not lookup user frr" without them). Every flavor gets
# them, so a ramdisk with FRR never lacks them. --chmod: the mode must not
# depend on the umask of the checkout (reproducible builds).
COPY --chmod=0644 hack/initramfs-etc/passwd hack/initramfs-etc/group etc/

# Copy busybox binary and create all applet symlinks (same as default variant).
# This ensures utilities like tail, grep, dmesg, hostname, ps, and xargs are
# available for debug diagnostics and shell commands.
COPY --from=busybox-bin /bin/busybox bin/busybox
RUN for cmd in $(bin/busybox --list); do if [ "$cmd" != "busybox" ]; then ln -sf busybox "bin/$cmd"; fi; done
# Docker COPY follows destination symlinks — remove colliding busybox symlinks.
RUN rm -f bin/partprobe bin/ip bin/wipefs bin/sgdisk bin/sfdisk \
    bin/mkfs.vfat bin/mkfs.fat bin/mkdosfs bin/lsblk bin/blkid bin/losetup
RUN cp /usr/bin/growpart bin/growpart

# BOOTy init binary (static, CGO-enabled)
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .
# "booty lldp ..." and other commands in the rescue shell run the agent binary.
RUN ln -sf ../init bin/booty
RUN mkdir -p usr/lib/booty
COPY --from=dev /go/src/github.com/telekom/BOOTy/esp-sync usr/lib/booty/booty-esp-sync

# Minimal networking tools (DHCP mode — no FRR, no BGP)
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /usr/sbin/ethtool bin/ethtool
# EFI boot entries: on UEFI a failed efibootmgr fails provisioning
# (EFI_BOOT_ENTRY_REQUIRED); its libraries are in /tool-libs.
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr
RUN mkdir -p etc/ssl/certs && cp /etc/ssl/certs/ca-certificates.crt etc/ssl/certs/

# Provisioning disk tools without FRR or LVM support.
RUN mkdir -p sbin
COPY --from=tools /sbin/mdadm sbin/mdadm
COPY --from=tools /usr/sbin/wipefs bin/wipefs
COPY --from=tools /usr/sbin/sgdisk bin/sgdisk
COPY --from=tools /usr/sbin/sfdisk bin/sfdisk
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /usr/bin/partx bin/partx
COPY --from=tools /bin/lsblk bin/lsblk
COPY --from=tools /sbin/e2fsck sbin/e2fsck
COPY --from=tools /sbin/blkid sbin/blkid
COPY --from=tools /usr/sbin/losetup bin/losetup
COPY --from=tools /sbin/resize2fs sbin/resize2fs
COPY --from=tools /usr/sbin/mkfs.ext4 sbin/mkfs.ext4
COPY --from=tools /usr/sbin/mkfs.vfat sbin/mkfs.vfat
COPY --from=tools /usr/sbin/mkfs.xfs sbin/mkfs.xfs
COPY --from=tools /usr/sbin/xfs_growfs sbin/xfs_growfs
COPY --from=tools /usr/sbin/xfs_repair sbin/xfs_repair
COPY --from=tools /usr/bin/btrfs bin/btrfs
# UEFI boot entries for the target disk and, with DISK_MIRRORING_MODE, the mirror disk.
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr

# Copy pre-collected shared libraries (slim was previously missing lib copying
# entirely, breaking all dynamically linked tool binaries at runtime).
COPY --from=tools /tool-libs/ .

# Kernel modules: storage controllers, software RAID and efivarfs are modules
# of the Debian kernel, and slim ships mdadm and the disk tools.
COPY --from=kernel /modules/ modules/

# pack-initramfs.sh stamps every entry with this time (reproducible builds).
ARG SOURCE_DATE_EPOCH=315532800

# Drop the libraries of the tools that slim does not ship, then check in a
# chroot that every dynamic executable resolves its libraries from the tree
# alone. Nothing may change the tree between this step and cpio.
COPY hack/prune-initramfs-libs.sh hack/pack-initramfs.sh /usr/local/bin/
RUN prune-initramfs-libs.sh .

# Package slim initramfs
RUN pack-initramfs.sh . /initramfs.cpio.zst

FROM scratch AS slim
COPY --from=slim-builder /initramfs.cpio.zst .

# ── GoBGP target: like default but without FRR (GoBGP is in-process Go) ───
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS gobgp-builder-packages
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends cpio zstd \
    && rm -rf /var/lib/apt/lists/*
# The packages on their own stage, so package-versions can export their
# database without the flavor-dependent layers below.
FROM gobgp-builder-packages AS gobgp-builder
WORKDIR /build/initramfs
RUN mkdir -p dev proc run sys tmp etc && \
    mknod -m 600 dev/console c 5 1 && \
    mknod -m 666 dev/null c 1 3 && \
    mknod -m 600 dev/ttyS0 c 4 64
# Users and groups: root, and frr and frrvty, which the FRR daemons switch
# to (zebra: "could not lookup user frr" without them). Every flavor gets
# them, so a ramdisk with FRR never lacks them. --chmod: the mode must not
# depend on the umask of the checkout (reproducible builds).
COPY --chmod=0644 hack/initramfs-etc/passwd hack/initramfs-etc/group etc/

# Copy busybox binary and create all applet symlinks (same as default variant).
# This ensures utilities like tail, pgrep, dmesg, lsblk, hostname, ps, grep,
# kill, id, and xargs are available for debug diagnostics and shell commands.
COPY --from=busybox-bin /bin/busybox bin/busybox
RUN for cmd in $(bin/busybox --list); do if [ "$cmd" != "busybox" ]; then ln -sf busybox "bin/$cmd"; fi; done
# Docker COPY follows destination symlinks — remove colliding busybox symlinks.
RUN rm -f bin/partprobe bin/hdparm bin/ip bin/mkfs.vfat bin/mkfs.fat bin/mkdosfs bin/lsblk bin/blkid bin/losetup
COPY --from=busybox /build/initramfs/bin/growpart bin/growpart

# BOOTy init binary (with GoBGP compiled in)
COPY --from=dev /go/src/github.com/telekom/BOOTy/init .
# "booty lldp ..." and other commands in the rescue shell run the agent binary.
RUN ln -sf ../init bin/booty
RUN mkdir -p usr/lib/booty
COPY --from=dev /go/src/github.com/telekom/BOOTy/esp-sync usr/lib/booty/booty-esp-sync
COPY --from=efi-fallback /out/ usr/lib/booty/efi/

# LVM + sfdisk for disk management
RUN mkdir -p sbin
COPY --from=lvm /LVM2.2.03.27/tools/lvm sbin/lvm
RUN for cmd in pvcreate vgs vgcreate lvcreate; do ln -sf lvm "sbin/${cmd}"; done
COPY --from=sfdisk /util-linux/sfdisk.static bin/sfdisk

# Disk and system tools (same as default)
COPY --from=tools /sbin/mdadm sbin/mdadm
COPY --from=tools /usr/sbin/wipefs bin/wipefs
COPY --from=tools /sbin/resize2fs sbin/resize2fs
COPY --from=tools /sbin/e2fsck sbin/e2fsck
COPY --from=tools /sbin/blkid sbin/blkid
COPY --from=tools /usr/sbin/losetup bin/losetup
COPY --from=tools /usr/sbin/mkfs.ext4 sbin/mkfs.ext4
COPY --from=tools /usr/sbin/mkfs.vfat sbin/mkfs.vfat
COPY --from=tools /usr/sbin/mkfs.xfs sbin/mkfs.xfs
COPY --from=tools /usr/sbin/xfs_growfs sbin/xfs_growfs
COPY --from=tools /usr/sbin/xfs_repair sbin/xfs_repair
COPY --from=tools /usr/bin/btrfs bin/btrfs
COPY --from=tools /usr/sbin/parted bin/parted
COPY --from=tools /usr/sbin/sgdisk bin/sgdisk
COPY --from=tools /sbin/partprobe bin/partprobe
COPY --from=tools /usr/bin/partx bin/partx
COPY --from=tools /usr/sbin/kpartx bin/kpartx
COPY --from=tools /usr/bin/efibootmgr bin/efibootmgr
COPY --from=tools /usr/sbin/dmidecode bin/dmidecode
COPY --from=tools /usr/sbin/ethtool bin/ethtool
COPY --from=tools /sbin/ip bin/ip
COPY --from=tools /sbin/bridge bin/bridge

# Secure erase tools (sg3-utils: SCSI SANITIZE and FORMAT UNIT for SAS disks)
COPY --from=tools /sbin/hdparm bin/hdparm
COPY --from=tools /usr/sbin/nvme bin/nvme
COPY --from=tools /usr/bin/sg_sanitize bin/sg_sanitize
COPY --from=tools /usr/bin/sg_format bin/sg_format

# LUKS disk encryption
COPY --from=tools /sbin/cryptsetup sbin/cryptsetup

# Firmware tools (Mellanox ConnectX SR-IOV config)
COPY --from=tools /usr/bin/mstconfig bin/mstconfig
COPY --from=tools /usr/bin/mstflint bin/mstflint


# Dropbear lightweight SSH server for rescue mode (~110 KB)
COPY --from=tools /usr/sbin/dropbear bin/dropbear
COPY --from=tools /usr/bin/dropbearkey bin/dropbearkey

# lsblk for rescue mode disk auto-mount
COPY --from=tools /bin/lsblk bin/lsblk

# Trust store and signature verification for HTTPS image/CAPRF/token/sysext
# fetches and IMAGE_SIGNATURE_URL verification.
RUN mkdir -p etc/ssl/certs
COPY --from=busybox /etc/ssl/certs/ca-certificates.crt etc/ssl/certs/
COPY --from=tools /usr/bin/gpgv bin/gpgv

# Copy pre-collected shared libraries from the tools stage.
COPY --from=tools /tool-libs/ .
COPY --from=tools /tool-libs-full/ .

# Kernel modules for common server NICs (flat directory, loaded via insmod)
COPY --from=kernel /modules/ modules/

# pack-initramfs.sh stamps every entry with this time (reproducible builds).
ARG SOURCE_DATE_EPOCH=315532800

# Drop the libraries that no shipped executable loads, then check in a chroot
# that every dynamic executable resolves its libraries from the tree alone.
# Nothing may change the tree between this step and cpio.
COPY hack/prune-initramfs-libs.sh hack/pack-initramfs.sh /usr/local/bin/
RUN prune-initramfs-libs.sh .

# Package GoBGP initramfs (no FRR binaries, GoBGP runs in-process). The
# Debian kernel has CONFIG_RD_ZSTD=y built-in.
RUN pack-initramfs.sh . /initramfs.cpio.zst

FROM scratch AS gobgp
COPY --from=gobgp-builder /initramfs.cpio.zst .

# ── GoBGP ISO target ──────────────────────────────────────────────────────
FROM iso-tools AS gobgp-iso-builder
ARG ISO_VOLID=BOOTY
ARG SOURCE_DATE_EPOCH=315532800
COPY --from=efi-boot /efi-manifest.txt /iso/boot/efi-manifest.txt
COPY --from=gobgp-builder /initramfs.cpio.zst /iso/boot/initrd.img
COPY --from=kernel /vmlinuz /iso/boot/vmlinuz
RUN make-iso.sh /iso /efiboot.img /booty-gobgp.iso "$ISO_VOLID"

FROM scratch AS gobgp-iso
COPY --from=gobgp-iso-builder /booty-gobgp.iso .

# ── Cable check ISO target ────────────────────────────────────────────────
# The GoBGP ISO with hack/check-iso/vars as /deploy/vars, in an
# uncompressed cpio archive before the zstd initramfs (like early
# microcode; an archive appended after the zstd one did not unpack in our
# test). One generic ISO for every host: MODE=check, links mode, bm4x LLDP
# identity and CHECK_HOLD=until-poweroff. make-iso.sh packs it like the
# other ISOs: BIOS (ISOLINUX) and UEFI with Secure Boot (Debian shim, GRUB
# and kernel).
FROM gobgp-builder AS check-iso-vars
ARG SOURCE_DATE_EPOCH=315532800
COPY --chmod=0644 hack/check-iso/vars /deploy-root/deploy/vars
RUN pack-initramfs.sh /deploy-root /deploy.cpio

FROM iso-tools AS check-iso-builder
ARG ISO_VOLID=BOOTY
ARG SOURCE_DATE_EPOCH=315532800
COPY --from=efi-boot /efi-manifest.txt /iso/boot/efi-manifest.txt
COPY --from=check-iso-vars /deploy.cpio /deploy.cpio
COPY --from=gobgp-builder /initramfs.cpio.zst /initramfs.cpio.zst
COPY --from=kernel /vmlinuz /iso/boot/vmlinuz
RUN cat /deploy.cpio /initramfs.cpio.zst > /iso/boot/initrd.img \
    && make-iso.sh /iso /efiboot.img /booty-check.iso "$ISO_VOLID"

FROM scratch AS check-iso
COPY --from=check-iso-builder /booty-check.iso .

# ── Micro target: pure-Go BOOTy only, no external binaries ────────────────
FROM golang:1.27.2-trixie@sha256:47667934874913a449b96faa7ef4777fa285d9ec29126ee80ed61c45b7cccf0e AS micro-dev
ARG BOOTY_VERSION=dev
ARG BOOTY_BUILD=unknown
ARG BOOTY_COMMIT_TIME=
ARG BOOTY_FLAVOR=micro
COPY go.mod go.sum /go/src/github.com/telekom/BOOTy/
WORKDIR /go/src/github.com/telekom/BOOTy
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    set -eu; \
    n=0; \
    until go mod download; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "go mod download failed after $n attempts" >&2; exit 1; fi; \
      sleep 10; \
    done
COPY . /go/src/github.com/telekom/BOOTy/
RUN --mount=type=cache,sharing=locked,id=gomod,target=/go/pkg/mod/cache \
    --mount=type=cache,sharing=locked,id=goroot,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -a -trimpath -tags grpcnotrace -ldflags "-s -w \
      -X main.Version=${BOOTY_VERSION} \
      -X main.Build=${BOOTY_BUILD} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.version=${BOOTY_VERSION} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.commit=${BOOTY_BUILD} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.commitTime=${BOOTY_COMMIT_TIME} \
      -X github.com/telekom/BOOTy/pkg/buildinfo.flavor=${BOOTY_FLAVOR}" -o init

FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS micro-builder-packages
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends cpio ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# The packages on their own stage, so package-versions can export their
# database without the flavor-dependent layers below.
FROM micro-builder-packages AS micro-builder
WORKDIR /build/initramfs
RUN mkdir -p bin sbin dev proc run sys tmp etc && \
    mknod -m 600 dev/console c 5 1 && \
    mknod -m 666 dev/null c 1 3 && \
    mknod -m 600 dev/ttyS0 c 4 64
# Users and groups: root, and frr and frrvty, which the FRR daemons switch
# to (zebra: "could not lookup user frr" without them). Every flavor gets
# them, so a ramdisk with FRR never lacks them. --chmod: the mode must not
# depend on the umask of the checkout (reproducible builds).
COPY --chmod=0644 hack/initramfs-etc/passwd hack/initramfs-etc/group etc/
COPY --from=micro-dev /go/src/github.com/telekom/BOOTy/init .
# "booty lldp ..." and other commands in the rescue shell run the agent binary.
RUN ln -sf ../init bin/booty
RUN mkdir -p etc/ssl/certs && cp /etc/ssl/certs/ca-certificates.crt etc/ssl/certs/

# Package micro initramfs
ARG SOURCE_DATE_EPOCH=315532800
COPY hack/pack-initramfs.sh /usr/local/bin/
RUN pack-initramfs.sh . /initramfs.cpio.gz

FROM scratch AS micro
COPY --from=micro-builder /initramfs.cpio.gz .

# ── Package versions (optional, --target=package-versions) ───────────────
# The Debian and Alpine package databases of every stage that installs
# packages for a ramdisk or for the tools that build and pack it.
# hack/check-reproducible.sh lists the versions of both rounds when they
# differ, so a mirror that served another version in between shows up.
# Nothing here goes into a ramdisk. After the stages it copies from: a
# stage name defined later would be taken for an image name.
FROM scratch AS package-versions
COPY --from=dev-packages /lib/apk/db/installed apk/dev
COPY --from=lvm /var/lib/dpkg/status dpkg/lvm
COPY --from=sfdisk /var/lib/dpkg/status dpkg/sfdisk
COPY --from=efi-fallback /var/lib/dpkg/status dpkg/efi-fallback
COPY --from=frr /var/lib/dpkg/status dpkg/frr
COPY --from=kernel /var/lib/dpkg/status dpkg/kernel
COPY --from=kernel /sbom-dpkg/status dpkg/kernel-packages
COPY --from=tools /var/lib/dpkg/status dpkg/tools
COPY --from=busybox-packages /var/lib/dpkg/status dpkg/busybox
COPY --from=slim-builder-packages /var/lib/dpkg/status dpkg/slim-builder
COPY --from=gobgp-builder-packages /var/lib/dpkg/status dpkg/gobgp-builder
COPY --from=micro-builder-packages /var/lib/dpkg/status dpkg/micro-builder

# Kernel that matches the shipped modules (--target kernel-image), for
# booting a flavor initramfs in QEMU.
FROM scratch AS kernel-image
COPY --from=kernel /vmlinuz /vmlinuz

# ── Default target: initramfs ──────────────────────────────────────────────
FROM scratch
COPY --from=busybox /initramfs.cpio.zst .
