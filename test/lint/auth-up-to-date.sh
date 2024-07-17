#!/bin/bash
set -eu

f="lxd/auth/entitlements_generated.go"

hash() {
  md5sum "${f}" | cut -f1 -d" "
}

echo "Checking that ${f} is up to date..."

# Make sure the generated file is up to date.
hash1="$(hash)"
mv "${f}" "${f}.bak"
make update-auth -s
hash2="$(hash)"
mv "${f}.bak" "${f}"
if [ "${hash1}" != "${hash2}" ]; then
  echo "==> Please update the ${f} file in your commit (make update-auth)"
  exit 1
fi
