#!/bin/sh
# Builds mok-enroll.efi (x86_64) with gnu-efi:
#   apt-get install gcc binutils gnu-efi && hack/mok-enroll/build.sh out.efi
set -eu
src="$(dirname "$0")/mok-enroll.c"
out="${1:-mok-enroll.efi}"
find_one() { find /usr/lib /usr/lib64 -name "$1" 2>/dev/null | head -n1; }
lds="$(find_one elf_x86_64_efi.lds)"
crt0="$(find_one crt0-efi-x86_64.o)"
libdir="$(dirname "$(find_one libgnuefi.a)")"
[ -n "$lds" ] && [ -n "$crt0" ] || { echo "gnu-efi not found" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
gcc -I/usr/include/efi -I/usr/include/efi/x86_64 -fpic -fshort-wchar -ffreestanding \
	-fno-stack-protector -fno-stack-check -fno-strict-aliasing -mno-red-zone \
	-maccumulate-outgoing-args -DGNU_EFI_USE_MS_ABI -Wall -Werror -O2 \
	-c "$src" -o "$tmp/mok-enroll.o"
ld -nostdlib -znocombreloc -shared -Bsymbolic -T "$lds" -L "$libdir" \
	"$crt0" "$tmp/mok-enroll.o" -o "$tmp/mok-enroll.so" -lefi -lgnuefi
objcopy -j .text -j .sdata -j .data -j .rodata -j .dynamic -j .dynsym -j .rel \
	-j .rela -j '.rel.*' -j '.rela.*' -j .reloc -O pei-x86-64 \
	--subsystem=10 "$tmp/mok-enroll.so" "$out"
