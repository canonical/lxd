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
    ! lxd import ctImport
    lxd import ctImport --force
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd import ctImport --force
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    ! lxd import ctImport
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
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
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
      rm -rf "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
    fi
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    kill_lxc "${pid}"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    [ -L "${LXD_DIR}/containers/ctImport" ] && [ -d "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/containers/ctImport" ]
    if [ "$lxd_backend" != "dir" ] && [ "$lxd_backend" != "btrfs" ]; then
      [ -L "${LXD_DIR}/snapshots/ctImport" ] && [ -d "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0" ]
    fi
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    case "$lxd_backend" in
      btrfs)
        btrfs subvolume delete "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
        rm -rf "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
        ;;
      ceph)
        rbd unmap "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0" || true
        rbd snap unprotect "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0" || true
        rbd snap rm "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0"
        ;;
      dir)
        rm -r "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
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
    ! lxd import ctImport
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
        btrfs subvolume delete "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
        rm -rf "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
        ;;
      ceph)
        rbd unmap "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0" || true
        rbd snap unprotect "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0" || true
        rbd snap rm "lxdtest-$(basename "${LXD_DIR}")/container_ctImport@snapshot_snap0"
        ;;
      dir)
        rm -r "${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")/snapshots/ctImport/snap0"
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
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport
  )
  # shellcheck disable=SC2031
  LXD_DIR=${LXD_DIR}
  kill_lxd "${LXD_IMPORT_DIR}"
}
