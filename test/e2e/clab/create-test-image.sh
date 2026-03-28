#!/usr/bin/env bash
# create-test-image.sh — Generate a minimal GPT disk image for E2E boot tests.
#
# Creates a ~256 MiB raw disk image with:
#   Partition 1: 64 MiB  EFI System Partition (FAT32)
#   Partition 2: ~180 MiB Linux root filesystem (ext4)
#
# The root partition includes a minimal directory skeleton so BOOTy's
# provisioning pipeline can proceed through mount, chroot, and grow steps.
# Provisioning will eventually fail at configure-kubelet (no real OS),
# giving a deterministic and meaningful E2E failure point.
#
# Usage:
#   ./create-test-image.sh [output-dir]
#   Default output: test/e2e/clab/images/
#
# Requires: dd, sgdisk (gdisk), losetup, mkfs.vfat, mkfs.ext4, mount (root)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_DIR="${1:-${SCRIPT_DIR}/images}"
IMAGE_RAW="${OUTPUT_DIR}/test.img"
IMAGE_GZ="${OUTPUT_DIR}/test.img.gz"
IMAGE_SIZE_MB=256

mkdir -p "${OUTPUT_DIR}"

echo "==> Creating ${IMAGE_SIZE_MB} MiB raw disk image"
dd if=/dev/zero of="${IMAGE_RAW}" bs=1M count="${IMAGE_SIZE_MB}" status=progress 2>&1

echo "==> Creating GPT partition table"
sgdisk --zap-all "${IMAGE_RAW}"
# Partition 1: EFI System Partition, 64 MiB
sgdisk --new=1:2048:+64M --typecode=1:EF00 --change-name=1:EFI "${IMAGE_RAW}"
# Partition 2: Linux filesystem, remaining space
sgdisk --new=2:0:0 --typecode=2:8300 --change-name=2:root "${IMAGE_RAW}"
sgdisk --print "${IMAGE_RAW}"

echo "==> Setting up loop device with partition scanning"
LOOP_DEV=$(losetup --find --show --partscan "${IMAGE_RAW}")
echo "    Loop device: ${LOOP_DEV}"

# Wait for partition devices to appear.
for i in $(seq 1 10); do
    if [ -b "${LOOP_DEV}p2" ]; then
        break
    fi
    partprobe "${LOOP_DEV}" 2>/dev/null || true
    sleep 0.5
done

if [ ! -b "${LOOP_DEV}p1" ] || [ ! -b "${LOOP_DEV}p2" ]; then
    echo "ERROR: partition devices not found (${LOOP_DEV}p1, ${LOOP_DEV}p2)"
    losetup -d "${LOOP_DEV}"
    exit 1
fi

echo "==> Formatting EFI partition (FAT32)"
mkfs.vfat -F 32 -n EFI "${LOOP_DEV}p1"

echo "==> Formatting root partition (ext4)"
mkfs.ext4 -L root -q "${LOOP_DEV}p2"

echo "==> Populating root filesystem skeleton"
MOUNT_DIR=$(mktemp -d)
mount "${LOOP_DEV}p2" "${MOUNT_DIR}"

# Minimal directory structure expected by BOOTy provisioning steps.
mkdir -p "${MOUNT_DIR}/boot/grub"
mkdir -p "${MOUNT_DIR}/boot/efi"
mkdir -p "${MOUNT_DIR}/etc/kubernetes"
mkdir -p "${MOUNT_DIR}/etc/default"
mkdir -p "${MOUNT_DIR}/usr/bin"
mkdir -p "${MOUNT_DIR}/var/log"

# Create a minimal grub.cfg so configure-grub doesn't fail.
cat > "${MOUNT_DIR}/boot/grub/grub.cfg" <<'GRUB'
set default=0
set timeout=5
menuentry "Linux" {
    linux /vmlinuz root=LABEL=root ro
    initrd /initrd.img
}
GRUB

# Mount and populate EFI partition.
mount "${LOOP_DEV}p1" "${MOUNT_DIR}/boot/efi"
mkdir -p "${MOUNT_DIR}/boot/efi/EFI/BOOT"
echo "# EFI stub" > "${MOUNT_DIR}/boot/efi/EFI/BOOT/BOOTX64.CSV"
umount "${MOUNT_DIR}/boot/efi"

umount "${MOUNT_DIR}"
rmdir "${MOUNT_DIR}"

echo "==> Detaching loop device"
losetup -d "${LOOP_DEV}"

echo "==> Compressing image with gzip"
gzip -f -k "${IMAGE_RAW}"
ls -lh "${IMAGE_RAW}" "${IMAGE_GZ}"

echo "==> Test image ready: ${IMAGE_GZ}"
echo "    Raw: $(du -h "${IMAGE_RAW}" | cut -f1)"
echo "    Gzip: $(du -h "${IMAGE_GZ}" | cut -f1)"
