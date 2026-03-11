# Build BOOTy VM image for containerlab vrnetlab-style testing.
# Packages QEMU + kernel + BOOTy initramfs into a container that boots
# a real VM inside containerlab's EVPN fabric.
#
# Build:  docker build -t booty-vrnetlab:latest -f test/e2e/clab/vrnetlab/Dockerfile .

# ── Stage 1: Build BOOTy static binary ────────────────────────────────
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates gcc linux-headers musl-dev
COPY . /src
WORKDIR /src
RUN CGO_ENABLED=1 GOOS=linux go build -a \
    -ldflags "-linkmode external -extldflags '-static' -s -w" \
    -o /booty

# ── Stage 2: Extract a bootable kernel ────────────────────────────────
FROM debian:bookworm-slim AS kernel
RUN apt-get update && \
    apt-get download linux-image-cloud-amd64 2>/dev/null && \
    dpkg-deb -x linux-image-cloud-amd64*.deb /tmp/kernel && \
    cp /tmp/kernel/boot/vmlinuz-* /vmlinuz && \
    rm -rf /tmp/kernel *.deb /var/lib/apt/lists/*

# ── Stage 3: Assemble initramfs ───────────────────────────────────────
FROM debian:bookworm-slim AS initramfs
RUN apt-get update && apt-get install -y --no-install-recommends \
    cpio busybox-static \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /initramfs
RUN mkdir -p bin sbin dev proc sys etc tmp deploy lib/modules mnt

# Busybox: static binary + essential symlinks
RUN cp /bin/busybox bin/busybox && \
    cd bin && \
    for cmd in sh mount umount insmod ip ping cat echo ls mkdir rm \
               chmod sleep ln test grep awk sed head tail; do \
        ln -sf busybox "$cmd"; \
    done

# BOOTy binary (exec'd by init wrapper)
COPY --from=builder /booty /initramfs/booty

# Init wrapper (PID 1 — sets up network then execs BOOTy)
COPY test/e2e/clab/vrnetlab/init-wrapper.sh /initramfs/init
RUN chmod +x /initramfs/init

# ── Stage 4: Runtime container with QEMU ──────────────────────────────
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-system-x86 iproute2 bridge-utils cpio gzip procps \
    && rm -rf /var/lib/apt/lists/*

COPY --from=kernel /vmlinuz /opt/vmlinuz
COPY --from=initramfs /initramfs /opt/initramfs

COPY test/e2e/clab/vrnetlab/boot.sh /opt/boot.sh
RUN chmod +x /opt/boot.sh

ENTRYPOINT ["/opt/boot.sh"]
