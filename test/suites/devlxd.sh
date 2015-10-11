#!/bin/sh

test_devlxd() {
  ensure_import_testimage

  cd "${TEST_DIR}"
  go build -tags netgo -a -installsuffix devlxd ../deps/devlxd-client.go
  cd -

  lxc launch testimage devlxd

  lxc file push "${TEST_DIR}/devlxd-client" devlxd/bin/

  lxc exec devlxd chmod +x /bin/devlxd-client

  lxc config set devlxd user.foo bar
  lxc exec devlxd devlxd-client user.foo | grep bar

  lxc stop devlxd --force
}
