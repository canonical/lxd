test_remote() {
  rm -f testconf || true

  (echo foo;  sleep 3;  echo y) |  lxc remote --config ./testconf add local 127.0.0.1:8443 --debug
  lxc remote --config ./testconf list | grep 'local'

  lxc remote --config ./testconf set-default local
  [ "$(lxc remote --config ./testconf get-default)" = "local" ]

  lxc remote --config ./testconf rename local foo
  lxc remote --config ./testconf list | grep 'foo'
  lxc remote --config ./testconf list | grep -v 'local'
  [ "$(lxc remote --config ./testconf get-default)" = "foo" ]

  lxc remote --config ./testconf rm foo
  [ "$(lxc remote --config ./testconf get-default)" = "" ]

  rm -f testconf || true
}
