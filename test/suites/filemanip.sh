#!/bin/sh

test_filemanip() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage filemanip
  lxc exec filemanip -- ln -s /tmp/ /tmp/outside
  lxc file push main.sh filemanip/tmp/outside/

  [ ! -f /tmp/main.sh ]
  lxc exec filemanip -- ls /tmp/main.sh

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/containers/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  # lxc {push|pull} -r
  mkdir "${TEST_DIR}"/source
  echo "foo" > "${TEST_DIR}"/source/foo
  echo "bar" > "${TEST_DIR}"/source/bar

  lxc file push -r "${TEST_DIR}"/source filemanip/tmp

  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/source)" = "755" ]

  mkdir "${TEST_DIR}"/dest
  lxc file pull -r filemanip/tmp/source "${TEST_DIR}"/dest

  [ "$(cat "${TEST_DIR}"/dest/source/foo)" = "foo" ]
  [ "$(cat "${TEST_DIR}"/dest/source/bar)" = "bar" ]

  [ "$(stat -c "%u" "${TEST_DIR}"/dest/source)" = "$(id -u)" ]
  [ "$(stat -c "%g" "${TEST_DIR}"/dest/source)" = "$(id -g)" ]
  [ "$(stat -c "%a" "${TEST_DIR}"/dest/source)" = "755" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/tmp/this/is/a/nonexistent/directory/
  lxc file pull filemanip/tmp/this/is/a/nonexistent/directory/foo "${TEST_DIR}"
  [ "$(cat "${TEST_DIR}"/foo)" = "foo" ]

  lxc delete filemanip -f
}
