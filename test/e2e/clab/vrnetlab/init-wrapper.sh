#!/bin/sh
# PID 1 init wrapper for BOOTy QEMU VM testing.
# Mounts essential filesystems, brings up the network interface,
# then execs BOOTy which configures FRR/EVPN networking.

# Mount essential filesystems
/bin/mount -t proc proc /proc 2>/dev/null
/bin/mount -t sysfs sysfs /sys 2>/dev/null
/bin/mount -t devtmpfs devtmpfs /dev 2>/dev/null
/bin/mount -t tmpfs tmpfs /tmp 2>/dev/null

# Widen serial console so log lines are not truncated at 80 columns
stty cols 200 2>/dev/null || true

# Load kernel modules needed by BOOTy's FRR/EVPN network stack.
# Modules are in /modules/ (flat directory), loaded via insmod in dependency order.
# virtio modules must load first so QEMU's virtio-net NIC appears as eth0.
for mod in virtio_ring virtio virtio_pci_modern_dev virtio_pci_legacy_dev \
         virtio_pci failover net_failover virtio_net \
         llc stp bridge udp_tunnel ip6_udp_tunnel dummy vxlan; do
    ko=$(find /modules -name "${mod}.ko*" 2>/dev/null | head -1)
    [ -n "$ko" ] && /bin/insmod "$ko" 2>/dev/null || true
done

# Wait for virtio NIC to appear
sleep 3

# Bring up interfaces — BOOTy's FRR manager handles IP configuration
/bin/ip link set lo up 2>/dev/null
/bin/ip link set eth0 up 2>/dev/null

# Execute BOOTy as the main init process
exec /booty
