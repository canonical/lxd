#!/bin/sh -eu

f="lxd/gendocs/docs.yaml"

gendocs_hash() {
  md5sum "${f}" | cut -f1 -d" "
}

echo "Checking that ${f} is up to date..."

# make sure the YAML is up to date
hash1="$(gendocs_hash)"
mv "${f}" "${f}.bak"
make update-gendocs -s
hash2="$(gendocs_hash)"
mv "${f}.bak" "${f}"
if [ "${hash1}" != "${hash2}" ]; then
  echo "==> Please update the ${f} file in your commit (make update-gendocs)"
  exit 1
fi
