#!/bin/sh -eu

safe_pot_hash() {
  sed -e "/Project-Id-Version/,/Content-Transfer-Encoding/d" -e "/^#/d" "po/lxd.pot" | md5sum | cut -f1 -d" "
}

echo "Checking that the .pot files are up to date..."

# make sure the .pot is updated
cp --preserve "po/lxd.pot" "po/lxd.pot.bak"
hash1=$(safe_pot_hash)
make i18n -s
hash2=$(safe_pot_hash)
mv "po/lxd.pot.bak" "po/lxd.pot"
if [ "${hash1}" != "${hash2}" ]; then
  echo "==> Please update the .pot file in your commit (make i18n)"
  exit 1
fi
