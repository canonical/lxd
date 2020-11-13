test_container_import() {
  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_IMPORT_DIR}"
  spawn_lxd "${LXD_IMPORT_DIR}" true
  (
    set -e

    kill_lxc() {
        pid=${1:-}
        [ -n "${pid}" ] || return
        ppid=$(ps -o ppid="" -p "${pid}")
        kill -9 "${pid}" || true

        [ -n "${ppid}" ] || return
        kill -9 "${ppid}" || true
    }

    # shellcheck disable=SC2030
    LXD_DIR=${LXD_IMPORT_DIR}
    lxd_backend=$(storage_backend "$LXD_DIR")

    ensure_import_testimage

    lxc init testimage ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    ! lxd import ctImport || false
    # Import without killing the running instance to test restoring control plane after DB corruption.
    lxd import ctImport --force
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd import ctImport --force
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    ! lxd import ctImport || false
    lxd import ctImport --force
    kill_lxc "${pid}"
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport'"
    ! lxd import ctImport || false
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances_snapshots WHERE name='snap0'"
    ! lxd import ctImport || false
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd import ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    # delete all snapshots from disk
    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    rm "${LXD_DIR}/containers/ctImport"
    if [ "$lxd_backend" != "dir" ] && [ "$lxd_backend" != "btrfs" ]; then
      rm -rf "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
    fi
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    [ -L "${LXD_DIR}/containers/ctImport" ] && [ -d "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers/ctImport" ]
    if [ "$lxd_backend" != "dir" ] && [ "$lxd_backend" != "btrfs" ]; then
      [ -L "${LXD_DIR}/snapshots/ctImport" ] && [ -d "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0" ]
    fi
    lxc start ctImport
    lxc delete --force ctImport

    # delete one snapshot from disk
    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    case "$lxd_backend" in
      btrfs)
        btrfs subvolume delete "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
        rm -rf "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
        ;;
      ceph)
        rbd unmap "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0" || true
        rbd snap unprotect "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0" || true
        rbd snap rm "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0"
        rm -f "${LXD_DIR}/snapshots/ctImport"
        ;;
      dir)
        rm -r "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
        ;;
      lvm)
        lvremove -f "lxdtest-$(basename "${LXD_DIR}")/containers_ctImport-snap0"
        rm -rf "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
        ;;
      zfs)
        zfs destroy "lxdtest-$(basename "${LXD_DIR}")/containers/ctImport@snapshot-snap0"
        ;;
    esac
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    ! lxd import ctImport || false
    lxd import ctImport --force
    ! lxc info ctImport | grep snap0 || false
    lxc start ctImport
    lxc delete --force ctImport
    # FIXME: the daemon code should get rid of this leftover db entry
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport/snap0'"
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

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: not found" ; then
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
