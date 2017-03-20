#!/bin/sh

test_container_import() {
  ensure_import_testimage

  lxc init testimage ctImport
  lxc snapshot ctImport
  lxc start ctImport
  ! lxd import ctImport
  lxd import ctImport --force
  lxc info ctImport | grep snap0
  lxc delete --force ctImport

  lxc init testimage ctImport
  lxc snapshot ctImport
  lxc start ctImport
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM containers WHERE name='ctImport'"
  ! lxd import ctImport
  lxd import ctImport --force
  lxc info ctImport | grep snap0
  lxc delete --force ctImport

  lxc init testimage ctImport
  lxc snapshot ctImport
  lxc start ctImport
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM containers WHERE name='ctImport/snap0'"
  ! lxd import ctImport
  lxd import ctImport --force
  lxc info ctImport | grep snap0
  lxc delete --force ctImport

  lxc init testimage ctImport
  lxc snapshot ctImport
  lxc start ctImport
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM containers WHERE name='ctImport'"
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM containers WHERE name='ctImport/snap0'"
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM storage_volumes WHERE name='ctImport'"
  ! lxd import ctImport
  lxd import ctImport --force
  lxc info ctImport | grep snap0
  lxc delete --force ctImport

  lxc init testimage ctImport
  lxc snapshot ctImport
  lxc start ctImport
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM containers WHERE name='ctImport'"
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM containers WHERE name='ctImport/snap0'"
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM storage_volumes WHERE name='ctImport'"
  sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM storage_volumes WHERE name='ctImport/snap0'"
  lxd import ctImport
  lxd import ctImport --force
  lxc info ctImport | grep snap0
  lxc delete --force ctImport

  # Test whether a snapshot that exists on disk but not in the "backup.yaml"
  # file is correctly restored. This can be done by not starting the parent
  # container which avoids that the backup file is written out.
  if [ "${LXD_BACKEND}" = "dir" ]; then
    lxc init testimage ctImport
    lxc snapshot ctImport
    sqlite3 "${LXD_DIR}/lxd.db" "DELETE FROM storage_volumes WHERE name='ctImport/snap0'"
    ! lxd import ctImport
    lxd import ctImport --force
    lxc info ctImport | grep snap0
    lxc delete --force ctImport
  fi
}
