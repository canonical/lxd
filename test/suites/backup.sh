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
    poolDriver=$(lxc storage show "${poolName}" | awk '/^driver:/ {print $2}')

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
    lxc exec c1 --project test -- grep -xF "hello world" /mnt/test.txt
    lxc stop -f c1
    lxc snapshot c1
    lxc info c1

    lxc storage volume snapshot "${poolName}" vol1_test snap0
    lxc storage volume show "${poolName}" vol1_test
    lxc storage volume show "${poolName}" vol1_test/snap0

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
    ! lxc storage volume show "${poolName}" container/c1 || false
    ! lxc storage volume show "${poolName}" container/c1/snap0 || false

    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      ! ls "${LXD_DIR}/storage-pools/${poolName}/containers/test_c1" || false
      ! ls "${LXD_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1" || false
    fi

    # Check custom volume appears removed.
    ! lxc storage volume show "${poolName}" vol1_test || false
    ! lxc storage volume show "${poolName}" vol1_test/snap0 || false

    # Shutdown LXD so pools are unmounted.
    shutdown_lxd "${LXD_DIR}"

    # Remove empty directory structures for pool drivers that don't have a mounted root.
    # This is so we can test the restoration of the storage pool directory structure.
    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      rm -rvf "${LXD_DIR}/storage-pools/${poolName}"
    fi

    respawn_lxd "${LXD_DIR}" true

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
    lxc storage volume show "${poolName}" vol1_test
    lxc storage volume show "${poolName}" vol1_test/snap0

    # Check snapshot exists and container can be started.
    lxc info c1 | grep snap0
    lxc storage volume ls "${poolName}"
    lxc storage volume show "${poolName}" container/c1
    lxc storage volume show "${poolName}" container/c1/snap0
    lxc start c1
    lxc exec c1 --project test -- hostname

    # Check custom volume accessible.
    lxc exec c1 --project test -- mount | grep /mnt
    lxc exec c1 --project test -- grep -xF "hello world" /mnt/test.txt

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

  # shellcheck disable=SC2031,2269
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
  lxc import "${LXD_DIR}/c2.tar.gz" c3
  lxc info c2 | grep snap0
  lxc info c3 | grep snap0
  lxc start c2
  lxc start c3
  lxc stop c2 --force
  lxc stop c3 --force

  if [ "$#" -ne 0 ]; then
    # Import into different project (before deleting earlier import).
    lxc import "${LXD_DIR}/c2.tar.gz" --project "$project-b"
    lxc import "${LXD_DIR}/c2.tar.gz" --project "$project-b" c3
    lxc info c2 --project "$project-b" | grep snap0
    lxc info c3 --project "$project-b" | grep snap0
    lxc start c2 --project "$project-b"
    lxc start c3 --project "$project-b"
    lxc stop c2 --project "$project-b" --force
    lxc stop c3 --project "$project-b" --force
    lxc restore c2 snap0 --project "$project-b"
    lxc restore c3 snap0 --project "$project-b"
    lxc delete --force c2 --project "$project-b"
    lxc delete --force c3 --project "$project-b"
  fi

  lxc restore c2 snap0
  lxc restore c3 snap0
  lxc start c2
  lxc start c3
  lxc delete --force c2
  lxc delete --force c3


  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc import "${LXD_DIR}/c2-optimized.tar.gz"
    lxc import "${LXD_DIR}/c2-optimized.tar.gz" c3
    lxc info c2 | grep snap0
    lxc info c3 | grep snap0
    lxc start c2
    lxc start c3
    lxc stop c2 --force
    lxc stop c3 --force
    lxc restore c2 snap0
    lxc restore c3 snap0
    lxc start c2
    lxc start c3
    lxc delete --force c2
    lxc delete --force c3
  fi

  # Test exporting container and snapshot names that container hyphens.
  # Also check that the container storage volume config is correctly captured and restored.
  default_pool="$(lxc profile device get default root pool)"

  lxc launch testimage c1-foo
  lxc storage volume set "${default_pool}" container/c1-foo user.foo=c1-foo-snap0
  lxc snapshot c1-foo c1-foo-snap0
  lxc storage volume set "${default_pool}" container/c1-foo user.foo=c1-foo-snap1
  lxc snapshot c1-foo c1-foo-snap1
  lxc storage volume set "${default_pool}" container/c1-foo user.foo=post-c1-foo-snap1

  lxc export c1-foo "${LXD_DIR}/c1-foo.tar.gz"
  lxc delete --force c1-foo

  lxc import "${LXD_DIR}/c1-foo.tar.gz"
  lxc storage volume ls "${default_pool}"
  lxc storage volume get "${default_pool}" container/c1-foo user.foo | grep -Fx "post-c1-foo-snap1"
  lxc storage volume get "${default_pool}" container/c1-foo/c1-foo-snap0 user.foo | grep -Fx "c1-foo-snap0"
  lxc storage volume get "${default_pool}" container/c1-foo/c1-foo-snap1 user.foo | grep -Fx "c1-foo-snap1"
  lxc delete --force c1-foo

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

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Instance not found" ; then
    echo "invalid rename response for missing container"
    false
  fi

  lxc init testimage c1

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Load backup from database: Instance backup not found" ; then
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

test_backup_volume_export() {
  test_backup_volume_export_with_project default "lxdtest-$(basename "${LXD_DIR}")"
  test_backup_volume_export_with_project fooproject "lxdtest-$(basename "${LXD_DIR}")"

  if [ "$lxd_backend" = "ceph" ] && [ -n "${LXD_CEPH_CEPHFS:-}" ]; then
    custom_vol_pool="lxdtest-$(basename "${LXD_DIR}")-cephfs"
    lxc storage create "${custom_vol_pool}" cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")-cephfs"

    test_backup_volume_export_with_project default "${custom_vol_pool}"
    test_backup_volume_export_with_project fooproject "${custom_vol_pool}"

    lxc storage rm "${custom_vol_pool}"
  fi
}

test_backup_volume_export_with_project() {
  pool="lxdtest-$(basename "${LXD_DIR}")"
  project="$1"
  custom_vol_pool="$2"

  if [ "${project}" != "default" ]; then
    # Create a project.
    lxc project create "$project"
    lxc project create "$project-b"
    lxc project switch "$project"

    deps/import-busybox --project "$project" --alias testimage
    deps/import-busybox --project "$project-b" --alias testimage

    # Add a root device to the default profile of the project.
    lxc profile device add default root disk path="/" pool="${pool}"
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  mkdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # Create test container.
  lxc init testimage c1

  # Create custom storage volume.
  lxc storage volume create "${custom_vol_pool}" testvol

  # Attach storage volume to the test container and start.
  lxc storage volume attach "${custom_vol_pool}" testvol c1 /mnt
  lxc start c1

  # Create file on the custom volume.
  echo foo | lxc file push - c1/mnt/test

  # Snapshot the custom volume.
  lxc storage volume set "${custom_vol_pool}" testvol user.foo=test-snap0
  lxc storage volume snapshot "${custom_vol_pool}" testvol test-snap0

  # Change the content (the snapshot will contain the old value).
  echo bar | lxc file push - c1/mnt/test

  lxc storage volume set "${custom_vol_pool}" testvol user.foo=test-snap1
  lxc storage volume snapshot "${custom_vol_pool}" testvol test-snap1
  lxc storage volume set "${custom_vol_pool}" testvol user.foo=post-test-snap1

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    # Create optimized backup without snapshots.
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar.gz" --volume-only --optimized-storage

    [ -f "${LXD_DIR}/testvol-optimized.tar.gz" ]

    # Extract backup tarball.
    tar -xzf "${LXD_DIR}/testvol-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/volume.bin" ]
    [ ! -d "${LXD_DIR}/optimized/backup/volume-snapshots" ]
  fi

  # Create non-optimized backup without snapshots.
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.tar.gz" --volume-only

  [ -f "${LXD_DIR}/testvol.tar.gz" ]

  # Extract non-optimized backup tarball.
  tar -xzf "${LXD_DIR}/testvol.tar.gz" -C "${LXD_DIR}/non-optimized"

  # Check tarball content.
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/volume" ]
  [ "$(cat "${LXD_DIR}/non-optimized/backup/volume/test")" = "bar" ]
  [ ! -d "${LXD_DIR}/non-optimized/backup/volume-snapshots" ]

  ! grep -q -- '- test-snap0' "${LXD_DIR}/non-optimized/backup/index.yaml" || false

  rm -rf "${LXD_DIR}/non-optimized/"*
  rm "${LXD_DIR}/testvol.tar.gz"

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    # Create optimized backup with snapshots.
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar.gz" --optimized-storage

    [ -f "${LXD_DIR}/testvol-optimized.tar.gz" ]

    # Extract backup tarball.
    tar -xzf "${LXD_DIR}/testvol-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/volume.bin" ]
    [ -f "${LXD_DIR}/optimized/backup/volume-snapshots/test-snap0.bin" ]
  fi

  # Create non-optimized backup with snapshots.
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.tar.gz"

  [ -f "${LXD_DIR}/testvol.tar.gz" ]

  # Extract backup tarball.
  tar -xzf "${LXD_DIR}/testvol.tar.gz" -C "${LXD_DIR}/non-optimized"

  # Check tarball content.
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/volume" ]
  [ "$(cat "${LXD_DIR}/non-optimized/backup/volume/test")" = "bar" ]
  [ -d "${LXD_DIR}/non-optimized/backup/volume-snapshots/test-snap0" ]
  [  "$(cat "${LXD_DIR}/non-optimized/backup/volume-snapshots/test-snap0/test")" = "foo" ]

  grep -q -- '- test-snap0' "${LXD_DIR}/non-optimized/backup/index.yaml"

  rm -rf "${LXD_DIR}/non-optimized/"*

  # Test non-optimized import.
  lxc stop -f c1
  lxc storage volume detach "${custom_vol_pool}" testvol c1
  lxc storage volume delete "${custom_vol_pool}" testvol
  lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol.tar.gz"
  lxc storage volume ls "${custom_vol_pool}"
  lxc storage volume get "${custom_vol_pool}" testvol user.foo | grep -Fx "post-test-snap1"
  lxc storage volume show "${custom_vol_pool}" testvol/test-snap0
  lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 user.foo | grep -Fx "test-snap0"
  lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 user.foo | grep -Fx "test-snap1"

  lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol.tar.gz" testvol2
  lxc storage volume attach "${custom_vol_pool}" testvol c1 /mnt
  lxc storage volume attach "${custom_vol_pool}" testvol2 c1 /mnt2
  lxc start c1
  lxc exec c1 --project "$project" -- stat /mnt/test
  lxc exec c1 --project "$project" -- stat /mnt2/test
  lxc stop -f c1

  if [ "${project}" != "default" ]; then
    # Import into different project (before deleting earlier import).
    lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol.tar.gz" --project "$project-b"
    lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol.tar.gz" --project "$project-b" testvol2
    lxc storage volume delete "${custom_vol_pool}" testvol --project "$project-b"
    lxc storage volume delete "${custom_vol_pool}" testvol2 --project "$project-b"
  fi

  # Test optimized import.
  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc storage volume detach "${custom_vol_pool}" testvol c1
    lxc storage volume detach "${custom_vol_pool}" testvol2 c1
    lxc storage volume delete "${custom_vol_pool}" testvol
    lxc storage volume delete "${custom_vol_pool}" testvol2
    lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol-optimized.tar.gz"
    lxc storage volume ls "${custom_vol_pool}"
    lxc storage volume get "${custom_vol_pool}" testvol user.foo | grep -Fx "post-test-snap1"
    lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 user.foo | grep -Fx "test-snap0"
    lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 user.foo | grep -Fx "test-snap1"

    lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol-optimized.tar.gz" testvol2
    lxc storage volume attach "${custom_vol_pool}" testvol c1 /mnt
    lxc storage volume attach "${custom_vol_pool}" testvol2 c1 /mnt2
    lxc start c1
    lxc exec c1 --project "$project" -- stat /mnt/test
    lxc exec c1 --project "$project" -- stat /mnt2/test
    lxc stop -f c1

    if [ "${project}" != "default" ]; then
      # Import into different project (before deleting earlier import).
      lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol-optimized.tar.gz" --project "$project-b"
      lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol-optimized.tar.gz" --project "$project-b" testvol2
      lxc storage volume delete "${custom_vol_pool}" testvol --project "$project-b"
      lxc storage volume delete "${custom_vol_pool}" testvol2 --project "$project-b"
    fi
  fi

  # Clean up.
  rm -rf "${LXD_DIR}/non-optimized/"* "${LXD_DIR}/optimized/"*
  lxc storage volume detach "${custom_vol_pool}" testvol c1
  lxc storage volume detach "${custom_vol_pool}" testvol2 c1
  lxc storage volume rm "${custom_vol_pool}" testvol
  lxc storage volume rm "${custom_vol_pool}" testvol2
  lxc rm -f c1
  rmdir "${LXD_DIR}/optimized"
  rmdir "${LXD_DIR}/non-optimized"

  if [ "${project}" != "default" ]; then
    lxc project switch default
    lxc image rm testimage --project "$project"
    lxc image rm testimage --project "$project-b"
    lxc project delete "$project"
    lxc project delete "$project-b"
  fi
}

test_backup_volume_rename_delete() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create test volume.
  lxc storage volume create "${pool}" vol1

  if ! lxc query -X POST /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Storage volume backup not found" ; then
    echo "invalid rename response for missing storage volume"
    false
  fi

  # Create backup.
  lxc query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups

  # All backups should be listed.
  lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups
  lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups | jq .'[0]' | grep storage-pools/"${pool}"/volumes/custom/vol1/backups/foo

  # The specific backup should exist.
  lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo
  stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol1/foo

  # Delete backup and check it is removed from DB and disk.
  lxc query -X DELETE --wait /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo
  ! lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo || false
  ! stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol1/foo || false
  ! stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol1 || false

  # Create backup again to test rename.
  lxc query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups

  # Rename the container which should rename the backup(s) as well.
  lxc storage volume rename "${pool}" vol1 vol2

  # All backups should be listed.
  lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups | jq .'[0]' | grep storage-pools/"${pool}"/volumes/custom/vol2/backups/foo

  # The specific backup should exist.
  lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups/foo
  stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol2/foo

  # The old backup should not exist.
  ! lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo || false
  ! stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol1/foo || false
  ! stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol1 || false

  # Rename backup itself and check its renamed in DB and on disk.
  lxc query -X POST --wait -d '{\"name\":\"foo2\"}' /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups/foo
  lxc query /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups | jq .'[0]' | grep storage-pools/"${pool}"/volumes/custom/vol2/backups/foo2
  stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol2/foo2
  ! stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol2/foo || false

  # Remove volume and check the backups are removed too.
  lxc storage volume rm "${pool}" vol2
  ! stat "${LXD_DIR}"/backups/custom/"${pool}"/default_vol2 || false
}

test_backup_different_instance_uuid() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  echo "==> Checking instances UUID during backup operation"
  lxc launch testimage c1
  initialUUID=$(lxc config get c1 volatile.uuid)
  initialGenerationID=$(lxc config get c1 volatile.uuid.generation)

  # export and import to trigger new UUID generation
  lxc export c1 "${LXD_DIR}/c1.tar.gz"
  lxc delete -f c1
  lxc import "${LXD_DIR}/c1.tar.gz"

  newUUID=$(lxc config get c1 volatile.uuid)
  newGenerationID=$(lxc config get c1 volatile.uuid.generation)

  if [ "${initialGenerationID}" != "${newGenerationID}" ] || [ "${initialUUID}" != "${newUUID}" ]; then
    echo "==> UUID of the instance should remain the same after importing the backup file"
    false
  fi

  lxc delete -f c1
}
