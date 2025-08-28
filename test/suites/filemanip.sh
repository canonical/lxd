test_filemanip() {
  # Workaround for shellcheck getting confused by "cd"
  set -e
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  echo "test" > "${TEST_DIR}"/filemanip

  lxc project create test -c features.profiles=false -c features.images=false -c features.storage.volumes=false
  lxc project switch test
  lxc launch testimage filemanip
  lxc exec filemanip --project=test -- ln -s /tmp/ /tmp/outside
  lxc file push "${TEST_DIR}"/filemanip filemanip/tmp/outside/

  [ ! -f /tmp/filemanip ]
  lxc exec filemanip --project=test -- ls /tmp/filemanip

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/containers/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  myuid=1522341878
  chown "${myuid}" "${TEST_DIR}/filemanip"
  lxc --project=test file push "${TEST_DIR}"/filemanip filemanip/root/
  [ "$(lxc exec filemanip --project=test -- cat /root/filemanip)" = "test" ]
  lxc --project=test file push -p "${TEST_DIR}"/filemanip filemanip/root/temp/
  [ "$(lxc exec filemanip --project=test -- cat /root/temp/filemanip)" = "test" ]
  chown root "${TEST_DIR}/filemanip"

  # lxc {push|pull} -r
  mkdir "${TEST_DIR}"/source
  mkdir "${TEST_DIR}"/source/another_level
  chown 1000:1000 "${TEST_DIR}"/source/another_level
  echo "foo" > "${TEST_DIR}"/source/foo
  echo "bar" > "${TEST_DIR}"/source/bar
  ln -s bar "${TEST_DIR}"/source/baz

  lxc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest

  [ "$(lxc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
  [ "$(lxc exec filemanip --project=test -- readlink /tmp/ptest/source/baz)" = "bar" ]

  lxc exec filemanip --project=test -- rm -rf /tmp/ptest/source

  # Test explicitly specifying permissions on push for files that already exist
  lxc file push "${TEST_DIR}"/source/foo filemanip/tmp/foo
  lxc file push --mode=664 --uid=1202 --gid=1203 "${TEST_DIR}"/source/foo filemanip/tmp/foo

  [ "$(lxc exec filemanip --project=test -- stat -c "%u" /tmp/foo)" = "1202" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%g" /tmp/foo)" = "1203" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%a" /tmp/foo)" = "664" ]

  lxc exec filemanip --project=test -- rm /tmp/foo

  # Test pushing/pulling a file with spaces
  echo "foo" > "${TEST_DIR}/source/file with spaces"

  lxc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest
  lxc exec filemanip --project=test -- find /tmp/ptest/source | grep -F "file with spaces"
  rm -rf "${TEST_DIR}/source/file with spaces"

  lxc file pull -p -r filemanip/tmp/ptest "${TEST_DIR}/dest"
  find "${TEST_DIR}/dest/" | grep "file with spaces"
  rm -rf "${TEST_DIR}/dest"

  # Check that file permissions are not applied to intermediate directories
  lxc file push -p --mode=400 "${TEST_DIR}"/source/foo \
      filemanip/tmp/ptest/d1/d2/foo

  [ "$(lxc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1)" = "750" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1/d2)" = "750" ]

  lxc exec filemanip --project=test -- rm -rf /tmp/ptest/d1

  # Special case where we are in the same directory as the one we are currently
  # created.
  oldcwd=$(pwd)
  cd "${TEST_DIR}"

  lxc file push -r source filemanip/tmp/ptest

  [ "$(lxc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  lxc exec filemanip --project=test -- rm -rf /tmp/ptest/source

  # Special case where we are in the same directory as the one we are currently
  # created.
  cd source

  lxc file push -r ./ filemanip/tmp/ptest

  [ "$(lxc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/another_level)" = "1000" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/another_level)" = "1000" ]

  lxc exec filemanip --project=test -- rm -rf /tmp/ptest/*

  lxc file push -r ../source filemanip/tmp/ptest

  [ "$(lxc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(lxc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  # Switch back to old working directory.
  cd "${oldcwd}"

  mkdir "${TEST_DIR}"/dest
  lxc file pull -r filemanip/tmp/ptest/source "${TEST_DIR}"/dest

  [ "$(< "${TEST_DIR}"/dest/source/foo)" = "foo" ]
  [ "$(< "${TEST_DIR}"/dest/source/bar)" = "bar" ]

  [ "$(stat -c "%u" "${TEST_DIR}"/dest/source)" = "$(id -u)" ]
  [ "$(stat -c "%g" "${TEST_DIR}"/dest/source)" = "$(id -g)" ]
  [ "$(stat -c "%a" "${TEST_DIR}"/dest/source)" = "755" ]

  lxc file push -p "${TEST_DIR}"/source/foo local:filemanip/tmp/this/is/a/nonexistent/directory/
  lxc file pull local:filemanip/tmp/this/is/a/nonexistent/directory/foo "${TEST_DIR}"
  [ "$(< "${TEST_DIR}"/foo)" = "foo" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/.
  [ "$(lxc exec filemanip --project=test -- cat /foo)" = "foo" ]

  lxc file push -p "${TEST_DIR}"/source/foo filemanip/A/B/C/D/
  [ "$(lxc exec filemanip --project=test -- cat /A/B/C/D/foo)" = "foo" ]

  if [ "$(storage_backend "$LXD_DIR")" != "lvm" ]; then
    lxc launch testimage idmap -c "raw.idmap=both 0 0"
    [ "$(stat -c %u "${LXD_DIR}/containers/test_idmap/rootfs")" = "0" ]
    lxc delete idmap --force
  fi

  # Test lxc file create.

  # Create a new empty file.
  lxc file create filemanip/tmp/create-test
  [ -z "$(lxc exec filemanip --project=test -- cat /tmp/create-test)" ]

  # This fails because the parent directory doesn't exist.
  ! lxc file create filemanip/tmp/create-test-dir/foo || false

  # Create foo along with the parent directory.
  lxc file create --create-dirs filemanip/tmp/create-test-dir/foo
  [ -z "$(lxc exec filemanip --project=test -- cat /tmp/create-test-dir/foo)" ]

  # Create directory using --type flag.
  lxc file create --type=directory filemanip/tmp/create-test-dir/sub-dir
  lxc exec filemanip --project=test -- test -d /tmp/create-test-dir/sub-dir

  # Create directory using trailing "/".
  lxc file create filemanip/tmp/create-test-dir/sub-dir-1/
  lxc exec filemanip --project=test -- test -d /tmp/create-test-dir/sub-dir-1

  # Create symlink.
  lxc file create --type=symlink filemanip/tmp/create-symlink foo
  [ "$(lxc exec filemanip --project=test -- readlink /tmp/create-symlink)" = "foo" ]

  # Test SFTP functionality.
  cmd=$(unset -f lxc; command -v lxc)
  $cmd file mount filemanip --listen=127.0.0.1:2022 --no-auth &
  mountPID=$!
  sleep 1

  output=$(curl -s -S --insecure sftp://127.0.0.1:2022/foo || true)
  kill -9 "${mountPID}"
  lxc delete filemanip -f
  [ "$output" = "foo" ]

  rm "${TEST_DIR}"/source/baz
  rm -rf "${TEST_DIR}/dest"
  lxc project switch default
  lxc project delete test
}

test_filemanip_req_content_type() {
  inst="c-file-push"

  lxc launch testimage "${inst}"

  # This ensures strings.Reader works correctly with the content-type check.
  # The specific here is that the net/http package will configure the
  # content-length on the request, which in LXD triggers content-type check.
  (
    cd lxd-client
    go run . file-push "${inst}" /tmp/status.txt "success"
    [ "$(lxc exec "${inst}" -- cat /tmp/status.txt)" = "success" ]
  )

  lxc delete "${inst}" --force
}
