test_container_recover() {
  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_IMPORT_DIR}"
  spawn_lxd "${LXD_IMPORT_DIR}" true

  (
    set -e

    # shellcheck disable=SC2030
    LXD_DIR=${LXD_IMPORT_DIR}
    lxd_backend=$(storage_backend "$LXD_DIR")

    ensure_import_testimage

    poolName=$(lxc profile device get default root pool)
    poolDriver=$(lxc storage show "${poolName}" | grep 'driver:' | awk '{print $2}')

    lxc storage set "${poolName}" user.foo=bah
    lxc project create test -c features.images=false -c features.profiles=true -c features.storage.volumes=true
    lxc profile device add default root disk path=/ pool="${poolName}" --project test
    lxc profile device add default eth0 nic nictype=p2p --project test
    lxc project switch test

    # Basic no-op check.
    cat <<EOF | lxd recover | grep "No unknown volumes found. Nothing to do."
no
yes
EOF

    # Recover container and custom volume that isn't mounted.
    lxc init testimage c1
    lxc storage volume create "${poolName}" vol1_test
    lxc storage volume attach "${poolName}" vol1_test c1 /mnt
    lxc start c1
    lxc exec c1 --project test -- mount | grep /mnt
    echo "hello world" | lxc exec c1 --project test -- tee /mnt/test.txt
    lxc exec c1 --project test -- cat /mnt/test.txt | grep "hello world"
    lxc stop -f c1
    lxc snapshot c1
    lxc info c1

    lxc storage volume snapshot "${poolName}" vol1_test snap0
    lxc storage volume ls "${poolName}" | grep vol1_test
    lxc storage volume ls "${poolName}" | grep -c vol1_test | grep 2

    # Remove container DB records and symlink.
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"
    rm "${LXD_DIR}/containers/test_c1"

    # Remove mount directories if block backed storage.
    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      rmdir "${LXD_DIR}/storage-pools/${poolName}/containers/test_c1"
      rmdir "${LXD_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1/snap0"
      rmdir "${LXD_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1"
    fi

    # Remove custom volume DB record.
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol1_test'"

    # Remove mount directories if block backed storage.
    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      rmdir "${LXD_DIR}/storage-pools/${poolName}/custom/test_vol1_test"
      rmdir "${LXD_DIR}/storage-pools/${poolName}/custom-snapshots/test_vol1_test/snap0"
      rmdir "${LXD_DIR}/storage-pools/${poolName}/custom-snapshots/test_vol1_test"
    fi

    # Check container appears removed.
    ! ls "${LXD_DIR}/containers/test_c1" || false
    ! lxc info c1 || false

    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      ! ls "${LXD_DIR}/storage-pools/${poolName}/containers/test_c1" || false
      ! ls "${LXD_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1" || false
    fi

    # Check custom volume appears removed.
    ! lxc storage volume ls "${poolName}" | grep vol1_test || false

    cat <<EOF | lxd recover
no
yes
yes
EOF

    # Check container mount directories have been restored.
    ls "${LXD_DIR}/containers/test_c1"
    ls "${LXD_DIR}/storage-pools/${poolName}/containers/test_c1"
    ls "${LXD_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1/snap0"

    # Check custom volume mount directories have been restored.
    ls "${LXD_DIR}/storage-pools/${poolName}/custom/test_vol1_test"
    ls "${LXD_DIR}/storage-pools/${poolName}/custom-snapshots/test_vol1_test/snap0"

    # Check custom volume record exists with snapshot.
    lxc storage volume ls "${poolName}" | grep vol1_test
    lxc storage volume ls "${poolName}" | grep -c vol1_test | grep 2

    # Check snapshot exists and container can be started.
    lxc info c1 | grep snap0
    lxc start c1
    lxc exec c1 --project test -- hostname

    # Check custom volume accessible.
    lxc exec c1 --project test -- mount | grep /mnt
    lxc exec c1 --project test -- cat /mnt/test.txt | grep "hello world"

    # Check snashot can be restored.
    lxc restore c1 snap0
    lxc info c1
    lxc exec c1 --project test -- hostname

    # Recover container that is running.
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"

    # Restart LXD so internal mount counters are cleared for deleted (but running) container.
    shutdown_lxd "${LXD_DIR}"
    respawn_lxd "${LXD_DIR}" true

    cat <<EOF | lxd recover
no
yes
yes
EOF

    lxc info c1 | grep snap0
    lxc exec c1 --project test -- hostname
    lxc restore c1 snap0
    lxc info c1
    lxc exec c1 --project test -- hostname

    # Test recover after pool DB config deletion too.
    poolConfigBefore=$(lxd sql global "SELECT key,value FROM storage_pools_config JOIN storage_pools ON storage_pools.id = storage_pools_config.storage_pool_id WHERE storage_pools.name = '${poolName}' ORDER BY key")
    poolSource=$(lxc storage get "${poolName}" source)
    poolExtraConfig=""

    case $poolDriver in
      lvm)
        poolExtraConfig="lvm.vg_name=$(lxc storage get "${poolName}" lvm.vg_name)
"
      ;;
      zfs)
        poolExtraConfig="zfs.pool_name=$(lxc storage get "${poolName}" zfs.pool_name)
"
      ;;
      ceph)
        poolExtraConfig="ceph.cluster_name=$(lxc storage get "${poolName}" ceph.cluster_name)
ceph.osd.pool_name=$(lxc storage get "${poolName}" ceph.osd.pool_name)
ceph.user.name=$(lxc storage get "${poolName}" ceph.user.name)
"
      ;;
    esac

    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_pools WHERE name='${poolName}'"

    cat <<EOF |lxd recover
yes
${poolName}
${poolDriver}
${poolSource}
${poolExtraConfig}
no
yes
yes
EOF

    # Check recovered pool config (from instance backup file) matches what originally was there.
    lxc storage show "${poolName}"
    poolConfigAfter=$(lxd sql global "SELECT key,value FROM storage_pools_config JOIN storage_pools ON storage_pools.id = storage_pools_config.storage_pool_id WHERE storage_pools.name = '${poolName}' ORDER BY key")
    echo "Before:"
    echo "${poolConfigBefore}"

    echo "After:"
    echo "${poolConfigAfter}"

    [ "${poolConfigBefore}" =  "${poolConfigAfter}" ] || false
    lxc storage show "${poolName}"

    lxc info c1 | grep snap0
    lxc exec c1 --project test -- ls
    lxc restore c1 snap0
    lxc info c1
    lxc exec c1 --project test -- ls
    lxc delete -f c1
    lxc storage volume delete "${poolName}" vol1_test
    lxc project switch default
    lxc project delete test
  )

  # shellcheck disable=SC2031
  LXD_DIR=${LXD_DIR}
  kill_lxd "${LXD_IMPORT_DIR}"
}

test_backup_import() {
  test_backup_import_with_project
  test_backup_import_with_project fooproject
}

test_backup_import_with_project() {
  project="default"

  if [ "$#" -ne 0 ]; then
    # Create a projects
    project="$1"
    lxc project create "$project"
    lxc project create "$project-b"
    lxc project switch "$project"

    deps/import-busybox --project "$project" --alias testimage
    deps/import-busybox --project "$project-b" --alias testimage

    # Add a root device to the default profile of the project
    pool="lxdtest-$(basename "${LXD_DIR}")"
    lxc profile device add default root disk path="/" pool="${pool}"
    lxc profile device add default root disk path="/" pool="${pool}" --project "$project-b"
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc launch testimage c2
  lxc snapshot c2

  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  # create backup
  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  lxc delete --force c1

  # import backup, and ensure it's valid and runnable
  lxc import "${LXD_DIR}/c1.tar.gz"
  lxc info c1
  lxc start c1
  lxc delete --force c1

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc import "${LXD_DIR}/c1-optimized.tar.gz"
    lxc info c1
    lxc start c1
    lxc delete --force c1
  fi

  # with snapshots

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc export c2 "${LXD_DIR}/c2-optimized.tar.gz" --optimized-storage
  fi

  lxc export c2 "${LXD_DIR}/c2.tar.gz"
  lxc delete --force c2

  lxc import "${LXD_DIR}/c2.tar.gz"
  lxc info c2 | grep snap0
  lxc start c2
  lxc stop c2 --force

  if [ "$#" -ne 0 ]; then
    # Import into different project (before deleting earlier import).
    lxc import "${LXD_DIR}/c2.tar.gz" --project "$project-b"
    lxc info c2 --project "$project-b" | grep snap0
    lxc start c2 --project "$project-b"
    lxc stop c2 --project "$project-b" --force
    lxc restore c2 snap0 --project "$project-b"
    lxc delete --force c2 --project "$project-b"
  fi

  lxc restore c2 snap0
  lxc start c2
  lxc delete --force c2

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc import "${LXD_DIR}/c2-optimized.tar.gz"
    lxc info c2 | grep snap0
    lxc start c2
    lxc stop c2 --force

    lxc restore c2 snap0
    lxc start c2
    lxc delete --force c2
  fi

  # Test hyphenated container and snapshot names
  lxc launch testimage c1-foo
  lxc snapshot c1-foo c1-foo-snap0

  lxc export c1-foo "${LXD_DIR}/c1-foo.tar.gz"
  lxc delete --force c1-foo

  lxc import "${LXD_DIR}/c1-foo.tar.gz"
  lxc delete --force c1-foo

  default_pool="$(lxc profile device get default root pool)"

  # Create new storage pools
  lxc storage create pool_1 dir
  lxc storage create pool_2 dir

  # Export created container
  lxc init testimage c3 -s pool_1
  lxc export c3 "${LXD_DIR}/c3.tar.gz"

  # Remove container and storage pool
  lxc rm -f c3
  lxc storage delete pool_1

  # This should succeed as it will fall back on the default pool
  lxc import "${LXD_DIR}/c3.tar.gz"

  lxc rm -f c3

  # Remove root device
  lxc profile device remove default root

  # This should fail as the expected storage is not available, and there is no default
  ! lxc import "${LXD_DIR}/c3.tar.gz" || false

  # Specify pool explicitly; this should fails as the pool doesn't exist
  ! lxc import "${LXD_DIR}/c3.tar.gz" -s pool_1 || false

  # Specify pool explicitly
  lxc import "${LXD_DIR}/c3.tar.gz" -s pool_2

  lxc rm -f c3

  # Reset default storage pool
  lxc profile device add default root disk path=/ pool="${default_pool}"

  lxc storage delete pool_2

  if [ "$#" -ne 0 ]; then
    lxc image rm testimage
    lxc image rm testimage --project "$project-b"
    lxc project switch default
    lxc project delete "$project"
    lxc project delete "$project-b"
  fi
}

test_backup_export() {
  test_backup_export_with_project
  test_backup_export_with_project fooproject
}

test_backup_export_with_project() {
  project="default"

  if [ "$#" -ne 0 ]; then
    # Create a project
    project="$1"
    lxc project create "$project"
    lxc project switch "$project"

    deps/import-busybox --project "$project" --alias testimage

    # Add a root device to the default profile of the project
    pool="lxdtest-$(basename "${LXD_DIR}")"
    lxc profile device add default root disk path="/" pool="${pool}"
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc snapshot c1

  mkdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
    tar -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ ! -d "${LXD_DIR}/optimized/backup/snapshots" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  tar -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # check tarball content
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/container" ]
  [ ! -d "${LXD_DIR}/non-optimized/backup/snapshots" ]

  rm -rf "${LXD_DIR}/non-optimized/"* "${LXD_DIR}/optimized/"*

  # with snapshots

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage
    tar -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ -f "${LXD_DIR}/optimized/backup/snapshots/snap0.bin" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz"
  tar -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # check tarball content
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/container" ]
  [ -d "${LXD_DIR}/non-optimized/backup/snapshots/snap0" ]

  lxc delete --force c1
  rm -rf "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"

  # Check if hyphens cause issues when creating backups
  lxc launch testimage c1-foo
  lxc snapshot c1-foo

  lxc export c1-foo "${LXD_DIR}/c1-foo.tar.gz"

  lxc delete --force c1-foo

  if [ "$#" -ne 0 ]; then
    lxc image rm testimage
    lxc project switch default
    lxc project delete "$project"
  fi
}

test_backup_rename() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Not Found" ; then
    echo "invalid rename response for missing container"
    false
  fi

  lxc init testimage c1

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error:.*No such object" ; then
    echo "invalid rename response for missing backup"
    false
  fi

  # Create backup
  lxc query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/instances/c1/backups

  # All backups should be listed
  lxc query /1.0/instances/c1/backups | jq .'[0]' | grep instances/c1/backups/foo

  # The specific backup should exist
  lxc query /1.0/instances/c1/backups/foo

  # Rename the container which should rename the backup(s) as well
  lxc mv c1 c2

  # All backups should be listed
  lxc query /1.0/instances/c2/backups | jq .'[0]' | grep instances/c2/backups/foo

  # The specific backup should exist
  lxc query /1.0/instances/c2/backups/foo

  # The old backup should not exist
  ! lxc query /1.0/instances/c1/backups/foo || false

  lxc delete --force c2
}
