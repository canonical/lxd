test_devlxd() {
  if [ -n "${TRAVIS_PULL_REQUEST:-}" ]; then
    return
  fi

  ensure_import_testimage

  cd deps
  go build -tags netgo -a -installsuffix devlxd devlxd-client.go
  cd ..

  lxc launch testimage devlxd

  lxc file push deps/devlxd-client devlxd/bin/
  lxc exec devlxd chmod +x /bin/devlxd-client

  lxc config set devlxd user.foo bar
  lxc exec devlxd devlxd-client user.foo | grep bar

  lxc stop devlxd --force
}
