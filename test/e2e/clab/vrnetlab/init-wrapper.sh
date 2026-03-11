#!/bin/sh
# PID 1 init wrapper for BOOTy QEMU VM testing.
# Mounts essential filesystems, configures network (simulating PXE DHCP
# assignment), then execs BOOTy which continues the provisioning lifecycle.

# Mount essential filesystems
/bin/mount -t proc proc /proc 2>/dev/null
/bin/mount -t sysfs sysfs /sys 2>/dev/null
/bin/mount -t devtmpfs devtmpfs /dev 2>/dev/null
/bin/mount -t tmpfs tmpfs /tmp 2>/dev/null

# Wait for virtio NIC to appear
sleep 3

# Configure network — IP is injected by boot.sh (sed replacement)
/bin/ip link set lo up 2>/dev/null
/bin/ip link set eth0 up 2>/dev/null
/bin/ip addr add __BOOTY_IP__/24 dev eth0 2>/dev/null

# Execute BOOTy as the main init process
exec /booty
