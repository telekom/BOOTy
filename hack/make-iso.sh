#!/bin/sh
# Pack a BOOTy ISO that boots on BIOS (ISOLINUX) and UEFI (shim + GRUB),
# from virtual media, CD or USB (isohybrid MBR and GPT).
#
# usage: make-iso.sh <iso-root> <efiboot.img> <out.iso> [volid]
#   iso-root must contain boot/vmlinuz and boot/initrd.img.
#   efiboot.img comes from make-efiboot.sh, built with a label list that
#   contains volid (default BOOTY).
# Needs: xorriso, isolinux, syslinux-common.
set -eu
root=${1:?usage: make-iso.sh <iso-root> <efiboot.img> <out.iso> [volid]}
efiboot=${2:?usage: make-iso.sh <iso-root> <efiboot.img> <out.iso> [volid]}
out=${3:?usage: make-iso.sh <iso-root> <efiboot.img> <out.iso> [volid]}
volid=${4:-BOOTY}
cmdline="console=tty0 console=ttyS0,115200n8"

for f in boot/vmlinuz boot/initrd.img; do
	[ -s "$root/$f" ] || { echo "make-iso: $root/$f is missing" >&2; exit 1; }
done

mkdir -p "$root/isolinux" "$root/boot/grub"
cp /usr/lib/ISOLINUX/isolinux.bin /usr/lib/syslinux/modules/bios/ldlinux.c32 "$root/isolinux/"
cp "$efiboot" "$root/boot/efiboot.img"

# SERIAL makes ISOLINUX usable on headless machines (serial console only).
printf 'SERIAL 0 115200\nDEFAULT booty\nLABEL booty\n  KERNEL /boot/vmlinuz\n  APPEND initrd=/boot/initrd.img %s\n' \
	"$cmdline" > "$root/isolinux/isolinux.cfg"

cat > "$root/boot/grub/grub.cfg" <<GRUBCFG
set timeout=1
set default=0
serial --unit=0 --speed=115200
terminal_input console serial
terminal_output console serial
menuentry "BOOTy" {
  linux /boot/vmlinuz $cmdline
  initrd /boot/initrd.img
}
GRUBCFG

# Booted from the El Torito image, Debian's GRUB sets root to the CD and
# looks for its config at /EFI/debian/grub.cfg on the ISO filesystem, not in
# the ESP image (booted from USB it uses the ESP copy instead).
mkdir -p "$root/EFI/debian"
printf 'search --no-floppy --set=root --label "%s"\nset prefix=($root)/boot/grub\nconfigfile ($root)/boot/grub/grub.cfg\n' \
	"$volid" > "$root/EFI/debian/grub.cfg"

xorriso -as mkisofs \
	-o "$out" \
	-V "$volid" \
	-r -J \
	-isohybrid-mbr /usr/lib/ISOLINUX/isohdpfx.bin \
	-b isolinux/isolinux.bin \
	-c isolinux/boot.cat \
	-no-emul-boot -boot-load-size 4 -boot-info-table \
	-eltorito-alt-boot \
	-e boot/efiboot.img \
	-no-emul-boot \
	-isohybrid-gpt-basdat \
	"$root"
