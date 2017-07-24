test_snapshots() {
  snapshots

  if [ "$(storage_backend "$LXD_DIR")" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with snaphots.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snapshots" lvm lvm.use_thinpool=false volume.size=25MB
    lxc profile device set default root pool "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snapshots"

    snapshots

    lxc profile device set default root pool "lxdtest-$(basename "${LXD_DIR}")"

    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snapshots"
  fi
}

snapshots() {
  # shellcheck disable=2039
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage foo

  lxc snapshot foo
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/foo/snap0" ]
  fi

  lxc snapshot foo
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/foo/snap1" ]
  fi

  lxc snapshot foo tester
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/foo/tester" ]
  fi

  lxc copy foo/tester foosnap1
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" != "lvm" ] && [ "${lxd_backend}" != "zfs" ] && [ "$lxd_backend" != "ceph" ]; then
    [ -d "${LXD_DIR}/containers/foosnap1/rootfs" ]
  fi

  lxc delete foo/snap0
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ ! -d "${LXD_DIR}/snapshots/foo/snap0" ]
  fi

  # no CLI for this, so we use the API directly (rename a snapshot)
  wait_for "${LXD_ADDR}" my_curl -X POST "https://${LXD_ADDR}/1.0/containers/foo/snapshots/tester" -d "{\"name\":\"tester2\"}"
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ ! -d "${LXD_DIR}/snapshots/foo/tester" ]
  fi

  lxc move foo/tester2 foo/tester-two
  lxc delete foo/tester-two
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ ! -d "${LXD_DIR}/snapshots/foo/tester-two" ]
  fi

  lxc snapshot foo namechange
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/foo/namechange" ]
  fi
  lxc move foo foople
  [ ! -d "${LXD_DIR}/containers/foo" ]
  [ -d "${LXD_DIR}/containers/foople" ]
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/foople/namechange" ]
    [ -d "${LXD_DIR}/snapshots/foople/namechange" ]
  fi

  lxc delete foople
  lxc delete foosnap1
  [ ! -d "${LXD_DIR}/containers/foople" ]
  [ ! -d "${LXD_DIR}/containers/foosnap1" ]
}

test_snap_restore() {
  snap_restore

  if [ "$(storage_backend "$LXD_DIR")" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with snaphots.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snap-restore" lvm lvm.use_thinpool=false volume.size=25MB
    lxc profile device set default root pool "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snap-restore"

    snap_restore

    lxc profile device set default root pool "lxdtest-$(basename "${LXD_DIR}")"

    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snap-restore"
  fi
}

snap_restore() {
  # shellcheck disable=2039
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  ##########################################################
  # PREPARATION
  ##########################################################

  ## create some state we will check for when snapshot is restored

  ## prepare snap0
  lxc launch testimage bar
  echo snap0 > state
  lxc file push state bar/root/state
  lxc file push state bar/root/file_only_in_snap0
  lxc exec bar -- mkdir /root/dir_only_in_snap0
  lxc exec bar -- ln -s file_only_in_snap0 /root/statelink
  lxc stop bar --force

  lxc snapshot bar snap0

  ## prepare snap1
  lxc start bar
  echo snap1 > state
  lxc file push state bar/root/state
  lxc file push state bar/root/file_only_in_snap1

  lxc exec bar -- rmdir /root/dir_only_in_snap0
  lxc exec bar -- rm /root/file_only_in_snap0
  lxc exec bar -- rm /root/statelink
  lxc exec bar -- ln -s file_only_in_snap1 /root/statelink
  lxc exec bar -- mkdir /root/dir_only_in_snap1
  lxc stop bar --force

  # Delete the state file we created to prevent leaking.
  rm state

  lxc config set bar limits.cpu 1

  lxc snapshot bar snap1

  ##########################################################

  if [ "$lxd_backend" != "zfs" ]; then
    # The problem here is that you can't `zfs rollback` to a snapshot with a
    # parent, which snap0 has (snap1).
    restore_and_compare_fs snap0

    # Check container config has been restored (limits.cpu is unset)
    cpus=$(lxc config get bar limits.cpu)
    if [ -n "${cpus}" ]; then
      echo "==> config didn't match expected value after restore (${cpus})"
      false
    fi
  fi

  ##########################################################

  # test restore using full snapshot name
  restore_and_compare_fs snap1

  # Check config value in snapshot has been restored
  cpus=$(lxc config get bar limits.cpu)
  if [ "${cpus}" != "1" ]; then
   echo "==> config didn't match expected value after restore (${cpus})"
   false
  fi

  ##########################################################

  # Start container and then restore snapshot to verify the running state after restore.
  lxc start bar

  if [ "$lxd_backend" != "zfs" ]; then
    # see comment above about snap0
    restore_and_compare_fs snap0

    # check container is running after restore
    lxc list | grep bar | grep RUNNING
  fi

  lxc stop --force bar

  lxc delete bar

  # Test if container's with hyphen's in their names are treated correctly.
  if [ "$lxd_backend" = "lvm" ]; then
    lxc launch testimage a-b
    lxc snapshot a-b base
    lxc restore a-b base

    lxc snapshot a-b c-d
    lxc restore a-b c-d

    lxc delete -f a-b
  fi
}

restore_and_compare_fs() {
  snap=${1}
  echo "==> Restoring ${snap}"

  lxc restore bar "${snap}"

  # FIXME: make this backend agnostic
  if [ "$(storage_backend "$LXD_DIR")" = "dir" ]; then
    # Recursive diff of container FS
    diff -r "${LXD_DIR}/containers/bar/rootfs" "${LXD_DIR}/snapshots/bar/${snap}/rootfs"
  fi
}
