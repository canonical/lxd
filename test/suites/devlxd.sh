#!/bin/sh

test_devlxd() {
  ensure_import_testimage

  # shellcheck disable=SC2164
  cd "${TEST_DIR}"
  go build -tags netgo -a -installsuffix devlxd ../deps/devlxd-client.go
  # shellcheck disable=SC2164
  cd -

  lxc launch testimage devlxd

  lxc file push "${TEST_DIR}/devlxd-client" devlxd/bin/

  lxc exec devlxd chmod +x /bin/devlxd-client

  lxc config set devlxd user.foo bar
  lxc exec devlxd devlxd-client user.foo | grep bar

  lxc stop devlxd --force
}
