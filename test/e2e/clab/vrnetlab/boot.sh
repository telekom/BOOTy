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
ETH1_READY=false
for i in $(seq 1 60); do
    if ip link show eth1 >/dev/null 2>&1; then
        ETH1_READY=true
        break
    fi
    sleep 0.5
done

if [ "$ETH1_READY" != "true" ]; then
    echo "[boot.sh] ERROR: eth1 not found after 30s — containerlab link not ready" >&2
    exit 1
fi

# Create tap device and bridge it with container's eth1.
# Traffic path: QEMU VM eth0 ↔ tap0 ↔ br-data ↔ eth1 ↔ containerlab fabric
ip tuntap add mode tap tap0

# Match TAP MTU to the containerlab link MTU so jumbo frames pass through.
ETH1_MTU=$(cat /sys/class/net/eth1/mtu 2>/dev/null || echo 9000)
ip link set tap0 mtu "$ETH1_MTU"
ip link set tap0 up

ip link add name br-data type bridge
# Disable multicast snooping — required for IPv6 NDP (BGP unnumbered) to
# traverse the bridge immediately without waiting for MLD/IGMP queries.
echo 0 > /sys/devices/virtual/net/br-data/bridge/multicast_snooping 2>/dev/null || true
ip link set dev br-data mtu "$ETH1_MTU"
ip link set dev br-data up
ip link set dev eth1 master br-data
ip link set dev tap0 master br-data
ip addr flush dev eth1

# Prevent iptables/ip6tables from filtering bridge traffic.
sysctl -q -w net.bridge.bridge-nf-call-iptables=0 2>/dev/null || true
sysctl -q -w net.bridge.bridge-nf-call-ip6tables=0 2>/dev/null || true

# ── Determine QEMU acceleration ───────────────────────────────────────
KVM_ARGS=""
if [ -c /dev/kvm ]; then
    KVM_ARGS="-enable-kvm -cpu host"
    echo "[boot.sh] KVM acceleration enabled"
else
    echo "[boot.sh] KVM not available, using TCG emulation"
fi

# ── Generate unique VM MAC address ─────────────────────────────────────
# Each container needs a unique MAC so BGP unnumbered (IPv6 link-local)
# addresses don't collide across VMs sharing the same spine.
VM_MAC="52:54:00:$(printf '%02x' $((RANDOM % 256))):$(printf '%02x' $((RANDOM % 256))):$(printf '%02x' $((RANDOM % 256)))"
echo "[boot.sh] VM MAC: ${VM_MAC}"

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
    -device virtio-net-pci,netdev=data,mac=${VM_MAC} \
    -netdev tap,id=data,ifname=tap0,script=no,downscript=no
