#!/bin/sh
# Builds mok-enroll.efi with gnu-efi for the build machine's architecture
# (x86_64 or aarch64). Firmware does not enable SSE/AVX or FP/SIMD state,
# so the code uses general registers only (some distro gcc default to
# x86-64-v3):
#   apt-get install gcc binutils gnu-efi && hack/mok-enroll/build.sh out.efi
# With SOURCE_DATE_EPOCH set (initrd.Dockerfile), binutils stamps the PE
# header with it, so the output is reproducible.
set -eu
src="$(dirname "$0")/mok-enroll.c"
out="${1:-mok-enroll.efi}"
arch="$(uname -m)"
case "$arch" in
x86_64)
	cflags="-mno-red-zone -maccumulate-outgoing-args -march=x86-64 -DGNU_EFI_USE_MS_ABI"
	format="-O pei-x86-64 --subsystem=10"
	;;
aarch64)
	cflags="-mstrict-align"
	format="--target=efi-app-aarch64"
	;;
*)
	echo "unsupported architecture $arch" >&2
	exit 1
	;;
esac
find_one() { find /usr/lib /usr/lib64 -name "$1" 2>/dev/null | head -n1; }
lds="$(find_one "elf_${arch}_efi.lds")"
crt0="$(find_one "crt0-efi-${arch}.o")"
libdir="$(dirname "$(find_one libgnuefi.a)")"
[ -n "$lds" ] && [ -n "$crt0" ] || { echo "gnu-efi not found" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# shellcheck disable=SC2086 # cflags and format are word lists
gcc -I/usr/include/efi -I"/usr/include/efi/$arch" -fpic -fshort-wchar -ffreestanding \
	-fno-stack-protector -fno-stack-check -fno-strict-aliasing -mgeneral-regs-only $cflags \
	-Wall -Werror -O2 -c "$src" -o "$tmp/mok-enroll.o"
ld -nostdlib -znocombreloc -shared -Bsymbolic -T "$lds" -L "$libdir" \
	"$crt0" "$tmp/mok-enroll.o" -o "$tmp/mok-enroll.so" -lefi -lgnuefi
# shellcheck disable=SC2086
objcopy -j .text -j .sdata -j .data -j .rodata -j .dynamic -j .dynsym -j .rel \
	-j .rela -j '.rel.*' -j '.rela.*' -j .reloc $format "$tmp/mok-enroll.so" "$out"
objdump -p "$out" | grep -q 'Subsystem.*EFI application' || {
	echo "$out is not an EFI application" >&2
	exit 1
}
# VEX (AVX) instructions fault with #UD before the OS enables XSAVE.
if [ "$arch" = x86_64 ] && objdump -d "$out" | grep -qE '\sv[a-z0-9]+\s+.*%[xyz]mm'; then
	echo "$out contains AVX instructions" >&2
	exit 1
fi
