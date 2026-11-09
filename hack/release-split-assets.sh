#!/bin/sh
# Stage the split build (initrd.Dockerfile, target split) as release assets
# in the current directory: every file that <out-dir>/SHA256SUMS lists is
# copied to split-<arch>-<name> with a split-<arch>-<name>.sha256, and
# split-<arch>-SHA256SUMS lists the released names, so that
# `sha256sum -c split-<arch>-SHA256SUMS` works next to the downloaded files.
# It gets a .sha256 too, which the release workflow signs like the others.
#
# usage: release-split-assets.sh <out-dir> <arch>
set -eu
export LC_ALL=C

out=${1:?usage: release-split-assets.sh <out-dir> <arch>}
arch=${2:?usage: release-split-assets.sh <out-dir> <arch>}
prefix="split-${arch}"

# The build's SHA256SUMS pins the whole set.
(cd "$out" && sha256sum -c --quiet SHA256SUMS)

: > "${prefix}-SHA256SUMS"
while read -r _ name; do
	name=${name#\*}
	cp "$out/$name" "${prefix}-${name}"
	sha256sum "${prefix}-${name}" > "${prefix}-${name}.sha256"
	cat "${prefix}-${name}.sha256" >> "${prefix}-SHA256SUMS"
done < "$out/SHA256SUMS"
[ -s "${prefix}-SHA256SUMS" ] || { echo "release-split-assets: $out/SHA256SUMS lists no files" >&2; exit 1; }

# Prove the manifest checks against the released names.
sha256sum -c --quiet "${prefix}-SHA256SUMS"
sha256sum "${prefix}-SHA256SUMS" > "${prefix}-SHA256SUMS.sha256"
