test_storage_volume_recover() {
  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_IMPORT_DIR}" true

  poolName=$(lxc profile device get default root pool)
  poolDriver=$(lxc storage show "${poolName}" | awk '/^driver:/ {print $2}')

  if [ "${poolDriver}" = "pure" ]; then
    echo "==> SKIP: Storage driver does not support recovery"
    return
  fi

  # Create custom block volume.
  lxc storage volume create "${poolName}" vol1 --type=block size=32MiB

  # Import ISO.
  truncate -s 8MiB foo.iso
  lxc storage volume import "${poolName}" ./foo.iso vol2 --type=iso

  # Delete database entry of the created custom block volume.
  lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol1'"
  lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol2'"

  # Ensure the custom block volume is no longer listed.
  ! lxc storage volume show "${poolName}" vol1 || false
  ! lxc storage volume show "${poolName}" vol2 || false

  if [ "$poolDriver" = "zfs" ]; then
    # Create filesystem volume.
    lxc storage volume create "${poolName}" vol3 size=32MiB

    # Create block_mode enabled volume.
    lxc storage volume create "${poolName}" vol4 zfs.block_mode=true size=200MiB

    # Delete database entries of the created custom volumes.
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol3'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol4'"

    # Ensure the custom volumes are no longer listed.
    ! lxc storage volume show "${poolName}" vol3 || false
    ! lxc storage volume show "${poolName}" vol4 || false
  fi

  # Recover custom block volume.
  cat <<EOF | lxd recover
no
yes
yes
EOF

  # Ensure custom storage volume has been recovered.
  lxc storage volume show "${poolName}" vol1 | grep -xF 'content_type: block'
  lxc storage volume show "${poolName}" vol2 | grep -xF 'content_type: iso'

  if [ "$poolDriver" = "zfs" ]; then
    # Ensure custom storage volumes have been recovered.
    lxc storage volume show "${poolName}" vol3 | grep -xF 'content_type: filesystem'
    lxc storage volume show "${poolName}" vol4 | grep -xF 'content_type: filesystem'

    # Cleanup
    lxc storage volume delete "${poolName}" vol3
    lxc storage volume delete "${poolName}" vol4
  fi

  # Cleanup
  rm -f foo.iso
  lxc storage volume delete "${poolName}" vol1
  lxc storage volume delete "${poolName}" vol2
  shutdown_lxd "${LXD_IMPORT_DIR}"
}

test_storage_volume_recover_by_container() {
  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_IMPORT_DIR}" true

  poolName=$(lxc profile device get default root pool)
  poolDriver=$(lxc storage show "${poolName}" | awk '/^driver:/ {print $2}')

  # Create another storage pool.
  poolName2="${poolName}-2"
  lxc storage create "${poolName2}" "${poolDriver}"

  # Create container.
  ensure_import_testimage
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"

  # Create a custom volume and attach to the instance.
  lxc storage volume create "${poolName}" vol1 size=32MiB
  lxc storage volume snapshot "${poolName}" vol1
  lxc storage volume attach "${poolName}" vol1 c1 /mnt

  # Create a custom volume in a different pool and attach to the instance.
  lxc storage volume create "${poolName2}" vol2 size=32MiB
  lxc storage volume snapshot "${poolName2}" vol2
  lxc storage volume attach "${poolName2}" vol2 c1 /mnt2

  # Get the volume's UUIDs before deleting it's database entries.
  vol1_uuid="$(lxc storage volume get "${poolName}" vol1 volatile.uuid)"
  vol2_uuid="$(lxc storage volume get "${poolName2}" vol2 volatile.uuid)"

  # Delete database entries of the created custom volumes.
  lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol1'"
  lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol2'"

  # Ensure the custom volumes are no longer listed.
  ! lxc storage volume show "${poolName}" vol1 || false
  ! lxc storage volume show "${poolName2}" vol2 || false

  # Recover custom volumes.
  cat <<EOF | lxd recover
no
yes
yes
EOF

  # Ensure custom storage volumes have been recovered.
  lxc storage volume show "${poolName}" vol1 | grep -xF 'content_type: filesystem'
  lxc storage volume show "${poolName2}" vol2 | grep -xF 'content_type: filesystem'

  # Ensure the custom volumes still have the same UUIDs.
  # This validates that the custom storage volumes were recovered from the instance's backup config.
  [ "${vol1_uuid}" = "$(lxc storage volume get "${poolName}" vol1 volatile.uuid)" ]
  [ "${vol2_uuid}" = "$(lxc storage volume get "${poolName2}" vol2 volatile.uuid)" ]

  # Detach the custom volumes from the instance.
  lxc storage volume detach "${poolName}" vol1 c1
  lxc storage volume detach "${poolName2}" vol2 c1

  # Delete database entries of the created custom volumes.
  lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol1'"
  lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol2'"

  # Ensure the custom volumes are no longer listed.
  ! lxc storage volume show "${poolName}" vol1 || false
  ! lxc storage volume show "${poolName2}" vol2 || false

  # Recover custom volumes.
  cat <<EOF | lxd recover
no
yes
yes
EOF

  # Ensure custom storage volumes have been recovered.
  lxc storage volume show "${poolName}" vol1 | grep -xF 'content_type: filesystem'
  lxc storage volume show "${poolName2}" vol2 | grep -xF 'content_type: filesystem'

  # Check the custom volumes got different UUIDs.
  # This validates that the custom storage volumes were recovered by name which looses all of their configuration.
  [ "${vol1_uuid}" != "$(lxc storage volume get "${poolName}" vol1 volatile.uuid)" ]
  [ "${vol2_uuid}" != "$(lxc storage volume get "${poolName2}" vol2 volatile.uuid)" ]

  # Cleanup
  lxc storage volume delete "${poolName}" vol1
  lxc storage volume delete "${poolName2}" vol2
  lxc delete -f c1
  lxc storage delete "${poolName2}"
  shutdown_lxd "${LXD_IMPORT_DIR}"
}

test_container_recover() {
  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_IMPORT_DIR}" true

  (
    set -e

    # shellcheck disable=SC2030
    LXD_DIR=${LXD_IMPORT_DIR}
    lxd_backend=$(storage_backend "$LXD_DIR")

    if [ "${lxd_backend}" = "pure" ]; then
      echo "==> SKIP: Storage driver does not support recovery"
      return
    fi

    ensure_import_testimage

    poolName=$(lxc profile device get default root pool)
    poolDriver=$(lxc storage show "${poolName}" | awk '/^driver:/ {print $2}')

    lxc storage set "${poolName}" user.foo=bah
    lxc project create test -c features.images=false -c features.profiles=true -c features.storage.volumes=true
    lxc profile device add default root disk path=/ pool="${poolName}" --project test
    lxc profile device add default eth0 nic nictype=p2p --project test
    lxc project switch test

    # Basic no-op check.
    cat <<EOF | lxd recover | grep "No unknown storage pools or volumes found. Nothing to do."
no
yes
EOF

    # Recover container and custom volume that isn't mounted.
    lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
    lxc storage volume create "${poolName}" vol1_test size=32MiB
    lxc storage volume attach "${poolName}" vol1_test c1 /mnt
    lxc start c1
    lxc exec c1 --project test -- mount | grep /mnt
    echo "hello world" | lxc exec c1 --project test -- tee /mnt/test.txt
    [ "$(lxc exec c1 --project test -- cat /mnt/test.txt)" = "hello world" ]
    lxc stop -f c1
    lxc config set c1 snapshots.expiry 1d
    lxc snapshot c1
    lxc info c1
    snapshotExpiryDateBefore=$(lxc info c1 | grep -wF "snap0")

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

    # Check snapshot expiry date has been restored.
    snapshotExpiryDateAfter=$(lxc info c1 | grep -wF "snap0")
    [ "$snapshotExpiryDateBefore" = "$snapshotExpiryDateAfter" ]

    # Check custom volume accessible.
    lxc exec c1 --project test -- mount | grep /mnt
    [ "$(lxc exec c1 --project test -- cat /mnt/test.txt)" = "hello world" ]

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
    poolConfigBefore=$(lxd sql global --format csv "SELECT key,value FROM storage_pools_config JOIN storage_pools ON storage_pools.id = storage_pools_config.storage_pool_id WHERE storage_pools.name = '${poolName}' ORDER BY key")
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
      *)
        # nothing extra config needed
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
    poolConfigAfter=$(lxd sql global --format csv "SELECT key,value FROM storage_pools_config JOIN storage_pools ON storage_pools.id = storage_pools_config.storage_pool_id WHERE storage_pools.name = '${poolName}' ORDER BY key")
    echo "Before:"
    echo "${poolConfigBefore}"

    echo "After:"
    echo "${poolConfigAfter}"

    [ "${poolConfigBefore}" = "${poolConfigAfter}" ]
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

test_bucket_recover() {
  if ! command -v "minio" >/dev/null 2>&1; then
    echo "==> SKIP: Skip bucket recovery test due to missing minio"
    return
  fi

  (
    set -e

    poolName=$(lxc profile device get default root pool)
    poolDriver=$(lxc storage show "${poolName}" | awk '/^driver:/ {print $2}')
    bucketName="bucket123"

    # Skip ceph driver - ceph does not support storage buckets
    if [ "${poolDriver}" = "ceph" ]; then
      return 0
    fi

    # Create storage bucket
    lxc storage bucket create "${poolName}" "${bucketName}"

    # Create storage bucket keys
    key1=$(lxc storage bucket key create "${poolName}" "${bucketName}" key1 --role admin)
    key2=$(lxc storage bucket key create "${poolName}" "${bucketName}" key2 --role read-only)
    key1_accessKey=$(echo "$key1" | awk '/^Access key/ { print $3 }')
    key1_secretKey=$(echo "$key1" | awk '/^Secret key/ { print $3 }')
    key2_accessKey=$(echo "$key2" | awk '/^Access key/ { print $3 }')
    key2_secretKey=$(echo "$key2" | awk '/^Secret key/ { print $3 }')

    # Remove bucket from global DB
    lxd sql global "delete from storage_buckets where name = '${bucketName}'"

    # Recover bucket
    cat <<EOF | lxd recover
no
yes
yes
EOF

    # Verify bucket is recovered
    lxc storage bucket ls "${poolName}" --format compact | grep "${bucketName}"

    # Verify bucket key with role admin is recovered
    recoveredKey1=$(lxc storage bucket key show "${poolName}" "${bucketName}" "${key1_accessKey}")
    echo "${recoveredKey1}" | grep "role: admin"
    echo "${recoveredKey1}" | grep "access-key: ${key1_accessKey}"
    echo "${recoveredKey1}" | grep "secret-key: ${key1_secretKey}"

    # Verify bucket key with role read-only is recovered
    recoveredKey2=$(lxc storage bucket key show "${poolName}" "${bucketName}" "${key2_accessKey}")
    echo "${recoveredKey2}" | grep "role: read-only"
    echo "${recoveredKey2}" | grep "access-key: ${key2_accessKey}"
    echo "${recoveredKey2}" | grep "secret-key: ${key2_secretKey}"
  )
}

test_backup_import() {
  _backup_import_with_project
  _backup_import_with_project fooproject
}

_backup_import_with_project() {
  project="default"
  pool="lxdtest-$(basename "${LXD_DIR}")"

  if [ "$#" -ne 0 ]; then
    # Create a projects
    project="$1"
    lxc project create "$project"
    lxc project create "$project-b"
    lxc project switch "$project"

    deps/import-busybox --project "$project" --alias testimage
    deps/import-busybox --project "$project-b" --alias testimage

    # Add a root device to the default profile of the project
    lxc profile device add default root disk path="/" pool="${pool}"
    lxc profile device add default root disk path="/" pool="${pool}" --project "$project-b"
  fi

  ensure_import_testimage

  lxc launch testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc launch testimage c2 -d "${SMALL_ROOT_DISK}"

  # Check invalid snapshot names
  ! lxc snapshot c2 ".." || false
  ! lxc snapshot c2 "with/slash" || false
  ! lxc snapshot c2 "with space" || false

  # Check valid snapshot name with underscore can be exported + imported
  lxc snapshot c2 snap0-with_underscore

  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  # create backup
  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  lxc delete --force c1

  # import backup, and ensure it's valid and runnable
  lxc import "${LXD_DIR}/c1.tar.gz"
  lxc info c1
  lxc start c1
  lxc delete --force c1

  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc import "${LXD_DIR}/c1-optimized.tar.gz"
    lxc info c1
    lxc start c1
    lxc delete --force c1
  fi

  # with snapshots

  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc export c2 "${LXD_DIR}/c2-optimized.tar.gz" --optimized-storage
  fi

  old_uuid="$(lxc storage volume get "${pool}" container/c2 volatile.uuid)"
  old_snap0_uuid="$(lxc storage volume get "${pool}" container/c2/snap0-with_underscore volatile.uuid)"
  lxc export c2 "${LXD_DIR}/c2.tar.gz"
  lxc delete --force c2

  lxc import "${LXD_DIR}/c2.tar.gz"
  lxc import "${LXD_DIR}/c2.tar.gz" c3
  lxc info c2 | grep snap0-with_underscore
  lxc info c3 | grep snap0-with_underscore

  # Check if the imported instance and its snapshot have a new UUID.
  [ -n "$(lxc storage volume get "${pool}" container/c2 volatile.uuid)" ]
  [ -n "$(lxc storage volume get "${pool}" container/c2/snap0-with_underscore volatile.uuid)" ]
  [ "$(lxc storage volume get "${pool}" container/c2 volatile.uuid)" != "${old_uuid}" ]
  [ "$(lxc storage volume get "${pool}" container/c2/snap0-with_underscore volatile.uuid)" != "${old_snap0_uuid}" ]

  lxc start c2
  lxc start c3
  lxc stop c2 --force
  lxc stop c3 --force

  if [ "$#" -ne 0 ]; then
    # Import into different project (before deleting earlier import).
    lxc import "${LXD_DIR}/c2.tar.gz" --project "$project-b"
    lxc import "${LXD_DIR}/c2.tar.gz" --project "$project-b" c3
    lxc info c2 --project "$project-b" | grep snap0-with_underscore
    lxc info c3 --project "$project-b" | grep snap0-with_underscore
    lxc start c2 --project "$project-b"
    lxc start c3 --project "$project-b"
    lxc stop c2 --project "$project-b" --force
    lxc stop c3 --project "$project-b" --force
    lxc restore c2 snap0-with_underscore --project "$project-b"
    lxc restore c3 snap0-with_underscore --project "$project-b"
    lxc delete --force c2 --project "$project-b"
    lxc delete --force c3 --project "$project-b"
  fi

  lxc restore c2 snap0-with_underscore
  lxc restore c3 snap0-with_underscore
  lxc start c2
  lxc start c3
  lxc delete --force c2
  lxc delete --force c3


  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc import "${LXD_DIR}/c2-optimized.tar.gz"
    lxc import "${LXD_DIR}/c2-optimized.tar.gz" c3
    lxc info c2 | grep snap0-with_underscore
    lxc info c3 | grep snap0-with_underscore
    lxc start c2
    lxc start c3
    lxc stop c2 --force
    lxc stop c3 --force
    lxc restore c2 snap0-with_underscore
    lxc restore c3 snap0-with_underscore
    lxc start c2
    lxc start c3
    lxc delete --force c2
    lxc delete --force c3
  fi

  # Test exporting container and snapshot names that container hyphens.
  # Also check that the container storage volume config is correctly captured and restored.
  default_pool="$(lxc profile device get default root pool)"

  lxc launch testimage c1-foo -d "${SMALL_ROOT_DISK}"
  lxc storage volume set "${default_pool}" container/c1-foo user.foo=c1-foo-snap0
  lxc snapshot c1-foo c1-foo-snap0
  lxc storage volume set "${default_pool}" container/c1-foo user.foo=c1-foo-snap1
  lxc snapshot c1-foo c1-foo-snap1
  lxc storage volume set "${default_pool}" container/c1-foo user.foo=post-c1-foo-snap1

  lxc export c1-foo "${LXD_DIR}/c1-foo.tar.gz"
  lxc delete --force c1-foo

  lxc import "${LXD_DIR}/c1-foo.tar.gz"
  lxc storage volume ls "${default_pool}"
  [ "$(lxc storage volume get "${default_pool}" container/c1-foo user.foo)" = "post-c1-foo-snap1" ]
  [ "$(lxc storage volume get "${default_pool}" container/c1-foo/c1-foo-snap0 user.foo)" = "c1-foo-snap0" ]
  [ "$(lxc storage volume get "${default_pool}" container/c1-foo/c1-foo-snap1 user.foo)" = "c1-foo-snap1" ]
  lxc delete --force c1-foo

  # Create new storage pools
  lxc storage create pool_1 dir
  lxc storage create pool_2 dir

  # Export created container
  lxc init --empty c3 -d "${SMALL_ROOT_DISK}" -s pool_1
  lxc export c3 "${LXD_DIR}/c3.tar.gz"

  # Remove container and storage pool
  lxc delete c3
  lxc storage delete pool_1

  # This should succeed as it will fall back on the default pool
  lxc import "${LXD_DIR}/c3.tar.gz"

  lxc delete c3

  # Remove root device
  lxc profile device remove default root

  # This should fail as the expected storage is not available, and there is no default
  ! lxc import "${LXD_DIR}/c3.tar.gz" || false

  # Specify pool explicitly; this should fails as the pool doesn't exist
  ! lxc import "${LXD_DIR}/c3.tar.gz" -s pool_1 || false

  # Specify pool explicitly
  lxc import "${LXD_DIR}/c3.tar.gz" -s pool_2

  lxc delete c3

  # Reset default storage pool
  lxc profile device add default root disk path=/ pool="${default_pool}"

  lxc storage delete pool_2

  # Cleanup exported tarballs
  rm -f "${LXD_DIR}"/c*.tar.gz

  if [ "$#" -ne 0 ]; then
    lxc image rm testimage
    lxc image rm testimage --project "$project-b"
    lxc project switch default
    lxc project delete "$project"
    lxc project delete "$project-b"
  fi
}

test_backup_export() {
  _backup_export_with_project
  _backup_export_with_project fooproject
}

_backup_export_with_project() {
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

  lxc launch testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1

  mkdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
    tar --warning=no-timestamp -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    ls -l "${LXD_DIR}/optimized/backup/"
    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ ! -d "${LXD_DIR}/optimized/backup/snapshots" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  tar --warning=no-timestamp -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # check tarball content
  ls -l "${LXD_DIR}/non-optimized/backup/"
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/container" ]
  [ ! -d "${LXD_DIR}/non-optimized/backup/snapshots" ]

  rm -rf "${LXD_DIR}/non-optimized/"* "${LXD_DIR}/optimized/"*

  # with snapshots
  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage
    tar --warning=no-timestamp -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    ls -l "${LXD_DIR}/optimized/backup/"
    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ -f "${LXD_DIR}/optimized/backup/snapshots/snap0.bin" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz"
  tar --warning=no-timestamp -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # check tarball content
  ls -l "${LXD_DIR}/non-optimized/backup/"
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/container" ]
  [ -d "${LXD_DIR}/non-optimized/backup/snapshots/snap0" ]

  lxc delete --force c1
  rm -rf "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"

  # Check if hyphens cause issues when creating backups
  lxc init --empty c1-foo -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1-foo

  lxc export c1-foo "${LXD_DIR}/c1-foo.tar.gz"

  lxc delete c1-foo

  # Cleanup exported tarballs
  rm -f "${LXD_DIR}"/c*.tar.gz

  if [ "$#" -ne 0 ]; then
    lxc image rm testimage
    lxc project switch default
    lxc project delete "$project"
  fi
}

test_backup_rename() {
  OUTPUT="$(! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 || false)"
  if ! echo "${OUTPUT}" | grep -F "Error: Instance backup not found" ; then
    echo "invalid rename response for missing container"
    false
  fi

  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"

  OUTPUT="$(! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 || false)"
  if ! echo "${OUTPUT}" | grep -F "Error: Instance backup not found" ; then
    echo "invalid rename response for missing backup"
    false
  fi

  # Create backup
  lxc query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/instances/c1/backups

  # All backups should be listed
  [ "$(lxc query /1.0/instances/c1/backups | jq -r '.[]')" = "/1.0/instances/c1/backups/foo" ]

  # The specific backup should exist
  lxc query /1.0/instances/c1/backups/foo

  # Rename the container which should rename the backup(s) as well
  lxc mv c1 c2

  # All backups should be listed
  [ "$(lxc query /1.0/instances/c2/backups | jq -r '.[]')" = "/1.0/instances/c2/backups/foo" ]

  # The specific backup should exist
  lxc query /1.0/instances/c2/backups/foo

  # The old backup should not exist
  ! lxc query /1.0/instances/c1/backups/foo || false

  lxc delete --force c2
}

test_backup_volume_export() {
  _backup_volume_export_with_project default "lxdtest-$(basename "${LXD_DIR}")"
  _backup_volume_export_with_project fooproject "lxdtest-$(basename "${LXD_DIR}")"

  if [ "$lxd_backend" = "ceph" ] && [ -n "${LXD_CEPH_CEPHFS:-}" ]; then
    custom_vol_pool="lxdtest-$(basename "${LXD_DIR}")-cephfs"
    lxc storage create "${custom_vol_pool}" cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")-cephfs"

    _backup_volume_export_with_project default "${custom_vol_pool}"
    _backup_volume_export_with_project fooproject "${custom_vol_pool}"

    lxc storage rm "${custom_vol_pool}"
  fi
}

_backup_volume_export_with_project() {
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
  else
    ensure_import_testimage
  fi

  mkdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized" "${LXD_DIR}/optimized-none" "${LXD_DIR}/optimized-squashfs" "${LXD_DIR}/non-optimized-none" "${LXD_DIR}/non-optimized-squashfs"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # Create test container.
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"

  # Create custom storage volume.
  lxc storage volume create "${custom_vol_pool}" testvol size=32MiB

  # Attach storage volume to the test container and start.
  lxc storage volume attach "${custom_vol_pool}" testvol c1 /mnt
  lxc start c1

  # Create file on the custom volume.
  echo foo | lxc file push - c1/mnt/test
  LXC_LOCAL='' lxc_remote exec c1 -- sync /mnt/test

  # Snapshot the custom volume.
  lxc storage volume set "${custom_vol_pool}" testvol user.foo=test-snap0
  lxc storage volume snapshot "${custom_vol_pool}" testvol test-snap0

  # Change the content (the snapshot will contain the old value).
  echo bar | lxc file push - c1/mnt/test
  LXC_LOCAL='' lxc_remote exec c1 -- sync /mnt/test

  lxc storage volume set "${custom_vol_pool}" testvol user.foo=test-snap1
  lxc storage volume snapshot "${custom_vol_pool}" testvol test-snap1
  lxc storage volume set "${custom_vol_pool}" testvol user.foo=post-test-snap1

  if storage_backend_optimized_backup "$lxd_backend"; then
    # Create optimized backups without snapshots.
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar.gz" --volume-only --optimized-storage
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar" --volume-only --optimized-storage --compression none
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.squashfs" --volume-only --optimized-storage --compression squashfs

    # Extract backups.
    tar --warning=no-timestamp -xzf "${LXD_DIR}/testvol-optimized.tar.gz" -C "${LXD_DIR}/optimized"
    tar --warning=no-timestamp -xf "${LXD_DIR}/testvol-optimized.tar" -C "${LXD_DIR}/optimized-none"
    unsquashfs -f -d "${LXD_DIR}/optimized-squashfs" "${LXD_DIR}/testvol-optimized.squashfs"

    # Check extracted content.
    for d in optimized optimized-none optimized-squashfs; do
      ls -l "${LXD_DIR}/${d}/backup/"
      [ -f "${LXD_DIR}/${d}/backup/index.yaml" ]
      [ -f "${LXD_DIR}/${d}/backup/volume.bin" ]
      [ ! -d "${LXD_DIR}/${d}/backup/volume-snapshots" ]

      ! grep -F -- '- test-snap0' "${LXD_DIR}/${d}/backup/index.yaml" || false
    done
  fi

  # Create non-optimized backups without snapshots.
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.tar.gz" --volume-only
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.tar" --volume-only --compression none
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.squashfs" --volume-only --compression squashfs

  # Extract non-optimized backups.
  tar --warning=no-timestamp -xzf "${LXD_DIR}/testvol.tar.gz" -C "${LXD_DIR}/non-optimized"
  tar --warning=no-timestamp -xf "${LXD_DIR}/testvol.tar" -C "${LXD_DIR}/non-optimized-none"
  unsquashfs -f -d "${LXD_DIR}/non-optimized-squashfs" "${LXD_DIR}/testvol.squashfs"

  # Check extracted content.
  for d in non-optimized non-optimized-none non-optimized-squashfs; do
    ls -l "${LXD_DIR}/${d}/backup/"
    [ -f "${LXD_DIR}/${d}/backup/index.yaml" ]
    [ -d "${LXD_DIR}/${d}/backup/volume" ]
    [ "$(< "${LXD_DIR}/${d}/backup/volume/test")" = "bar" ]
    [ ! -d "${LXD_DIR}/${d}/backup/volume-snapshots" ]

    ! grep -F -- '- test-snap0' "${LXD_DIR}/${d}/backup/index.yaml" || false
  done

  rm "${LXD_DIR}/testvol.tar.gz" "${LXD_DIR}/testvol.tar" "${LXD_DIR}/testvol.squashfs"

  if storage_backend_optimized_backup "$lxd_backend"; then
    # Create optimized backups with snapshots.
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar.gz" --optimized-storage
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar" --optimized-storage --compression none
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.squashfs" --optimized-storage --compression squashfs

    # Extract backups.
    tar --warning=no-timestamp -xzf "${LXD_DIR}/testvol-optimized.tar.gz" -C "${LXD_DIR}/optimized"
    tar --warning=no-timestamp -xf "${LXD_DIR}/testvol-optimized.tar" -C "${LXD_DIR}/optimized-none"
    unsquashfs -f -d "${LXD_DIR}/optimized-squashfs" "${LXD_DIR}/testvol-optimized.squashfs"

    # Check extracted content.
    for d in optimized optimized-none optimized-squashfs; do
      ls -l "${LXD_DIR}/${d}/backup/"
      [ -f "${LXD_DIR}/${d}/backup/index.yaml" ]
      [ -f "${LXD_DIR}/${d}/backup/volume.bin" ]
      [ -f "${LXD_DIR}/${d}/backup/volume-snapshots/test-snap0.bin" ]
    done
  fi

  # Create non-optimized backups with snapshots.
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.tar.gz"
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.tar" --compression none
  lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol.squashfs" --compression squashfs

  # Extract backups.
  tar --warning=no-timestamp -xzf "${LXD_DIR}/testvol.tar.gz" -C "${LXD_DIR}/non-optimized"
  tar --warning=no-timestamp -xf "${LXD_DIR}/testvol.tar" -C "${LXD_DIR}/non-optimized-none"
  unsquashfs -f -d "${LXD_DIR}/non-optimized-squashfs" "${LXD_DIR}/testvol.squashfs"

  # Check extracted content.
  for d in non-optimized non-optimized-none non-optimized-squashfs; do
    ls -l "${LXD_DIR}/${d}/backup/"
    [ -f "${LXD_DIR}/${d}/backup/index.yaml" ]
    [ -d "${LXD_DIR}/${d}/backup/volume" ]
    [ "$(< "${LXD_DIR}/${d}/backup/volume/test")" = "bar" ]
    [ -d "${LXD_DIR}/${d}/backup/volume-snapshots/test-snap0" ]
    [ "$(< "${LXD_DIR}/${d}/backup/volume-snapshots/test-snap0/test")" = "foo" ]

    grep -F -- '- test-snap0' "${LXD_DIR}/${d}/backup/index.yaml"
  done

  old_uuid="$(lxc storage volume get "${custom_vol_pool}" testvol volatile.uuid)"
  old_snap0_uuid="$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 volatile.uuid)"
  old_snap1_uuid="$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 volatile.uuid)"

  # Test non-optimized import.
  lxc stop -f c1
  lxc storage volume detach "${custom_vol_pool}" testvol c1
  lxc storage volume delete "${custom_vol_pool}" testvol
  lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol.tar.gz"
  lxc storage volume ls "${custom_vol_pool}"
  [ "$(lxc storage volume get "${custom_vol_pool}" testvol user.foo)" = "post-test-snap1" ]
  lxc storage volume show "${custom_vol_pool}" testvol/test-snap0
  [ "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 user.foo)" = "test-snap0" ]
  [ "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 user.foo)" = "test-snap1" ]

  # Check if the imported volume and its snapshots have a new UUID.
  [ -n "$(lxc storage volume get "${custom_vol_pool}" testvol volatile.uuid)" ]
  [ -n "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 volatile.uuid)" ]
  [ -n "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 volatile.uuid)" ]
  [ "$(lxc storage volume get "${custom_vol_pool}" testvol volatile.uuid)" != "${old_uuid}" ]
  [ "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 volatile.uuid)" != "${old_snap0_uuid}" ]
  [ "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 volatile.uuid)" != "${old_snap1_uuid}" ]

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
  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc storage volume detach "${custom_vol_pool}" testvol c1
    lxc storage volume detach "${custom_vol_pool}" testvol2 c1
    lxc storage volume delete "${custom_vol_pool}" testvol
    lxc storage volume delete "${custom_vol_pool}" testvol2
    lxc storage volume import "${custom_vol_pool}" "${LXD_DIR}/testvol-optimized.tar.gz"
    lxc storage volume ls "${custom_vol_pool}"
    [ "$(lxc storage volume get "${custom_vol_pool}" testvol user.foo)" = "post-test-snap1" ]
    [ "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap0 user.foo)" = "test-snap0" ]
    [ "$(lxc storage volume get "${custom_vol_pool}" testvol/test-snap1 user.foo)" = "test-snap1" ]

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
  rm -rf "${LXD_DIR}/non-optimized/"* "${LXD_DIR}/optimized/"* "${LXD_DIR}/non-optimized-none/"* "${LXD_DIR}/optimized-none/"* "${LXD_DIR}/non-optimized-squashfs/"* "${LXD_DIR}/optimized-squashfs/"*
  lxc storage volume detach "${custom_vol_pool}" testvol c1
  lxc storage volume detach "${custom_vol_pool}" testvol2 c1
  lxc storage volume rm "${custom_vol_pool}" testvol
  lxc storage volume rm "${custom_vol_pool}" testvol2
  lxc delete -f c1
  rmdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized" "${LXD_DIR}/optimized-none" "${LXD_DIR}/non-optimized-none" "${LXD_DIR}/non-optimized-squashfs" "${LXD_DIR}/optimized-squashfs"

  if [ "${project}" != "default" ]; then
    lxc project switch default
    lxc image rm testimage --project "$project"
    lxc image rm testimage --project "$project-b"
    lxc project delete "$project"
    lxc project delete "$project-b"
  fi
}

test_backup_volume_rename_delete() {
  pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create test volume.
  lxc storage volume create "${pool}" vol1 size=32MiB

  OUTPUT="$(! lxc query -X POST /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 || false)"
  if ! echo "${OUTPUT}" | grep -F "Error: Storage volume backup not found" ; then
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

test_backup_instance_uuid() {
  echo "==> Checking instance UUIDs during backup operation"
  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"
  initialUUID=$(lxc config get c1 volatile.uuid)
  initialGenerationID=$(lxc config get c1 volatile.uuid.generation)

  # export and import should preserve the UUID and generation UUID
  lxc export c1 "${LXD_DIR}/c1.tar.gz"
  lxc delete c1
  lxc import "${LXD_DIR}/c1.tar.gz"

  newUUID=$(lxc config get c1 volatile.uuid)
  newGenerationID=$(lxc config get c1 volatile.uuid.generation)

  if [ "${initialGenerationID}" != "${newGenerationID}" ] || [ "${initialUUID}" != "${newUUID}" ]; then
    echo "==> UUID and generation UUID of the instance should remain the same after importing the backup file"
    false
  fi

  lxc delete c1

  # Cleanup exported tarballs
  rm -f "${LXD_DIR}"/c*.tar.gz
}

test_backup_volume_expiry() {
  poolName=$(lxc profile device get default root pool)

  # Create custom volume.
  lxc storage volume create "${poolName}" vol1 size=32MiB

  # Create storage volume backups using the API directly.
  # The first one is created with an expiry date, the second one never expires.
  lxc query -X POST -d '{\"expires_at\":\"2023-07-17T00:00:00Z\"}' /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups
  lxc query -X POST -d '{}' /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups

  # Check that both backups are listed.
  [ "$(lxc query /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups | jq '.[]' | wc -l)" -eq 2 ]

  # Restart LXD which will trigger the task which removes expired volume backups.
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # Check that there's only one backup remaining.
  [ "$(lxc query /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups | jq '.[]' | wc -l)" -eq 1 ]

  # Cleanup.
  lxc storage volume delete "${poolName}" vol1
}

test_backup_export_import_recover() {
  lxd_backend=$(storage_backend "$LXD_DIR")

  if [ "$lxd_backend" = "pure" ]; then
    echo "==> SKIP: Storage driver does not support recovery"
    return
  fi

  (
    set -e

    poolName=$(lxc profile device get default root pool)

    # Create and export an instance.
    lxc init --empty c1 -d "${SMALL_ROOT_DISK}"
    lxc export c1 "${LXD_DIR}/c1.tar.gz"
    lxc delete c1

    # Import instance and remove no longer required tarball.
    lxc import "${LXD_DIR}/c1.tar.gz" c2
    rm "${LXD_DIR}/c1.tar.gz"

    # Remove imported instance enteries from database.
    lxd sql global "delete from instances where name = 'c2'"
    lxd sql global "delete from storage_volumes where name = 'c2'"

    # Recover removed instance.
    cat <<EOF | lxd recover
no
yes
yes
EOF

    # Remove recovered instance.
    lxc delete c2
  )
}

test_backup_export_import_instance_only() {
  poolName=$(lxc profile device get default root pool)

  # Create an instance with snapshot.
  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1

  # Verify the original instance has snapshots.
  [ "$(lxc query "/1.0/storage-pools/${poolName}/volumes/container/c1/snapshots" | jq -r 'length')" = "1" ]

  # Export the instance and remove it.
  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  lxc delete c1

  # Import the instance from tarball.
  lxc import "${LXD_DIR}/c1.tar.gz"

  # Verify imported instance has no snapshots.
  [ "$(lxc query "/1.0/storage-pools/${poolName}/volumes/container/c1/snapshots" | jq -r 'length')" = "0" ]

  rm "${LXD_DIR}/c1.tar.gz"
  lxc delete c1
}

test_backup_metadata() {
  ensure_import_testimage

  # Fetch the least and most recent supported backup metadata version from the range.
  lowest_version=$(lxc query /1.0 | jq -r .environment.backup_metadata_version_range[0])
  highest_version=$(lxc query /1.0 | jq -r .environment.backup_metadata_version_range[1])

  [ "$lowest_version" = "1" ]
  [ "$highest_version" = "2" ]

  tmpDir=$(mktemp -d -p "${TEST_DIR}" metadata-XXX)

  # Create an instance with one snapshot.
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1

  # Attach a disk from another pool with one snapshot.
  custom_vol_pool="lxdtest-$(basename "${LXD_DIR}")-dir"
  lxc storage create "${custom_vol_pool}" dir
  lxc storage volume create "${custom_vol_pool}" foo
  lxc storage volume snapshot "${custom_vol_pool}" foo
  lxc storage volume attach "${custom_vol_pool}" foo c1 path=/mnt
  [ "$(lxc query "/1.0/instances/c1" | jq '.expanded_devices | map(select(.type=="disk")) | length')" = "2" ]

  lxc start c1
  backup_yaml_path="${LXD_DIR}/containers/c1/backup.yaml"
  cat "${backup_yaml_path}"

  # Test the containers backup config contains the latest format.
  [ "$(yq -r '.snapshots | length' < "${backup_yaml_path}")" = "1" ]
  [ "$(yq -r .version < "${backup_yaml_path}")" = "${highest_version}" ]
  [ "$(yq -r '.volumes | length' < "${backup_yaml_path}")" = "2" ]
  [ "$(yq -r '.volumes.[0].snapshots | length' < "${backup_yaml_path}")" = "1" ]
  [ "$(yq -r '.volumes.[1].snapshots | length' < "${backup_yaml_path}")" = "1" ]
  [ "$(yq -r '.pools | length' < "${backup_yaml_path}")" = "2" ]

  # Test attaching the same vol a second time doesn't increase it's appearance in the backup config.
  lxc storage volume attach "${custom_vol_pool}" foo c1 foo2 /mnt2
  [ "$(lxc query "/1.0/instances/c1" | jq '.expanded_devices | map(select(.type=="disk")) | length')" = "3" ]
  [ "$(yq -r '.volumes | length' < "${backup_yaml_path}")" = "2" ]
  [ "$(yq -r '.volumes.[0].snapshots | length' < "${backup_yaml_path}")" = "1" ]
  [ "$(yq -r '.volumes.[1].snapshots | length' < "${backup_yaml_path}")" = "1" ]
  [ "$(yq -r '.pools | length' < "${backup_yaml_path}")" = "2" ]
  lxc storage volume detach "${custom_vol_pool}" foo c1 foo2

  # Test custom volume changes are reflected in the config file.
  lxc storage volume set "${custom_vol_pool}" foo user.foo bar # test volume config update
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .config."user.foo"' < "${backup_yaml_path}")" = "bar" ]
  lxc storage volume unset "${custom_vol_pool}" foo user.foo
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .config."user.foo"' < "${backup_yaml_path}")" = "null" ]
  [ "$(yq -r '.volumes | length' < "${backup_yaml_path}")" = "2" ]
  [ "$(yq -r '.pools | length' < "${backup_yaml_path}")" = "2" ]
  lxc storage volume detach "${custom_vol_pool}" foo c1 # test detaching/attaching vol and its effects on the list of vols and pools
  [ "$(yq -r '.volumes | length' < "${backup_yaml_path}")" = "1" ]
  [ "$(yq -r '.pools | length' < "${backup_yaml_path}")" = "1" ]
  lxc storage volume attach "${custom_vol_pool}" foo c1 path=/mnt
  [ "$(yq -r '.volumes | length' < "${backup_yaml_path}")" = "2" ]
  [ "$(yq -r '.pools | length' < "${backup_yaml_path}")" = "2" ]

  # Test custom volume snapshots changes are reflected in the config file.
  lxc storage volume snapshot "${custom_vol_pool}" foo # test snapshot creation
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots | length' < "${backup_yaml_path}")" = "2" ]
  lxc storage volume rm "${custom_vol_pool}" foo/snap1
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots | length' < "${backup_yaml_path}")" = "1" ]
  lxc storage volume rename "${custom_vol_pool}" foo/snap0 foo/snap00 # test snapshot rename
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots.[] | select(.name == "snap00") | .name' < "${backup_yaml_path}")" = "snap00" ]
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots.[] | select(.name == "snap0") | .name' < "${backup_yaml_path}")" = "" ]
  lxc storage volume rename "${custom_vol_pool}" foo/snap00 foo/snap0
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots.[] | select(.name == "snap0") | .name' < "${backup_yaml_path}")" = "snap0" ]
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots.[] | select(.name == "snap00") | .name' < "${backup_yaml_path}")" = "" ]
  lxc storage volume set "${custom_vol_pool}" foo/snap0 --property description bar # test snapshot update (only description can be updated on snaps)
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots.[] | select(.name == "snap0") | .description' < "${backup_yaml_path}")" = "bar" ]
  lxc storage volume unset "${custom_vol_pool}" foo/snap0 --property description
  [ "$(yq -r '.volumes.[] | select(.name == "foo" and .pool == "'"${custom_vol_pool}"'") | .snapshots.[] | select(.name == "snap0") | .description' < "${backup_yaml_path}")" = "" ]

  lxc stop -f c1

  # Export the instance without setting an export version.
  # The server should implicitly pick its latest supported version.
  lxc export c1 "${tmpDir}/c1.tar.gz"
  tar -xzf "${tmpDir}/c1.tar.gz" -C "${tmpDir}" --occurrence=1 backup/index.yaml

  cat "${tmpDir}/backup/index.yaml"
  [ "$(yq '.snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq .config.version < "${tmpDir}/backup/index.yaml")" = "${highest_version}" ]
  [ "$(yq '.config.volumes | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq '.config.volumes.[0].snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq '.config.pools | length' < "${tmpDir}/backup/index.yaml")" = "1" ]

  rm -rf "${tmpDir}/backup" "${tmpDir}/c1.tar.gz"

  # Export the instance using the specified lowest export version.
  # The server should used the provided version instead of its default.
  lxc export c1 "${tmpDir}/c1.tar.gz" --export-version "${lowest_version}"
  tar -xzf "${tmpDir}/c1.tar.gz" -C "${tmpDir}" --occurrence=1 backup/index.yaml

  cat "${tmpDir}/backup/index.yaml"
  [ "$(yq .config.version < "${tmpDir}/backup/index.yaml")" = "null" ]
  [ "$(yq .config.container < "${tmpDir}/backup/index.yaml")" != "null" ]
  [ "$(yq .config.pool < "${tmpDir}/backup/index.yaml")" != "null" ]
  [ "$(yq .config.volume < "${tmpDir}/backup/index.yaml")" != "null" ]
  [ "$(yq '.config.snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq '.config.volume_snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]

  rm -rf "${tmpDir}/backup" "${tmpDir}/c1.tar.gz"
  lxc delete c1

  # Create a custom storage volume with one snapshot.
  poolName=$(lxc profile device get default root pool)
  lxc storage volume create "${poolName}" vol1 size=32MiB
  lxc storage volume snapshot "${poolName}" vol1

  # Export the custom storage volume without setting an export version.
  # The server should implicitly pick its latest supported version.
  lxc storage volume export "${poolName}" vol1 "${tmpDir}/vol1.tar.gz"
  tar -xzf "${tmpDir}/vol1.tar.gz" -C "${tmpDir}" --occurrence=1 backup/index.yaml

  cat "${tmpDir}/backup/index.yaml"
  [ "$(yq '.snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq .config.version < "${tmpDir}/backup/index.yaml")" = "${highest_version}" ]
  [ "$(yq .config.instance < "${tmpDir}/backup/index.yaml")" = "null" ]
  [ "$(yq '.config.volumes | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq '.config.volumes.[0].snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq '.config.pools | length' < "${tmpDir}/backup/index.yaml")" = "1" ]

  rm -rf "${tmpDir}/backup" "${tmpDir}/vol1.tar.gz"

  # Export the custom storage volume using the specified lowest export version.
  # The server should used the provided version instead of its default.
  lxc storage volume export "${poolName}" vol1 "${tmpDir}/vol1.tar.gz" --export-version "${lowest_version}"
  tar -xzf "${tmpDir}/vol1.tar.gz" -C "${tmpDir}" --occurrence=1 backup/index.yaml

  cat "${tmpDir}/backup/index.yaml"
  [ "$(yq '.snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]
  [ "$(yq .config.version < "${tmpDir}/backup/index.yaml")" = "null" ]
  [ "$(yq .config.container < "${tmpDir}/backup/index.yaml")" = "null" ]
  [ "$(yq .config.volume < "${tmpDir}/backup/index.yaml")" != "null" ]
  [ "$(yq '.config.volume_snapshots | length' < "${tmpDir}/backup/index.yaml")" = "1" ]

  rm -rf "${tmpDir}/backup" "${tmpDir}/vol1.tar.gz"
  lxc storage volume delete "${poolName}" vol1
  lxc storage volume delete "${custom_vol_pool}" foo
  lxc storage delete "${custom_vol_pool}"

  rm -rf "${tmpDir}"
}
