#!/usr/bin/env bash

set -euo pipefail

retries="${DOCKER_BUILD_RETRIES:-3}"
delay="${DOCKER_BUILD_RETRY_DELAY:-15}"

if ! [[ "${retries}" =~ ^[1-9][0-9]*$ ]]; then
  echo "DOCKER_BUILD_RETRIES must be a positive integer, got '${retries}'" >&2
  exit 2
fi

if ! [[ "${delay}" =~ ^[0-9]+$ ]]; then
  echo "DOCKER_BUILD_RETRY_DELAY must be a non-negative integer, got '${delay}'" >&2
  exit 2
fi

for attempt in $(seq 1 "${retries}"); do
  echo "docker build attempt ${attempt}/${retries}: docker build $*"
  if docker build "$@"; then
    exit 0
  fi

  if [ "${attempt}" -eq "${retries}" ]; then
    echo "docker build failed after ${retries} attempts" >&2
    exit 1
  fi

  sleep "${delay}"
done
