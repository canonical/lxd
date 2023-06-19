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
  # shellcheck disable=2039,3043
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
  # shellcheck disable=2039,3043
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

  # Get container's pool.
  pool=$(lxc config profile device get default root pool)

  lxc storage volume set "${pool}" container/bar user.foo=snap0

  # Check parent volume.block.filesystem is copied to snapshot and not from pool.
  if [ "$lxd_backend" = "lvm" ] || [ "$lxd_backend" = "ceph" ]; then
    # Change pool volume.block.filesystem setting after creation of instance and before snapshot.
    lxc storage set "${pool}" volume.block.filesystem=xfs
  fi

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
  initialUUID=$(lxc config get bar volatile.uuid)
  lxc stop bar --force
  lxc storage volume set "${pool}" container/bar user.foo=snap1

  # Delete the state file we created to prevent leaking.
  rm state

  lxc config set bar limits.cpu 1

  lxc snapshot bar snap1
  lxc storage volume set "${pool}" container/bar user.foo=postsnaps

  # Check volume.block.filesystem on storage volume in parent and snapshot match.
  if [ "${lxd_backend}" = "lvm" ] || [ "${lxd_backend}" = "ceph" ]; then
    # Change pool volume.block.filesystem setting after creation of instance and before snapshot.
    pool=$(lxc config profile device get default root pool)
    parentFS=$(lxc storage volume get "${pool}" container/bar block.filesystem)
    snapFS=$(lxc storage volume get "${pool}" container/bar/snap0 block.filesystem)

    if [ "${parentFS}" != "${snapFS}" ]; then
      echo "block.filesystem settings do not match in parent and snapshot"
      false
    fi

    lxc storage unset "${pool}" volume.block.filesystem
  fi

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

    # Check storage volume has been restored (user.foo=snap0)
    lxc storage volume get "${pool}" container/bar user.foo | grep -Fx "snap0"
  fi

  ##########################################################

  # test restore using full snapshot name
  restore_and_compare_fs snap1

  # Check that instances UUID are different before and after snapshoting
  newUUID=$(lxc config get bar volatile.uuid)
  if [ "${initialUUID}" = "${newUUID}" ]; then
    echo "==> UUID of the instance should be different after restoring its snapshot"
    false
  fi

  # Check that instances UUIS are different before and after snapshoting  (stateful mode)
  if ! command -v criu >/dev/null 2>&1; then
    echo "==> SKIP: stateful snapshotting with CRIU (missing binary)"
  else
    initialUUID=$(lxc config get bar volatile.uuid)
    lxc start bar
    lxc snapshot bar snap2 --stateful
    restore_and_compare_fs snap2

    newUUID=$(lxc config get bar volatile.uuid)
    if [ "${initialUUID}" = "${newUUID}" ]; then
      echo "==> UUID of the instance should be different after restoring its stateful snapshot"
      false
    fi

    lxc stop bar --force
  fi

  # Check that instances have two different UUID after a snapshot copy
  lxc launch testimage bar2
  initialUUID=$(lxc config get bar2 volatile.uuid)
  lxc copy bar2 bar3
  newUUID=$(lxc config get bar3 volatile.uuid)

  if [ "${initialUUID}" = "${newUUID}" ]; then
    echo "==> UUID of the instance should be different after copying snapshot into instance"
    false
  fi

  lxc delete --force bar2
  lxc delete --force bar3

  # Check config value in snapshot has been restored
  cpus=$(lxc config get bar limits.cpu)
  if [ "${cpus}" != "1" ]; then
   echo "==> config didn't match expected value after restore (${cpus})"
   false
  fi

  # Check storage volume has been restored (user.foo=snap0)
  lxc storage volume get "${pool}" container/bar user.foo | grep -Fx "snap1"

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
  lxc launch testimage a-b
  lxc snapshot a-b base
  lxc restore a-b base
  lxc snapshot a-b c-d
  lxc restore a-b c-d
  lxc delete -f a-b
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

test_snap_expiry() {
  # shellcheck disable=2039,3043
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc snapshot c1
  lxc config show c1/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

  lxc config set c1 snapshots.expiry '1d'
  lxc snapshot c1
  ! lxc config show c1/snap1 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  lxc copy c1 c2
  ! lxc config show c2/snap1 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  lxc snapshot c1 --no-expiry
  lxc config show c1/snap2 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  lxc rm -f c1
  lxc rm -f c2
}

test_snap_schedule() {
  # shellcheck disable=2039,3043
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Check we get a snapshot on first start
  lxc launch testimage c1 -c snapshots.schedule='@startup'
  lxc launch testimage c2 -c snapshots.schedule='@startup, @daily'
  lxc launch testimage c3 -c snapshots.schedule='@startup, 10 5,6 * * *'
  lxc launch testimage c4 -c snapshots.schedule='@startup, 10 5-8 * * *'
  lxc launch testimage c5 -c snapshots.schedule='@startup, 10 2,5-8/2 * * *'
  lxc info c1 | grep -q snap0
  lxc info c2 | grep -q snap0
  lxc info c3 | grep -q snap0
  lxc info c4 | grep -q snap0
  lxc info c5 | grep -q snap0

  # Check we get a new snapshot on restart
  lxc restart c1 -f
  lxc info c1 | grep -q snap1

  lxc rm -f c1 c2 c3 c4 c5
}

test_snap_volume_db_recovery() {
  # shellcheck disable=2039,3043
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  poolName=$(lxc profile device get default root pool)

  lxc init testimage c1
  lxc snapshot c1
  lxc snapshot c1
  lxc start c1
  lxc stop -f c1
  lxd sql global 'DELETE FROM storage_volumes_snapshots' # Remove volume snapshot DB records.
  lxd sql local 'DELETE FROM  patches WHERE name = "storage_missing_snapshot_records"' # Clear patch indicator.
  ! lxc start c1 || false # Shouldn't be able to start as backup.yaml generation checks for DB consistency.
  lxd shutdown
  respawn_lxd "${LXD_DIR}" true
  lxc storage volume show "${poolName}" container/c1/snap0 | grep "Auto repaired"
  lxc storage volume show "${poolName}" container/c1/snap1 | grep "Auto repaired"
  lxc start c1
  lxc delete -f c1
}
