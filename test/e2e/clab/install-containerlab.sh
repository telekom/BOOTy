#!/usr/bin/env bash
set -euo pipefail

version="${1:-${CONTAINERLAB_VERSION:-0.75.0}}"
version="${version#v}"
tag="v${version}"

arch="$(dpkg --print-architecture)"
case "${arch}" in
  amd64|arm64) ;;
  *)
    echo "unsupported architecture for containerlab: ${arch}" >&2
    exit 1
    ;;
esac

asset="containerlab_${version}_linux_${arch}.deb"
base_url="https://github.com/srl-labs/containerlab/releases/download/${tag}"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

curl_common=(
  --fail
  --location
  --show-error
  --silent
  --retry 8
  --retry-all-errors
  --retry-delay 10
  --retry-max-time 300
  --connect-timeout 30
  --max-time 120
)

curl "${curl_common[@]}" "${base_url}/checksums.txt" --output "${tmp_dir}/checksums.txt"
curl "${curl_common[@]}" "${base_url}/${asset}" --output "${tmp_dir}/${asset}"

grep " ${asset}$" "${tmp_dir}/checksums.txt" > "${tmp_dir}/${asset}.sha256"
cd "${tmp_dir}"
sha256sum --check "${asset}.sha256"

sudo dpkg --install "${asset}"
containerlab version
