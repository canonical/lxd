safe_pot_hash() {
  sed -e "/Project-Id-Version/,/Content-Transfer-Encoding/d" -e "/^#/d" "po/lxd.pot" | md5sum | cut -f1 -d" "
}

test_static_analysis() {
  (
    set -e

    cd ../
    # Python3 static analysis
    if which flake8 >/dev/null 2>&1; then
      flake8 test/deps/import-busybox
    else
      echo "flake8 not found, python static analysis disabled"
    fi

    # Shell static analysis
    if which shellcheck >/dev/null 2>&1; then
      shellcheck --shell sh test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh
    else
      echo "shellcheck not found, shell static analysis disabled"
    fi

    # Go static analysis
    sqlite="$(pwd)/lxd/sqlite"
    if [ -e "/lxc-ci/build/cache/sqlite" ]; then
        sqlite="/lxc-ci/build/cache/sqlite"
        ls "/lxc-ci/build/cache/sqlite"
    fi

    CGO_CFLAGS="-I${sqlite}"
    CGO_LDFLAGS="-L${sqlite}/.libs"
    LD_LIBRARY_PATH="${sqlite}/.libs"
    export CGO_CFLAGS
    export CGO_LDFLAGS
    export LD_LIBRARY_PATH

    ## Functions starting by empty line
    OUT=$(grep -r "^$" -B1 . | grep "func " | grep -v "}$" | grep -v "./lxd/sqlite/" || true)
    if [ -n "${OUT}" ]; then
      echo "ERROR: Functions must not start with an empty line: ${OUT}"
      false
    fi

    ## go vet, if it exists
    if go help vet >/dev/null 2>&1; then
      go vet ./...
    fi

    ## vet
    if which vet >/dev/null 2>&1; then
      vet --all .
    fi

    ## golint
    if which golint >/dev/null 2>&1; then
      golint -set_exit_status client/
      golint -set_exit_status lxc/config/
      golint -set_exit_status shared/api/
      golint -set_exit_status shared/cmd/
      golint -set_exit_status shared/gnuflag/
      golint -set_exit_status shared/i18n/
      golint -set_exit_status shared/ioprogress/
      golint -set_exit_status shared/logger/
      golint -set_exit_status shared/logging/
      golint -set_exit_status shared/termios/
      golint -set_exit_status shared/version/
      golint -set_exit_status test/deps/
    fi

    ## deadcode
    if which deadcode >/dev/null 2>&1; then
      OUT=$(deadcode ./fuidshift ./lxc ./lxd ./lxd/types ./shared ./shared/api ./shared/i18n ./shared/ioprogress ./shared/logging ./shared/osarch ./shared/simplestreams ./shared/termios ./shared/version ./lxd-benchmark 2>&1 | grep -v lxd/migrate.pb.go: | grep -v /C: | grep -vi _cgo | grep -vi _cfunc || true)
      if [ -n "${OUT}" ]; then
        echo "${OUT}" >&2
        false
      fi
    fi

    ## imports
    OUT=$(go list -f '{{ join .Imports "\n" }}' ./client ./shared/api ./lxc/config | sort -u | grep \\. | diff -u test/godeps.list - || true)
    if [ -n "${OUT}" ]; then
      echo "ERROR: you added a new dependency to the client or shared; please make sure this is what you want"
      echo "${OUT}"
      exit 1
    fi

    ## misspell
    if which misspell >/dev/null 2>&1; then
      OUT=$(misspell ./ | grep -v po/ | grep -Ev "test/includes/lxd.sh.*monitord" | grep -Ev "test/suites/static_analysis.sh.*monitord" || true)
      if [ -n "${OUT}" ]; then
        echo "Found some typos"
        echo "${OUT}"
        exit 1
      fi
    fi

    ## ineffassign
    if which ineffassign >/dev/null 2>&1; then
      ineffassign ./
    fi

    # Skip the tests which require git
    if ! git status; then
      return
    fi

    # go fmt
    git add -u :/
    gofmt -w -s ./
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
