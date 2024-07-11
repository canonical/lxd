test_storage_volume_recover() {
  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_IMPORT_DIR}"
  spawn_lxd "${LXD_IMPORT_DIR}" true

  poolName=$(lxc profile device get default root pool)
  poolDriver=$(lxc storage show "${poolName}" | awk '/^driver:/ {print $2}')

  # Create custom block volume.
  lxc storage volume create "${poolName}" vol1 --type=block

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
    lxc storage volume create "${poolName}" vol3

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
  lxc storage volume show "${poolName}" vol1 | grep -q 'content_type: block'
  lxc storage volume show "${poolName}" vol2 | grep -q 'content_type: iso'

  if [ "$poolDriver" = "zfs" ]; then
    # Ensure custom storage volumes have been recovered.
    lxc storage volume show "${poolName}" vol3 | grep -q 'content_type: filesystem'
    lxc storage volume show "${poolName}" vol4 | grep -q 'content_type: filesystem'

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
    cat <<EOF | lxd recover | grep "No unknown storage pools or volumes found. Nothing to do."
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
    [ "$(lxc exec c1 --project test -- cat /mnt/test.txt)" = "hello world" ]
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
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc launch testimage c2
  lxc snapshot c2

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
  old_snap0_uuid="$(lxc storage volume get "${pool}" container/c2/snap0 volatile.uuid)"
  lxc export c2 "${LXD_DIR}/c2.tar.gz"
  lxc delete --force c2

  lxc import "${LXD_DIR}/c2.tar.gz"
  lxc import "${LXD_DIR}/c2.tar.gz" c3
  lxc info c2 | grep snap0
  lxc info c3 | grep snap0

  # Check if the imported instance and its snapshot have a new UUID.
  [ -n "$(lxc storage volume get "${pool}" container/c2 volatile.uuid)" ]
  [ -n "$(lxc storage volume get "${pool}" container/c2/snap0 volatile.uuid)" ]
  [ "$(lxc storage volume get "${pool}" container/c2 volatile.uuid)" != "${old_uuid}" ]
  [ "$(lxc storage volume get "${pool}" container/c2/snap0 volatile.uuid)" != "${old_snap0_uuid}" ]

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


  if storage_backend_optimized_backup "$lxd_backend"; then
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
  [ "$(lxc storage volume get "${default_pool}" container/c1-foo user.foo)" = "post-c1-foo-snap1" ]
  [ "$(lxc storage volume get "${default_pool}" container/c1-foo/c1-foo-snap0 user.foo)" = "c1-foo-snap0" ]
  [ "$(lxc storage volume get "${default_pool}" container/c1-foo/c1-foo-snap1 user.foo)" = "c1-foo-snap1" ]
  lxc delete --force c1-foo

  # Create new storage pools
  lxc storage create pool_1 dir
  lxc storage create pool_2 dir

  # Export created container
  lxc init testimage c3 -s pool_1
  lxc export c3 "${LXD_DIR}/c3.tar.gz"

  # Remove container and storage pool
  lxc delete -f c3
  lxc storage delete pool_1

  # This should succeed as it will fall back on the default pool
  lxc import "${LXD_DIR}/c3.tar.gz"

  lxc delete -f c3

  # Remove root device
  lxc profile device remove default root

  # This should fail as the expected storage is not available, and there is no default
  ! lxc import "${LXD_DIR}/c3.tar.gz" || false

  # Specify pool explicitly; this should fails as the pool doesn't exist
  ! lxc import "${LXD_DIR}/c3.tar.gz" -s pool_1 || false

  # Specify pool explicitly
  lxc import "${LXD_DIR}/c3.tar.gz" -s pool_2

  lxc delete -f c3

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
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc snapshot c1

  mkdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
    tar -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    ls -l "${LXD_DIR}/optimized/backup/"
    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ ! -d "${LXD_DIR}/optimized/backup/snapshots" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  tar -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # check tarball content
  ls -l "${LXD_DIR}/non-optimized/backup/"
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/container" ]
  [ ! -d "${LXD_DIR}/non-optimized/backup/snapshots" ]

  rm -rf "${LXD_DIR}/non-optimized/"* "${LXD_DIR}/optimized/"*

  # with snapshots
  if storage_backend_optimized_backup "$lxd_backend"; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage
    tar -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    ls -l "${LXD_DIR}/optimized/backup/"
    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ -f "${LXD_DIR}/optimized/backup/snapshots/snap0.bin" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz"
  tar -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # check tarball content
  ls -l "${LXD_DIR}/non-optimized/backup/"
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

  # Cleanup exported tarballs
  rm -f "${LXD_DIR}"/c*.tar.gz

  if [ "$#" -ne 0 ]; then
    lxc image rm testimage
    lxc project switch default
    lxc project delete "$project"
  fi
}

test_backup_rename() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Instance backup not found" ; then
    echo "invalid rename response for missing container"
    false
  fi

  lxc init testimage c1

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Instance backup not found" ; then
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

  if storage_backend_optimized_backup "$lxd_backend"; then
    # Create optimized backup without snapshots.
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar.gz" --volume-only --optimized-storage

    [ -f "${LXD_DIR}/testvol-optimized.tar.gz" ]

    # Extract backup tarball.
    tar -xzf "${LXD_DIR}/testvol-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    ls -l "${LXD_DIR}/optimized/backup/"
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
  ls -l "${LXD_DIR}/non-optimized/backup/"
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/volume" ]
  [ "$(cat "${LXD_DIR}/non-optimized/backup/volume/test")" = "bar" ]
  [ ! -d "${LXD_DIR}/non-optimized/backup/volume-snapshots" ]

  ! grep -q -- '- test-snap0' "${LXD_DIR}/non-optimized/backup/index.yaml" || false

  rm -rf "${LXD_DIR}/non-optimized/"*
  rm "${LXD_DIR}/testvol.tar.gz"

  if storage_backend_optimized_backup "$lxd_backend"; then
    # Create optimized backup with snapshots.
    lxc storage volume export "${custom_vol_pool}" testvol "${LXD_DIR}/testvol-optimized.tar.gz" --optimized-storage

    [ -f "${LXD_DIR}/testvol-optimized.tar.gz" ]

    # Extract backup tarball.
    tar -xzf "${LXD_DIR}/testvol-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    ls -l "${LXD_DIR}/optimized/backup/"
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
  ls -l "${LXD_DIR}/non-optimized/backup/"
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/volume" ]
  [ "$(cat "${LXD_DIR}/non-optimized/backup/volume/test")" = "bar" ]
  [ -d "${LXD_DIR}/non-optimized/backup/volume-snapshots/test-snap0" ]
  [  "$(cat "${LXD_DIR}/non-optimized/backup/volume-snapshots/test-snap0/test")" = "foo" ]

  grep -q -- '- test-snap0' "${LXD_DIR}/non-optimized/backup/index.yaml"

  rm -rf "${LXD_DIR}/non-optimized/"*

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
  rm -rf "${LXD_DIR}/non-optimized/"* "${LXD_DIR}/optimized/"*
  lxc storage volume detach "${custom_vol_pool}" testvol c1
  lxc storage volume detach "${custom_vol_pool}" testvol2 c1
  lxc storage volume rm "${custom_vol_pool}" testvol
  lxc storage volume rm "${custom_vol_pool}" testvol2
  lxc delete -f c1
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

  # Cleanup exported tarballs
  rm -f "${LXD_DIR}"/c*.tar.gz
}

test_backup_volume_expiry() {
  poolName=$(lxc profile device get default root pool)

  # Create custom volume.
  lxc storage volume create "${poolName}" vol1

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
  (
    set -e

    poolName=$(lxc profile device get default root pool)

    ensure_import_testimage
    ensure_has_localhost_remote "${LXD_ADDR}"

    # Create and export an instance.
    lxc launch testimage c1
    lxc export c1 "${LXD_DIR}/c1.tar.gz"
    lxc delete -f c1

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
    lxc delete -f c2
  )
}

test_backup_export_import_instance_only() {
  poolName=$(lxc profile device get default root pool)

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Create an instance with snapshot.
  lxc init testimage c1
  lxc snapshot c1

  # Export the instance and remove it.
  lxc export c1 "${LXD_DIR}/c1.tar.gz" --instance-only
  lxc delete -f c1

  # Import the instance from tarball.
  lxc import "${LXD_DIR}/c1.tar.gz"

  # Verify imported instance has no snapshots.
  [ "$(lxc query "/1.0/storage-pools/${poolName}/volumes/container/c1/snapshots" | jq "length == 0")" = "true" ]

  rm "${LXD_DIR}/c1.tar.gz"
  lxc delete -f c1
}
