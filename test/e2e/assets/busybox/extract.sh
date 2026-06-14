#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <destination>" >&2
  exit 2
fi

dest=$1
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
busybox_image=$(awk '/^FROM / { print $2; exit }' "$script_dir/Dockerfile")
tmp=$(mktemp)
cleanup() {
  rm -f "$tmp"
}
trap cleanup EXIT

for attempt in 1 2 3; do
  if docker run --rm --platform=linux/amd64 "$busybox_image" cat /bin/busybox > "$tmp"; then
    mv "$tmp" "$dest"
    chmod +x "$dest"
    exit 0
  fi

  if [ "$attempt" != "3" ]; then
    sleep $((attempt * 5))
  fi
done

fallback=$(command -v busybox || true)
if [ -n "$fallback" ]; then
  echo "Falling back to host busybox-static at $fallback after Docker extraction failed" >&2
  cp "$fallback" "$dest"
  chmod +x "$dest"
  exit 0
fi

echo "ERROR: could not extract busybox from $busybox_image and no host busybox fallback is available" >&2
exit 1
