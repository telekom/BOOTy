#!/bin/bash
# Container entrypoint for BOOTy vrnetlab VM.
# Injects /deploy/vars into the initramfs, bridges the container's
# data interface (eth1) to a QEMU tap device, then boots the VM.
set -e

# ── Prepare initramfs ──────────────────────────────────────────────────
cd /opt/initramfs

# Inject /deploy/vars if bind-mounted into the container
if [ -f /deploy/vars ]; then
    mkdir -p deploy
    cp /deploy/vars deploy/vars
fi

# Pack initramfs
find . -print0 | cpio --null -ov --format=newc 2>/dev/null | gzip > /tmp/initramfs.cpio.gz

# ── Set up QEMU networking ─────────────────────────────────────────────
# Wait for eth1 (containerlab data interface)
for i in $(seq 1 60); do
    ip link show eth1 2>/dev/null && break
    sleep 0.5
done

# Create tap device and bridge it with container's eth1.
# Traffic path: QEMU VM eth0 ↔ tap0 ↔ br-data ↔ eth1 ↔ containerlab fabric
ip tuntap add mode tap tap0
ip link set tap0 up
ip link add name br-data type bridge
ip link set dev br-data up
ip link set dev eth1 master br-data
ip link set dev tap0 master br-data
ip addr flush dev eth1

# ── Determine QEMU acceleration ───────────────────────────────────────
KVM_ARGS=""
if [ -c /dev/kvm ]; then
    KVM_ARGS="-enable-kvm -cpu host"
    echo "[boot.sh] KVM acceleration enabled"
else
    echo "[boot.sh] KVM not available, using TCG emulation"
fi

echo "[boot.sh] Booting BOOTy VM..."

# ── Boot QEMU ──────────────────────────────────────────────────────────
# net.ifnames=0 forces classic eth0 naming inside the VM.
# virtio-net-pci is the NIC — Debian cloud kernel has virtio built-in.
exec qemu-system-x86_64 \
    -kernel /opt/vmlinuz \
    -initrd /tmp/initramfs.cpio.gz \
    -append "console=ttyS0 panic=1 net.ifnames=0" \
    -m 512 \
    -nographic \
    -no-reboot \
    ${KVM_ARGS} \
    -device virtio-net-pci,netdev=data \
    -netdev tap,id=data,ifname=tap0,script=no,downscript=no
