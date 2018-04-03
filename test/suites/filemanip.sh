test_filemanip() {
  # Workaround for shellcheck getting confused by "cd"
  set -e
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
  chown 1000:1000 "${TEST_DIR}"/source/another_level
  echo "foo" > "${TEST_DIR}"/source/foo
  echo "bar" > "${TEST_DIR}"/source/bar
  ln -s bar "${TEST_DIR}"/source/baz

  lxc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest

  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/ptest/source)" = "755" ]
  [ "$(lxc exec filemanip -- readlink /tmp/ptest/source/baz)" = "bar" ]

  lxc exec filemanip -- rm -rf /tmp/ptest/source

  # Test pushing/pulling a file with spaces
  echo "foo" > "${TEST_DIR}/source/file with spaces"

  lxc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest
  lxc exec filemanip -- find /tmp/ptest/source | grep -q "file with spaces"
  rm -rf "${TEST_DIR}/source/file with spaces"

  lxc file pull -p -r filemanip/tmp/ptest "${TEST_DIR}/dest"
  find "${TEST_DIR}/dest/" | grep "file with spaces"
  rm -rf "${TEST_DIR}/dest"

  # Check that file permissions are not applied to intermediate directories
  lxc file push -p --mode=400 "${TEST_DIR}"/source/foo \
      filemanip/tmp/ptest/d1/d2/foo

  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/ptest/d1)" = "750" ]
  [ "$(lxc exec filemanip -- stat -c "%a" /tmp/ptest/d1/d2)" = "750" ]

  lxc exec filemanip -- rm -rf /tmp/ptest/d1

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

  lxc file push -r ./ filemanip/tmp/ptest

  [ "$(lxc exec filemanip -- stat -c "%u" /tmp/ptest/another_level)" = "1000" ]
  [ "$(lxc exec filemanip -- stat -c "%g" /tmp/ptest/another_level)" = "1000" ]

  lxc exec filemanip -- rm -rf /tmp/ptest/*

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

  lxc file push -p "${TEST_DIR}"/source/foo local:filemanip/tmp/this/is/a/nonexistent/directory/
  lxc file pull local:filemanip/tmp/this/is/a/nonexistent/directory/foo "${TEST_DIR}"
  [ "$(cat "${TEST_DIR}"/foo)" = "foo" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/.
  [ "$(lxc exec filemanip cat /foo)" = "foo" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/A/B/C/D/
  [ "$(lxc exec filemanip cat /A/B/C/D/foo)" = "foo" ]

  lxc delete filemanip -f

  if [ "$(storage_backend "$LXD_DIR")" != "lvm" ]; then
    lxc launch testimage idmap -c "raw.idmap=both 0 0"
    [ "$(stat -c %u "${LXD_DIR}/containers/idmap/rootfs")" = "0" ]
    lxc delete idmap --force
  fi
}
