#!/bin/sh

test_filemanip() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  echo "test" > "${TEST_DIR}"/filemanip

  lxc launch testimage filemanip
  lxc exec filemanip -- ln -s /tmp/ /tmp/outside
  lxc file push "${TEST_DIR}"/filemanip filemanip/tmp/outside/

  [ ! -f /tmp/filemanip ]
  lxc exec filemanip -- ls /tmp/filemanip

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/containers/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  # lxc {push|pull} -r
  mkdir "${TEST_DIR}"/source
  mkdir "${TEST_DIR}"/source/another_level
  echo "foo" > "${TEST_DIR}"/source/foo
  echo "bar" > "${TEST_DIR}"/source/bar

  lxc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest

  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  lxc exec filemanip -- rm -rf /tmp/ptest/source

  # Special case where we are in the same directory as the one we are currently
  # created.
  oldcwd=$(pwd)
  cd "${TEST_DIR}"

  lxc file push -r source filemanip/tmp/ptest

  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  lxc exec filemanip -- rm -rf /tmp/ptest/source

  # Special case where we are in the same directory as the one we are currently
  # created.
  cd source

  lxc file push -r ../source filemanip/tmp/ptest

  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  # Switch back to old working directory.
  cd "${oldcwd}"

  mkdir "${TEST_DIR}"/dest
  lxc file pull -r filemanip/tmp/ptest/source "${TEST_DIR}"/dest

  [ "$(cat "${TEST_DIR}"/dest/source/foo)" = "foo" ]
  [ "$(cat "${TEST_DIR}"/dest/source/bar)" = "bar" ]

  [ "$(stat -c "%u" "${TEST_DIR}"/dest/source)" = "$(id -u)" ]
  [ "$(stat -c "%g" "${TEST_DIR}"/dest/source)" = "$(id -g)" ]
  [ "$(stat -c "%a" "${TEST_DIR}"/dest/source)" = "755" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/tmp/this/is/a/nonexistent/directory/
  lxc file pull filemanip/tmp/this/is/a/nonexistent/directory/foo "${TEST_DIR}"
  [ "$(cat "${TEST_DIR}"/foo)" = "foo" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/.
  [ "$(lxc exec filemanip cat /foo)" = "foo" ]

  lxc delete filemanip -f

  if [ "${LXD_BACKEND}" != "lvm" ]; then
    lxc launch testimage idmap -c "raw.idmap=\"both 0 0\""
    [ "$(stat -c %u "${LXD_DIR}/containers/idmap/rootfs")" = "0" ]
    lxc delete idmap --force
  fi
}
