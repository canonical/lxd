#!/bin/sh

safe_pot_hash() {
  grep ^msgid "${toplevel}/po/lxd.pot" | md5sum | cut -f1 -d" "
}

test_static_analysis() {
  # Python3 static analysis
  pep8 ../scripts/lxd-images ../scripts/lxd-setup-lvm-storage
  pyflakes3 ../scripts/lxd-images ../scripts/lxd-setup-lvm-storage

  # Shell static analysis
  shellcheck ../lxd-bridge/lxd-bridge ../test/main.sh ../test/suites/*

  # Skip the test when not running from a git repository
  if ! git status; then
    return
  fi

  toplevel="$(git rev-parse --show-toplevel)"

  # go tools
  git add -u :/
  (cd "${toplevel}" && go fmt ./... && go vet ./...)
  git diff --exit-code

  # make sure the .pot is updated
  cp "${toplevel}/po/lxd.pot" "${toplevel}/po/lxd.pot.bak"
  hash1=$(safe_pot_hash)
  (cd "${toplevel}" && make i18n -s)
  hash2=$(safe_pot_hash)
  mv "${toplevel}/po/lxd.pot.bak" "${toplevel}/po/lxd.pot"
  if [ "${hash1}" != "${hash2}" ]; then
    echo "==> Please update the .pot file in your commit (make i18n)" && false
  fi
}
