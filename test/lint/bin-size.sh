#!/bin/bash
set -eu
set -o pipefail

# If GOCOVERDIR is set, we are running under coverage mode and should skip this test
if [ -n "${GOCOVERDIR:-}" ]; then
    echo "Skipping binary size check under coverage mode"
    exit 0
fi

echo "Check binaries size limits"

# bin/max (sizes are in MiB)
declare -rA sizes=(
    ["lxc"]=16
    ["lxd-agent"]=14
)
readonly MIB="$((1024 * 1024))"

# Strip a copy of the freshly built binaries and check their size
d="$(mktemp -d)"
for bin in "${!sizes[@]}"; do
  max="${sizes[${bin}]}"

  install --strip ~/go/bin/"${bin}" "${d}/${bin}"
  cur="$(stat --format=%s "${d}/${bin}")"
  min=$((max - 1))
  min_mib="$((min * MIB))"
  max_mib="$((max * MIB))"
  rm -f "${d}/${bin}"

  if [ "${cur}" -gt "${max_mib}" ]; then
    echo "FAIL: ${bin} binary size exceeds ${max}MiB"
    exit 1
  fi

  # XXX: check for when we need to lower the min/max sizes
  if [ "${cur}" -lt "${min_mib}" ]; then
    echo "Congratulations: ${bin} binary size reduced below ${min}MiB"
    echo "It is now time to edit test/lint/bin-size.sh to use smaller min/max sizes for ${bin}"
    exit 1
  fi

  echo "OK: ${bin} is between ${min} and ${max}MiB"
done

rm -rf "${d}"
