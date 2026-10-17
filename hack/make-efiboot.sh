#!/bin/sh
# Build the UEFI El Torito image (a FAT ESP) for a BOOTy ISO.
#
# The image boots Debian's Secure Boot chain: shim (signed by the Microsoft
# UEFI CA) loads GRUB (signed by Debian), and GRUB verifies the kernel
# through shim. Debian's signed GRUB cannot carry a custom embedded config,
# so the ESP holds a small grub.cfg that finds the ISO by volume label and
# loads /boot/grub/grub.cfg from it.
#
# usage: make-efiboot.sh <out.img> <label>...
#   label: ISO volume labels to search, in order (for example BOOTY, or
#          caas-deploy-image for ISOs that CAPRF renders).
# Needs: shim-signed, shim-helpers-amd64-signed, grub-efi-amd64-signed,
# dosfstools, mtools.
set -eu
out=${1:?usage: make-efiboot.sh <out.img> <label>...}
shift
[ "$#" -gt 0 ] || { echo "make-efiboot: at least one ISO label is required" >&2; exit 2; }

work=$(mktemp -d)
trap 'rm -r "$work"' EXIT

{
	printf 'for label in'
	printf ' "%s"' "$@"
	printf '; do\n'
	printf '  if search --no-floppy --set=root --label "$label"; then\n'
	printf '    set prefix=($root)/boot/grub\n'
	printf '    configfile ($root)/boot/grub/grub.cfg\n'
	printf '  fi\n'
	printf 'done\n'
	printf 'echo "BOOTy: no ISO volume with one of the labels:'
	printf ' %s' "$@"
	printf '"\n'
	printf 'sleep 30\n'
} > "$work/grub.cfg"

rm -f "$out"
shim=/usr/lib/shim/shimx64.efi.signed
mm=/usr/lib/shim/mmx64.efi.signed
grub=/usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed
# Size the FAT image to its content plus 1 MiB for FAT metadata and slack.
kib=$(du -kc "$shim" "$mm" "$grub" | tail -n 1 | cut -f 1)
mkfs.vfat -C -n BOOTY_ESP "$out" $((kib + 1024)) >/dev/null
mmd -i "$out" ::/EFI ::/EFI/BOOT ::/EFI/debian
mcopy -i "$out" "$shim" ::/EFI/BOOT/BOOTX64.EFI
mcopy -i "$out" "$mm" ::/EFI/BOOT/mmx64.efi
mcopy -i "$out" "$grub" ::/EFI/BOOT/grubx64.efi
# Debian's monolithic GRUB reads grub.cfg next to itself ($cmdpath) or
# from its built-in prefix /EFI/debian; put it in both places.
mcopy -i "$out" "$work/grub.cfg" ::/EFI/BOOT/grub.cfg
mcopy -i "$out" "$work/grub.cfg" ::/EFI/debian/grub.cfg
