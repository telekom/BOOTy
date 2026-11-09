#!/usr/bin/env bash
# Build initramfs targets twice, each round in a new buildx builder with an
# empty cache, and fail if any output file differs between the rounds
# (README.md, Reproducible builds).
#
# usage: check-reproducible.sh <platform> <target>...
#   target: a stage of initrd.Dockerfile, or "default" for the default target
#   OUT_DIR: where the outputs of both rounds go (default: repro-out)
set -euo pipefail

platform=${1:?usage: check-reproducible.sh <platform> <target>...}
shift
[ $# -gt 0 ] || { echo "usage: check-reproducible.sh <platform> <target>..." >&2; exit 2; }
out=${OUT_DIR:-repro-out}
epoch=$(git log -1 --format=%ct)
commit=$(git rev-parse HEAD)
commit_time=$(git log -1 --format=%cI)

builder=booty-repro-$$
trap 'docker buildx rm "$builder-1" "$builder-2" >/dev/null 2>&1 || true' EXIT

rm -rf "$out"
for round in 1 2; do
	docker buildx create --name "$builder-$round" --driver docker-container >/dev/null
	for target in "$@"; do
		target_arg=()
		[ "$target" = default ] || target_arg=(--target "$target")
		docker buildx build --builder "$builder-$round" --platform "$platform" \
			"${target_arg[@]}" \
			--build-arg "SOURCE_DATE_EPOCH=$epoch" \
			--build-arg "BOOTY_VERSION=repro" \
			--build-arg "BOOTY_BUILD=$commit" \
			--build-arg "BOOTY_COMMIT_TIME=$commit_time" \
			--output "type=local,dest=$out/$round/$target" \
			-f initrd.Dockerfile .
	done
	docker buildx rm "$builder-$round" >/dev/null
done

(cd "$out/1" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum) > "$out/1.sha256"
(cd "$out/2" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum) > "$out/2.sha256"
cat "$out/1.sha256"
if diff -u "$out/1.sha256" "$out/2.sha256"; then
	echo "reproducible: both rounds built the same bytes"
	exit 0
fi

# Show which ramdisk entries differ. Device nodes need root to extract and
# carry no content, so their errors are ignored.
for f in $(diff "$out/1.sha256" "$out/2.sha256" | awk '/^[<>]/ {print $3}' | sort -u); do
	case "$f" in
	*.cpio.zst) cat=(zstd -dc) ;;
	*.cpio.gz) cat=(gzip -dc) ;;
	*) continue ;;
	esac
	echo "::group::differing entries of $f"
	for round in 1 2; do
		mkdir -p "$out/x$round/$f"
		"${cat[@]}" "$out/$round/$f" | (cd "$out/x$round/$f" && cpio -id --quiet --no-absolute-filenames 2>/dev/null) || true
	done
	diff -rq --no-dereference "$out/x1/$f" "$out/x2/$f" || true
	echo "::endgroup::"
done
echo "::error::the two builds differ; compare $out/1 and $out/2 with diffoscope"
exit 1
