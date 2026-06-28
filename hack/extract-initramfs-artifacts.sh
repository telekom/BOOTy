#!/usr/bin/env bash
set -euo pipefail

src="${1:-release-artifacts}"
dest="${2:-scan-rootfs}"

fail() {
  echo "::error::$*"
  exit 1
}

validate_inputs() {
  if [ ! -d "${src}" ]; then
    fail "artifact source directory does not exist: ${src}"
  fi

  case "${dest}" in
    "" | "/" | "." | ".." | /* | ../* | */.. | */../* | */.)
      fail "refusing unsafe extraction destination: ${dest}"
      ;;
  esac
}

cpio_supports() {
  cpio --help 2>&1 | grep -q -- "$1"
}

list_cpio() {
  if cpio_supports '--quiet'; then
    cpio -it --quiet
  else
    cpio -it
  fi
}

extract_cpio() {
  if cpio_supports '--no-absolute-filenames'; then
    cpio -idmu --quiet --no-absolute-filenames
  else
    cpio -idmu
  fi
}

stream_artifact() {
  local artifact="$1"
  case "${artifact}" in
    *.cpio.gz)
      gzip -dc "${artifact}"
      ;;
    *.cpio.zst)
      zstd -dc "${artifact}"
      ;;
    *)
      fail "unsupported initramfs artifact ${artifact}"
      ;;
  esac
}

validate_cpio_entries() {
  local artifact="$1"
  local entry clean

  while IFS= read -r entry; do
    clean="${entry}"
    while [[ "${clean}" == ./* ]]; do
      clean="${clean#./}"
    done
    case "${clean}" in
      "" | ".")
        ;;
      /* | ".." | ../* | */.. | */../*)
        echo "::error file=${artifact}::unsafe cpio entry: ${entry}"
        return 1
        ;;
    esac
  done
}

validate_inputs
rm -rf -- "${dest}"
mkdir -p -- "${dest}"

count=0
while IFS= read -r -d '' artifact; do
  base="$(basename "${artifact}")"
  name="${base%.cpio.gz}"
  name="${name%.cpio.zst}"
  out="${dest}/${name}"
  mkdir -p -- "${out}"

  stream_artifact "${artifact}" | list_cpio | validate_cpio_entries "${artifact}"
  stream_artifact "${artifact}" | (cd "${out}" && extract_cpio)
  count=$((count + 1))
done < <(find "${src}" -type f \( -name '*initramfs.cpio.gz' -o -name '*initramfs.cpio.zst' \) -print0)

if [ "${count}" -eq 0 ]; then
  echo "::error::no initramfs artifacts found under ${src}"
  find "${src}" -maxdepth 4 -type f -print || true
  exit 1
fi

echo "Extracted ${count} initramfs artifact(s) into ${dest}"
