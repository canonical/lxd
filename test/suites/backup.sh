test_container_import() {
  ensure_import_testimage

  LXD_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_IMPORT_DIR}"
  spawn_lxd "${LXD_IMPORT_DIR}" true
  (
    lxc init testimage ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    ! lxd import ctImport
    lxd import ctImport --force
    kill -9 "${pid}"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    lxd import ctImport --force
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    ! lxd import ctImport
    lxd import ctImport --force
    kill -9 "${pid}"
    lxc info ctImport | grep snap0
    lxc start ctImport
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    shutdown_lxd "${LXD_IMPORT_DIR}"
    kill -9 "${pid}"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    respawn_lxd "${LXD_IMPORT_DIR}"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    shutdown_lxd "${LXD_IMPORT_DIR}"
    kill -9 "${pid}"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    respawn_lxd "${LXD_IMPORT_DIR}"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    shutdown_lxd "${LXD_IMPORT_DIR}"
    kill -9 "${pid}"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    respawn_lxd "${LXD_IMPORT_DIR}"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    shutdown_lxd "${LXD_IMPORT_DIR}"
    kill -9 "${pid}"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport/snap0'"
    respawn_lxd "${LXD_IMPORT_DIR}"
    lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc delete --force ctImport

    lxc init testimage ctImport
    lxc snapshot ctImport
    lxc start ctImport
    rm "${LXD_DIR}/containers/ctImport"
    pid=$(lxc info ctImport | grep ^Pid | awk '{print $2}')
    shutdown_lxd "${LXD_IMPORT_DIR}"
    kill -9 "${pid}"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM containers WHERE name='ctImport/snap0'"
    sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport'"
    respawn_lxd "${LXD_IMPORT_DIR}"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc delete --force ctImport

    # Test whether a snapshot that exists on disk but not in the "backup.yaml"
    # file is correctly restored. This can be done by not starting the parent
    # container which avoids that the backup file is written out.
    if [ "$(storage_backend "$LXD_DIR")" = "dir" ]; then
      lxc init testimage ctImport
      lxc snapshot ctImport
      shutdown_lxd "${LXD_IMPORT_DIR}"
      sqlite3 "${LXD_DIR}/lxd.db" "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='ctImport/snap0'"
      respawn_lxd "${LXD_IMPORT_DIR}"
      ! lxd import ctImport
      lxd import ctImport --force
      lxc info ctImport | grep snap0
      lxc delete --force ctImport
    fi
  )
  # shellcheck disable=SC2031
  kill_lxd "${LXD_IMPORT_DIR}"
}
