test_incremental_copy() {
  # shellcheck disable=2039
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  do_copy "" ""

  # cross-pool copy
  if [ "${lxd_backend}" != 'dir' ]; then
    # shellcheck disable=2039
    local source_pool
    source_pool="lxdtest-$(basename "${LXD_DIR}")-dir-pool"
    lxc storage create "${source_pool}" dir
    do_copy "${source_pool}" "lxdtest-$(basename "${LXD_DIR}")"
    lxc storage rm "${source_pool}"
  fi
}

do_copy() {
  # shellcheck disable=2039
  local source_pool=$1
  # shellcheck disable=2039
  local target_pool=$2

  # Make sure the containers don't exist
  lxc rm -f c1 c2 || true

  if [ -z "${source_pool}" ]; then
    lxc init testimage c1
  else
    lxc init testimage c1 -s "${source_pool}"
  fi

  pool=
  if [ -n "${target_pool}" ]; then
    pool="-s ${target_pool}"
  fi

  # Initial copy
  # shellcheck disable=2086
  lxc copy c1 c2 ${pool}

  # Make sure the testfile doesn't exist
  ! lxc exec c1 -- touch /root/testfile1
  ! lxc exec c2 -- touch /root/testfile1

  lxc start c1 c2

  # Target container may not be running when refreshing
  # shellcheck disable=2086
  ! lxc copy c1 c2 --refresh ${pool}

  # Create test file in c1
  lxc exec c1 -- touch /root/testfile1

  lxc stop -f c2

  # Refresh the container and validate the contents
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${pool}
  lxc start c2
  lxc exec c2 -- test -f /root/testfile1
  lxc stop -f c2

  # This will create snapshot c1/snap0
  lxc snapshot c1

  # Remove the testfile from c1 and refresh again
  lxc exec c1 -- rm /root/testfile1
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh --container-only ${pool}
  lxc start c2
  ! lxc exec c2 -- test -f /root/testfile1
  lxc stop -f c2

  # Check whether snapshot c2/snap0 has been created
  ! lxc config show c2/snap0
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${pool}
  lxc config show c2/snap0

  # This will create snapshot c2/snap1
  lxc snapshot c2
  lxc config show c2/snap1

  # This should remove c2/snap1
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${pool}
  ! lxc config show c2/snap1

  lxc rm -f c1 c2
}
