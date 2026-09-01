test_shutdown() {
  # shellcheck disable=2039,3043
  local lxd_backend forkfile_pid_file forkfile_pid

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "zfs" ]; then
    echo "==> SKIP: test_shutdown only applies to the zfs storage driver"
    return
  fi

  ensure_import_testimage

  lxc init testimage i1

  # Performing a file operation on a stopped instance makes LXD mount the instance's storage
  # volume and spawn a forkfile daemon that keeps it mounted/busy for a short while afterwards.
  echo "test" > "${TEST_DIR}/forkfile-test"
  lxc file push "${TEST_DIR}/forkfile-test" i1/root/forkfile-test

  forkfile_pid_file="${LXD_DIR}/logs/i1/forkfile.pid"
  [ -f "${forkfile_pid_file}" ]
  forkfile_pid=$(cat "${forkfile_pid_file}")
  kill -0 "${forkfile_pid}"

  # A full daemon shutdown must stop any lingering forkfile daemon left behind by a stopped
  # instance, otherwise it can keep the instance's storage volume busy/mounted after LXD has
  # exited, preventing the storage pool from being unmounted/exported cleanly.
  shutdown_lxd "${LXD_DIR}"

  if kill -0 "${forkfile_pid}" 2>/dev/null; then
    echo "FAIL: forkfile daemon (pid ${forkfile_pid}) is still running after LXD shutdown"
    false
  fi

  respawn_lxd "${LXD_DIR}" true

  lxc delete -f i1
}
