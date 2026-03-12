#!/bin/sh
# PID 1 init wrapper for BOOTy QEMU VM testing.
# Mounts essential filesystems, brings up the network interface,
# then execs BOOTy which configures FRR/EVPN networking.

# Mount essential filesystems
/bin/mount -t proc proc /proc 2>/dev/null
/bin/mount -t sysfs sysfs /sys 2>/dev/null
/bin/mount -t devtmpfs devtmpfs /dev 2>/dev/null
/bin/mount -t tmpfs tmpfs /tmp 2>/dev/null

# Load kernel modules needed by BOOTy's FRR/EVPN network stack
/bin/busybox depmod 2>/dev/null || true
/bin/modprobe dummy 2>/dev/null || true
/bin/modprobe vxlan 2>/dev/null || true

# Wait for virtio NIC to appear
sleep 3

# Bring up interfaces — BOOTy's FRR manager handles IP configuration
/bin/ip link set lo up 2>/dev/null
/bin/ip link set eth0 up 2>/dev/null

# Execute BOOTy as the main init process
exec /booty
