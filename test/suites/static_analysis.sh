#!/bin/sh

safe_pot_hash() {
  sed -e "/Project-Id-Version/,/Content-Transfer-Encoding/d" -e "/^#/d" "po/lxd.pot" | tee /tmp/foo | md5sum | cut -f1 -d" "
}

test_static_analysis() {
  (
    set -e

    cd ../
    # Python3 static analysis
    pep8 scripts/lxd-images scripts/lxd-setup-lvm-storage
    pyflakes3 scripts/lxd-images scripts/lxd-setup-lvm-storage

    # Shell static analysis
    shellcheck lxd-bridge/lxd-bridge test/main.sh test/suites/* test/backends/*

    # Go static analysis
    ## Functions starting by empty line
    OUT=$(grep -r "^$" -B1 . | grep "func " | grep -v "}$" || true)
    if [ -n "${OUT}" ]; then
      echo "${OUT}"
      false
    fi

    ## go vet, if it exists
    have_go_vet=1
    go help vet > /dev/null 2>&1 || have_go_vet=0
    if [ "${have_go_vet}" -eq 1 ]; then
      go vet ./...
    fi

    ## vet
    if which vet >/dev/null 2>&1; then
      vet --all .
    fi

    ## deadcode
    if which deadcode >/dev/null 2>&1; then
      for path in . lxc/ lxd/ shared/ i18n/ fuidshift/ lxd-bridge/lxd-bridge-proxy/; do
        OUT=$(deadcode ${path} 2>&1 | grep -v lxd/migrate.pb.go || true)
        if [ -n "${OUT}" ]; then
          echo "${OUT}" >&2
          false
        fi
      done
    fi

    # Skip the tests which require git
    if ! git status; then
      return
    fi

    # go fmt
    git add -u :/
    go fmt ./...
    git diff --exit-code

    # make sure the .pot is updated
    cp --preserve "po/lxd.pot" "po/lxd.pot.bak"
    hash1=$(safe_pot_hash)
    make i18n -s
    hash2=$(safe_pot_hash)
    mv "po/lxd.pot.bak" "po/lxd.pot"
    if [ "${hash1}" != "${hash2}" ]; then
      echo "==> Please update the .pot file in your commit (make i18n)" && false
    fi
  )
}
