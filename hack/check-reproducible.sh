#!/usr/bin/env bash
# Build initramfs targets twice, each round in a new buildx builder with an
# empty cache, and fail if any output file differs between the rounds
# (README.md, Reproducible builds).
#
# Round 1 builds the checkout. Round 2 builds a fresh clone of HEAD in
# another directory, with umask 002 and TZ=Asia/Kolkata, so a build that
# depends on the checkout path, the file modes of the checkout or the time
# zone of the host fails the check. The build containers never see the
# host time zone (their images set none), so TZ covers the host side: git,
# the buildx client and this script. Both rounds pass the same build arguments, with
# BOOTY_FLAVOR per target like release-v2.yml.
#
# usage: check-reproducible.sh <platform> <target>...
#   target: a stage of initrd.Dockerfile, or "default" for the default target
#   OUT_DIR: where the outputs of both rounds go (default: repro-out)
set -euo pipefail

platform=${1:?usage: check-reproducible.sh <platform> <target>...}
shift
[ $# -gt 0 ] || { echo "usage: check-reproducible.sh <platform> <target>..." >&2; exit 2; }
# Round 2 builds HEAD, so round 1 must build it too.
if [ -n "$(git status --porcelain)" ]; then
	echo "check-reproducible: commit or stash the changes first; round 2 builds HEAD" >&2
	exit 2
fi
out=${OUT_DIR:-repro-out}
rm -rf "$out"
mkdir -p "$out"
out=$(cd "$out" && pwd)
epoch=$(git log -1 --format=%ct)
commit=$(git rev-parse HEAD)
commit_time=$(git log -1 --format=%cI)

builder=booty-repro-$$
clone=$(mktemp -d)
trap 'docker buildx rm "$builder-1" "$builder-2" >/dev/null 2>&1 || true; rm -rf "$clone"' EXIT
src1=$PWD
src2=$clone/BOOTy

# build <round> <target>...: build the targets from the source of the round
# with the builder of the round.
build() {
	local round=$1 target flavor target_arg src
	shift
	src=src$round
	src=${!src}
	for target in "$@"; do
		target_arg=()
		[ "$target" = default ] || target_arg=(--target "$target")
		# The flavor of the init binary, like release-v2.yml.
		case "$target" in
		default | split) flavor=full ;;
		*) flavor=$target ;;
		esac
		docker buildx build --builder "$builder-$round" --platform "$platform" \
			${target_arg[@]+"${target_arg[@]}"} \
			--build-arg "SOURCE_DATE_EPOCH=$epoch" \
			--build-arg "BOOTY_VERSION=repro" \
			--build-arg "BOOTY_BUILD=$commit" \
			--build-arg "BOOTY_COMMIT_TIME=$commit_time" \
			--build-arg "BOOTY_FLAVOR=$flavor" \
			--output "type=local,dest=$out/$round/$target" \
			-f "$src/initrd.Dockerfile" "$src"
	done
}

docker buildx create --name "$builder-1" --driver docker-container >/dev/null
build 1 "$@"
(
	umask 002
	export TZ=Asia/Kolkata
	git clone --quiet --no-checkout "$src1" "$src2"
	git -C "$src2" checkout --quiet --detach "$commit"
	docker buildx create --name "$builder-2" --driver docker-container >/dev/null
	build 2 "$@"
)

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

# Show the Debian and Alpine package versions that differ between the
# rounds: a mirror may have published a new version in between. Each
# builder still has the stages of its round in its cache; a stage that no
# checked target needs is built now.
echo "::group::package versions that differ between the rounds (stage package version)"
for round in 1 2; do
	if ! build "$round" package-versions >/dev/null 2>&1; then
		echo "check-reproducible: listing the packages of round $round failed" >&2
	fi
	(cd "$out/$round/package-versions" 2>/dev/null && for db in */*; do
		awk -v db="${db#*/}" '
			/^(Package|P):/ { sub(/^[^:]*: ?/, ""); p = $0 }
			/^(Version|V):/ { sub(/^[^:]*: ?/, ""); print db, p, $0 }' "$db"
	done) | LC_ALL=C sort > "$out/packages$round.txt" || true
done
diff -u "$out/packages1.txt" "$out/packages2.txt" || true
echo "::endgroup::"
echo "::error::the two builds differ; compare $out/1 and $out/2 with diffoscope"
exit 1
