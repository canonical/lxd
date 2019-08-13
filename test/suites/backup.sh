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
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport/snap0'"
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
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport/snap0'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
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
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport/snap0'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport/snap0'"
    lxd import ctImport
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
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='ctImport/snap0'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    ! lxd import ctImport || false
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    [ -L "${LXD_DIR}/containers/ctImport" ] && [ -d "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers/ctImport" ]
    if [ "$lxd_backend" != "dir" ] && [ "$lxd_backend" != "btrfs" ]; then
      [ -L "${LXD_DIR}/snapshots/ctImport" ] && [ -d "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0" ]
    fi
    lxc start ctImport
    lxc delete --force ctImport

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
        ;;
      dir)
        rm -r "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
        ;;
      lvm)
        lvremove -f "lxdtest-$(basename "${LXD_DIR}")/containers_ctImport-snap0"
        ;;
      zfs)
        zfs destroy "lxdtest-$(basename "${LXD_DIR}")/containers/ctImport@snapshot-snap0"
        ;;
    esac
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    ! lxd import ctImport || false
    lxd import ctImport --force
    lxc info ctImport | grep snap0
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
        ;;
      dir)
        rm -r "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers-snapshots/ctImport/snap0"
        ;;
      lvm)
        lvremove -f "lxdtest-$(basename "${LXD_DIR}")/containers_ctImport-snap0"
        ;;
      zfs)
        zfs destroy "lxdtest-$(basename "${LXD_DIR}")/containers/ctImport@snapshot-snap0"
        ;;
    esac
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    ! lxd import ctImport || false
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport
  )
  # shellcheck disable=SC2031
  LXD_DIR=${LXD_DIR}
  kill_lxd "${LXD_IMPORT_DIR}"
}

test_backup_import() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc launch testimage c2
  lxc snapshot c2

  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  # create backup
  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --container-only
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --container-only
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
  ! lxc import "${LXD_DIR}/c3.tar.gz"

  # Specify pool explicitly; this should fails as the pool doesn't exist
  ! lxc import "${LXD_DIR}/c3.tar.gz" -s pool_1

  # Specify pool explicitly
  lxc import "${LXD_DIR}/c3.tar.gz" -s pool_2

  lxc rm -f c3

  # Reset default storage pool
  lxc profile device add default root disk path=/ pool="${default_pool}"

  lxc storage delete pool_2
}

test_backup_export() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage c1
  lxc snapshot c1

  mkdir "${LXD_DIR}/optimized" "${LXD_DIR}/non-optimized"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # container only

  if [ "$lxd_backend" = "btrfs" ] || [ "$lxd_backend" = "zfs" ]; then
    lxc export c1 "${LXD_DIR}/c1-optimized.tar.gz" --optimized-storage --container-only
    tar -xzf "${LXD_DIR}/c1-optimized.tar.gz" -C "${LXD_DIR}/optimized"

    [ -f "${LXD_DIR}/optimized/backup/index.yaml" ]
    [ -f "${LXD_DIR}/optimized/backup/container.bin" ]
    [ ! -d "${LXD_DIR}/optimized/backup/snapshots" ]
  fi

  lxc export c1 "${LXD_DIR}/c1.tar.gz" --container-only
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
}

test_backup_rename() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep "Error: not found" ; then
    echo "invalid rename response for missing container"
    false
  fi

  lxc launch testimage c1

  if ! lxc query -X POST /1.0/containers/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep "Error: not found" ; then
    echo "invalid rename response for missing backup"
    false
  fi

  lxc delete --force c1
}
