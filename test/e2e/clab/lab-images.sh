#!/usr/bin/env bash
# Exports BOOTY_LAB_<STAGE>_IMAGE for every stage of labimages/Dockerfile and
# labimages/*/Dockerfile (stage frr-type5: BOOTY_LAB_FRR_TYPE5_IMAGE).
#
#   source test/e2e/clab/lab-images.sh        # export into this shell
#   test/e2e/clab/lab-images.sh CMD [ARG...]  # run CMD with them, like env(1)
#   sudo test/e2e/clab/lab-images.sh containerlab deploy --topo FILE
#
# Run under sudo, the variables need no --preserve-env. Containerlab leaves an
# unset ${VAR} in place, so with --topo/-t FILE this fails before CMD runs if
# FILE uses a ${BOOTY_LAB_*} variable without a stage.

booty_lab_images_export() {
	local dir lines line
	dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/labimages"
	# Strip CR like the Go reader, which splits on any whitespace.
	lines=$(awk '
		{ sub(/\r$/, "") }
		/^FROM / {
			# Same check as labimages.go; the digest length is checked
			# separately because not every awk supports {64}.
			digest = substr($2, index($2, "@sha256:") + 8)
			if (NF != 4 || toupper($3) != "AS" ||
				$2 !~ /^[^@]+:[^@:\/]+@sha256:[0-9a-f]+$/ || length(digest) != 64) {
				printf "%s:%d: want FROM <image>:<tag>@sha256:<digest> AS <stage>\n", FILENAME, FNR > "/dev/stderr"
				bad = 1
				next
			}
			name = toupper($4)
			gsub(/-/, "_", name)
			print "BOOTY_LAB_" name "_IMAGE=" $2
			n++
		}
		END { if (bad || n == 0) exit 1 }
	' "$dir/Dockerfile" "$dir"/*/Dockerfile) || return 1
	while IFS= read -r line; do
		export "${line?}" || return 1
	done <<<"$lines"
}

if [ "${BASH_SOURCE[0]}" != "$0" ]; then
	booty_lab_images_export
	return
fi

set -euo pipefail
if [ "$#" -eq 0 ]; then
	echo "usage: $0 CMD [ARG...]" >&2
	exit 2
fi
booty_lab_images_export
topo=""
prev=""
for arg in "$@"; do
	case "$prev" in --topo | -t) topo=$arg ;; esac
	case "$arg" in --topo=*) topo=${arg#--topo=} ;; esac
	prev=$arg
done
if [ -n "$topo" ]; then
	# shellcheck disable=SC2016 # matches a literal ${...}
	for var in $(grep -o '\${BOOTY_LAB_[A-Z0-9_]*}' "$topo" | tr -d '${}' | sort -u); do
		if [ -z "${!var:-}" ]; then
			echo "$topo uses \${$var}, but test/e2e/clab/labimages has no matching stage" >&2
			exit 1
		fi
	done
fi
exec "$@"
