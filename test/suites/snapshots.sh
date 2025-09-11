test_snapshots() {
  ensure_import_testimage
  snapshots "lxdtest-$(basename "${LXD_DIR}")"

  if [ "$(storage_backend "$LXD_DIR")" = "lvm" ]; then
    pool="lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snapshots"

    # Test that non-thinpool lvm backends work fine with snaphots.
    lxc storage create "${pool}" lvm lvm.use_thinpool=false volume.size=25MiB
    lxc profile device set default root pool "${pool}"

    snapshots "${pool}"

    lxc profile device set default root pool "lxdtest-$(basename "${LXD_DIR}")"

    lxc storage delete "${pool}"
  fi
}

snapshots() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")
  pool="$1"

  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage foo -d "${SMALL_ROOT_DISK}"

  echo "Verify that / is not permitted in snapshots.pattern"
  lxc config set foo snapshots.pattern="/"
  SNAP_ERR="$(! lxc snapshot foo 2>&1)"
  echo "${SNAP_ERR}" | grep -xF 'Error: Invalid snapshot name: Cannot contain "/"'
  [ "$(lxc list -f csv -c S foo)" = "0" ]

  echo "Test pongo2 template restrictions"
  # XXX: using wordcount filter to avoid `\n` or other unexpected char.
  lxc config set foo snapshots.pattern='{% filter wordcount %}{% include \"/etc/hosts\" %}{% endfilter %}'
  SNAP_ERR="$(! lxc snapshot foo 2>&1)"
  echo "${SNAP_ERR}" | grep -F "Usage of tag 'include' is not allowed (sandbox restriction active)"
  [ "$(lxc list -f csv -c S foo)" = "0" ]
  lxc config unset foo snapshots.pattern

  lxc snapshot foo
  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/foo/snap0" ]
  fi

  # Check if the snapshot has an UUID
  [ -n "$(lxc storage volume get "${pool}" container/foo/snap0 volatile.uuid)" ]

  # Check if the snapshot's UUID is different from the parent volume
  [ "$(lxc storage volume get "${pool}" container/foo/snap0 volatile.uuid)" != "$(lxc storage volume get "${pool}" container/foo volatile.uuid)" ]

  # Check if the snapshot's UUID can be modified
  ! lxc storage volume set "${pool}" container/foo/snap0 volatile.uuid "2d94c537-5eff-4751-95b1-6a1b7d11f849" || false

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

  # Create a snapshot with an expiry date specified in a YAML
  expiry_date_in_one_minute=$(date -u -d '+10 minute' '+%Y-%m-%dT%H:%M:%SZ')
  lxc snapshot foo tester_yaml <<EOF
expires_at: ${expiry_date_in_one_minute}
EOF
  # Check that the expiry date is set correctly
  lxc config show foo/tester_yaml | grep "expires_at: ${expiry_date_in_one_minute}"
  # Delete the snapshot
  lxc delete foo/tester_yaml

  # Check if the instance with snapshots and non-default profile can be copied to a new project
  lxc profile create source-profile
  lxc profile add foo source-profile
  lxc project create test-project

  lxc copy foo foo --no-profiles -s "${pool}" --project default --target-project test-project
  [ "$(lxc list -f csv -c S --project test-project foo)" = "3" ]
  lxc delete --project=test-project foo

  lxc profile create target-profile --project=test-project
  lxc copy foo foo --profile=target-profile -s "${pool}" --target-project=test-project
  [ "$(lxc list -f csv -c S --project test-project foo)" = "3" ]
  lxc delete --project test-project foo

  lxc profile delete target-profile --project=test-project
  lxc profile remove foo source-profile
  lxc profile delete source-profile
  lxc project delete test-project

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

  # test deleting multiple snapshots
  lxc snapshot foo snap2
  lxc snapshot foo snap3
  lxc delete foo/snap2 foo/snap3
  ! lxc info foo | grep -wF snap2 || false
  ! lxc info foo | grep -wF snap3 || false

  # no CLI for this, so we use the API directly (rename a snapshot)
  wait_for "${LXD_ADDR}" my_curl -X POST --fail-with-body -H 'Content-Type: application/json' "https://${LXD_ADDR}/1.0/containers/foo/snapshots/tester" -d "{\"name\":\"tester2\"}"
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
  snap_restore "lxdtest-$(basename "${LXD_DIR}")"

  if [ "$(storage_backend "$LXD_DIR")" = "lvm" ]; then
    pool="lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-snap-restore"

    # Test that non-thinpool lvm backends work fine with snaphots.
    lxc storage create "${pool}" lvm lvm.use_thinpool=false volume.size=25MiB
    lxc profile device set default root pool "${pool}"

    snap_restore "${pool}"

    lxc profile device set default root pool "lxdtest-$(basename "${LXD_DIR}")"

    lxc storage delete "${pool}"
  fi
}

snap_restore() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")
  pool="$1"

  ensure_import_testimage

  ##########################################################
  # PREPARATION
  ##########################################################

  ## create some state we will check for when snapshot is restored

  ## prepare snap0
  lxc launch testimage bar -d "${SMALL_ROOT_DISK}"
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
  initialGenerationID=$(lxc config get bar volatile.uuid.generation)
  initialVolumeUUID=$(lxc storage volume get "${pool}" container/bar volatile.uuid)
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
    [ "$(lxc storage volume get "${pool}" container/bar user.foo)" = "snap0" ]
  fi

  ##########################################################

  # test restore using full snapshot name
  restore_and_compare_fs snap1

  # Check that instances UUID remain the same before and after snapshoting
  newUUID=$(lxc config get bar volatile.uuid)
  if [ "${initialUUID}" != "${newUUID}" ]; then
    echo "==> UUID of the instance should remain the same after restoring its snapshot"
    false
  fi

  # Check that the generation UUID from before changes compared to the one after snapshoting
  newGenerationID=$(lxc config get bar volatile.uuid.generation)
  if [ "${initialGenerationID}" = "${newGenerationID}" ]; then
    echo "==> Generation UUID of the instance should change after restoring its snapshot"
    false
  fi

  # Check if the volumes's UUID is the same as the original volume
  [ "$(lxc storage volume get "${pool}" container/bar volatile.uuid)" = "${initialVolumeUUID}" ]

  # Check that instances have two different UUID after a snapshot copy
  lxc launch testimage bar2 -d "${SMALL_ROOT_DISK}"
  initialUUID=$(lxc config get bar2 volatile.uuid)
  initialGenerationID=$(lxc config get bar2 volatile.uuid.generation)
  lxc copy bar2 bar3
  newUUID=$(lxc config get bar3 volatile.uuid)
  newGenerationID=$(lxc config get bar3 volatile.uuid.generation)

  if [ "${initialGenerationID}" = "${newGenerationID}" ] || [ "${initialUUID}" = "${newUUID}" ]; then
    echo "==> UUIDs of the instance should be different after copying snapshot into instance"
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
  [ "$(lxc storage volume get "${pool}" container/bar user.foo)" = "snap1" ]

  ##########################################################

  # Start container and then restore snapshot to verify the running state after restore.
  lxc start bar

  if [ "$lxd_backend" != "zfs" ]; then
    # see comment above about snap0
    restore_and_compare_fs snap0

    # check container is running after restore
    lxc list --fast | grep -wF bar | grep -wF RUNNING
  fi

  lxc delete --force bar

  # Test if container's with hyphen's in their names are treated correctly.
  lxc launch testimage a-b -d "${SMALL_ROOT_DISK}"
  lxc snapshot a-b base
  lxc restore a-b base
  lxc snapshot a-b c-d
  lxc restore a-b c-d
  lxc delete -f a-b

  # Check snapshot creation dates.
  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1
  ! lxc storage volume show "${pool}" container/c1 | grep '^created_at: 0001-01-01T00:00:00Z' || false
  ! lxc storage volume show "${pool}" container/c1/snap0 | grep '^created_at: 0001-01-01T00:00:00Z' || false
  lxc copy c1 c2
  ! lxc storage volume show "${pool}" container/c2 | grep '^created_at: 0001-01-01T00:00:00Z' || false
  [ "$(lxc storage volume show "${pool}" container/c1/snap0 | awk /created_at:/)" = "$(lxc storage volume show "${pool}" container/c2/snap0 | awk /created_at:/)" ]
  lxc delete c1 c2

  # Check the restore isn't blocked by not anymore existing custom volumes.
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc storage volume create "${pool}" foo
  lxc storage volume attach "${pool}" foo c1 path=/mnt
  lxc snapshot c1
  lxc storage volume detach "${pool}" foo c1
  lxc storage volume delete "${pool}" foo
  lxc restore c1 snap0
  ! lxc start c1 || false # Fails because custom vol foo in "${pool}" doesn't exist anymore.
  lxc config device remove c1 foo
  lxc start c1
  lxc delete -f c1
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
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1
  lxc config show c1/snap0 | grep -F 'expires_at: 0001-01-01T00:00:00Z'
  [ "$(lxc config get --property c1/snap0 expires_at)" = "0001-01-01 00:00:00 +0000 UTC" ]

  # Check the API returns the zero time representation when listing all snapshots in recursive mode.
  [ "$(lxc query "/1.0/instances/c1?recursion=2" | jq -r '.snapshots[] | select(.name == "snap0") | .expires_at')" = "0001-01-01T00:00:00Z" ]

  lxc config set c1 snapshots.expiry '1d'
  lxc snapshot c1

  # Get snapshot created_at and expires_at properties.
  # Remove the " +0000 UTC" from the end of the timestamp so we can add one day using `date`.
  created_at="$(lxc config get c1/snap1 --property created_at | awk -F' +' '{print $1}')"
  expires_at="$(lxc config get c1/snap1 --property expires_at | awk -F' +' '{print $1}')"

  # Check if the expires_at propery is exactly 1d ahead.
  [ "$(date -d "${created_at} today + 1days")" = "$(date -d "${expires_at}")" ]

  lxc copy c1 c2
  ! lxc config show c2/snap1 | grep -F 'expires_at: 0001-01-01T00:00:00Z' || false
  [ "$(lxc config get --property c2/snap1 expires_at)" != "0001-01-01 00:00:00 +0000 UTC" ]

  lxc snapshot c1 --no-expiry
  lxc config show c1/snap2 | grep -F 'expires_at: 0001-01-01T00:00:00Z'
  [ "$(lxc config get --property c1/snap2 expires_at)" = "0001-01-01 00:00:00 +0000 UTC" ]

  lxc delete c1 c2
}

test_snap_schedule() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage

  # Check we get a snapshot on first start
  lxc launch testimage c1 -d "${SMALL_ROOT_DISK}" -c snapshots.schedule='@startup'
  lxc launch testimage c2 -d "${SMALL_ROOT_DISK}" -c snapshots.schedule='@startup, @daily'
  lxc launch testimage c3 -d "${SMALL_ROOT_DISK}" -c snapshots.schedule='@startup, 10 5,6 * * *'
  lxc launch testimage c4 -d "${SMALL_ROOT_DISK}" -c snapshots.schedule='@startup, 10 5-8 * * *'
  lxc launch testimage c5 -d "${SMALL_ROOT_DISK}" -c snapshots.schedule='@startup, 10 2,5-8/2 * * *'
  [ "$(lxc list --columns nS --format csv c1)" = "c1,1" ]
  [ "$(lxc list --columns nS --format csv c2)" = "c2,1" ]
  [ "$(lxc list --columns nS --format csv c3)" = "c3,1" ]
  [ "$(lxc list --columns nS --format csv c4)" = "c4,1" ]
  [ "$(lxc list --columns nS --format csv c5)" = "c5,1" ]

  # Check we get a new snapshot on restart
  lxc restart c1 -f
  [ "$(lxc list --columns nS --format csv c1)" = "c1,2" ]

  lxc delete -f c1 c2 c3 c4 c5
}

test_snap_volume_db_recovery() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage

  poolName=$(lxc profile device get default root pool)

  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
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

test_snap_fail() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  if [ "${lxd_backend}" != "zfs" ]; then
    echo "==> SKIP: test_snap_fail only supports 'zfs', not ${lxd_backend}"
    return
  fi

  ensure_import_testimage

  # Containers should fail to snapshot when root is full (can't write to backup.yaml)
  lxc launch testimage c1 --device root,size=2MiB
  if lxc exec c1 -- dd if=/dev/urandom of=/root/big.bin count=100 bs=100K; then
    echo "Writting more data than the root size should have failed"
    false
  fi

  ! lxc snapshot c1 || false

  # Make sure that the snapshot creation failed (c1 has 0 snapshots)
  [ "$(lxc list --columns nS --format csv c1)" = "c1,0" ]

  lxc delete --force c1
}
