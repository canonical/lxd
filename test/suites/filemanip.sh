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
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/instances/filemanip/files?path=/tmp/foo")
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

  # Verify recursive pull is idempotent when target directories already exist
  # and that permissions are restored to match the source.
  # Change the existing symlink so the second pull must overwrite it.
  ln --symbolic --force --no-dereference foo "${TEST_DIR}"/dest/source/baz
  [ "$(readlink "${TEST_DIR}"/dest/source/baz)" = "foo" ]
  chmod 700 "${TEST_DIR}"/dest/source
  lxc file pull --create-dirs --recursive filemanip/tmp/ptest/source "${TEST_DIR}"/dest
  [ "$(stat -c "%a" "${TEST_DIR}"/dest/source)" = "755" ]
  [ -L "${TEST_DIR}"/dest/source/baz ]
  [ "$(basename "$(readlink "${TEST_DIR}"/dest/source/baz)")" = "bar" ]

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
  [ -z "$(lxc exec filemanip --project=test -- cat /tmp/create-test || echo fail)" ]

  # This fails because the parent directory doesn't exist.
  ! lxc file create filemanip/tmp/create-test-dir/foo || false

  # Create foo along with the parent directory.
  lxc file create --create-dirs filemanip/tmp/create-test-dir/foo
  [ -z "$(lxc exec filemanip --project=test -- cat /tmp/create-test-dir/foo || echo fail)" ]

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
  CURL_OPTIONS="--silent --show-error"
  if grep -qxF 'VERSION_ID="24.04"' /etc/os-release; then
    # On 24.04, curl insists on checking ~/.ssh/known_hosts even if --hostpubsha256 is used.
    # --insecure disables that check while still allowing the fingerprint check to work.
    CURL_OPTIONS="${CURL_OPTIONS} --insecure"
  fi

  NOAUTH_FILE="$(mktemp)"
  "${_LXC}" file mount filemanip --listen=127.0.0.1:2022 --no-auth > "${NOAUTH_FILE}" &
  mountPID=$!
  sleep 0.1
  fingerprint=$(sed -nE 's/^SSH host key fingerprint: SHA256:(.+)$/\1/p' "${NOAUTH_FILE}")
  rm "${NOAUTH_FILE}"

  # shellcheck disable=SC2086
  [ "$(curl ${CURL_OPTIONS} --hostpubsha256 "${fingerprint}" sftp://127.0.0.1:2022/foo)" = "foo" ]
  kill_go_proc "${mountPID}"

  CREDS_FILE="$(mktemp)"
  "${_LXC}" file mount filemanip --listen=127.0.0.1:2022 > "${CREDS_FILE}" &
  mountPID=$!
  sleep 0.1
  fingerprint=$(sed -nE 's/^SSH host key fingerprint: SHA256:(.+)$/\1/p' "${CREDS_FILE}")
  userCreds=$(sed -nE 's/^[^"]+ "([^"]+)" [^"]+ "([^"]+)"$/\1:\2/p' "${CREDS_FILE}")
  rm "${CREDS_FILE}"

  # shellcheck disable=SC2086
  [ "$(curl ${CURL_OPTIONS} --hostpubsha256 "${fingerprint}" --user "${userCreds}" sftp://127.0.0.1:2022/foo)" = "foo" ]
  kill_go_proc "${mountPID}"

  lxc delete -f filemanip

  rm "${TEST_DIR}"/filemanip
  rm -rf "${TEST_DIR}/source" "${TEST_DIR}/dest"
  lxc project switch default
  lxc project delete test
}

test_filemanip_req_content_type() {
  ensure_import_testimage

  inst="c-file-push"

  lxc launch testimage "${inst}"

  # This ensures strings.Reader works correctly with the content-type check.
  # The specific here is that the net/http package will configure the
  # content-length on the request, which in LXD triggers content-type check.
  lxd-client file-push "${inst}" /tmp/status.txt "success"
  [ "$(lxc exec "${inst}" -- cat /tmp/status.txt)" = "success" ]

  lxc delete "${inst}" --force
}

# _forkfile_copy_worker is the per-worker body of test_filemanip_concurrent_copy.
# Each worker repeatedly copies the stopped base instance into a new instance,
# immediately performs a file push on the copy, then deletes it. Any failure
# whose error message mentions "forkfile.sock" is appended to error_file.
_forkfile_copy_worker() {
  local worker_idx="${1}"
  local base_name="${2}"
  local copy_prefix="${3}"
  local iterations="${4}"
  local error_file="${5}"

  local i name err

  for i in $(seq "${iterations}"); do
    name="${copy_prefix}-${worker_idx}-${i}"

    lxc copy "${base_name}" "${name}"

    if ! err=$(lxc file push /dev/null "${name}/forkfile-race-test" 2>&1); then
      if grep -qF "forkfile.sock" <<< "${err}"; then
        printf '%s\n' "${err}" >> "${error_file}"
      else
        printf 'worker %s: unexpected file operation error: %s\n' "${worker_idx}" "${err}" >&2
        return 1
      fi
    fi

    lxc delete "${name}"
  done
}

# Regression test for the forkfile socket race condition in fileSFTPConnNoLock
# (lxd/instance/drivers/driver_lxc.go) detailed in
# https://github.com/canonical/lxd/issues/18403
#
# The race: net.UnixListener stores the socket path as /proc/self/fd/<N>/forkfile.sock
# where N is an open directory fd.  That fd is closed when fileSFTPConnNoLock returns,
# making N available for reuse.  A background goroutine holding the listener then calls
# Close(), which resolves the now-recycled /proc/self/fd/<N> through whichever directory
# fd N currently refers to and unlinks that instance's socket — causing the next caller's
# DialUnix to fail with ENOENT.
#
# File systems that support instant copy (e.g. ZFS, Btrfs) eliminates the storage I/O delay
# between copy completion and the first file operation, making the race window reliably
# hit.  On slower backends the window exists but is too narrow to trigger consistently.
# This test uses Btrfs because its CoW is the fastest making test time shorter.
test_filemanip_concurrent_copy() {

  if [ "$(storage_backend "$LXD_DIR")" != "btrfs" ]; then
    export TEST_UNMET_REQUIREMENT="requires btrfs backend"
    return
  fi

  ensure_import_testimage

  local rand base copy_prefix workers iterations error_file pids w pid

  rand="${RANDOM}"
  base="forkfile-base-${rand}"
  copy_prefix="forkfile-copy-${rand}"
  workers=12
  iterations=12
  error_file="$(mktemp -p "${TEST_DIR}" "forkfile-error-${rand}-XXXX")"
  pids=""

  lxc init testimage "${base}"

  for w in $(seq "${workers}"); do
    _forkfile_copy_worker "${w}" "${base}" "${copy_prefix}" "${iterations}" "${error_file}" &
    pids="${pids} $!"
  done

  local rc failed_pid
  failed_pid=""

  for pid in ${pids}; do
    if wait "${pid}"; then
      rc=0
    else
      rc=$?
    fi

    if [ "${rc}" -ne 0 ] && [ "${rc}" -ne 127 ] && [ -z "${failed_pid}" ]; then
      failed_pid="${pid}:${rc}"
    fi
  done

  # Delete any copies left behind by a worker killed mid-iteration.
  # Workers delete their own copies on success, so most of these won't exist.
  for w in $(seq "${workers}"); do
    for i in $(seq "${iterations}"); do
      lxc delete "${copy_prefix}-${w}-${i}" 2>/dev/null || true
    done
  done

  lxc delete "${base}"

  if [ -n "${failed_pid}" ]; then
    echo "worker ${failed_pid%%:*} failed with exit status ${failed_pid##*:}"
  fi

  sub_test "Assert no forkfile socket race condition errors"
  if [ -s "${error_file}" ]; then
    echo "==> forkfile socket race detected:"
    cat "${error_file}"
    false
  fi

  rm "${error_file}"

  sub_test "Assert no unexpected file operation errors"
  [ -z "${failed_pid}" ]
}
