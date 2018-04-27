test_database_update(){
  LXD_MIGRATE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  mkdir -p "${LXD_MIGRATE_DIR}/database"
  MIGRATE_DB=${LXD_MIGRATE_DIR}/database/local.db

  # Add some custom queries in patch.local.sql
  cat << EOF > "${LXD_MIGRATE_DIR}/database/patch.local.sql"
INSERT INTO certificates(fingerprint, type, name, certificate) VALUES('abc', 0, 'cert1', 'blob1');
CREATE TABLE test (n INT);
INSERT INTO test(n) VALUES(1);
EOF

  # Add some custom queries in patch.global.sql
  cat << EOF > "${LXD_MIGRATE_DIR}/database/patch.global.sql"
CREATE TABLE test (n INT);
INSERT INTO test(n) VALUES(1);
EOF

  # Create the version 1 schema as the database
  sqlite3 "${MIGRATE_DB}" > /dev/null < deps/schema1.sql

  # Start an LXD demon in the tmp directory. This should start the updates.
  spawn_lxd "${LXD_MIGRATE_DIR}" true

  # Assert there are enough tables.
  expected_tables=5
  tables=$(sqlite3 "${MIGRATE_DB}" ".dump" | grep -c "CREATE TABLE")
  [ "${tables}" -eq "${expected_tables}" ] || { echo "FAIL: Wrong number of tables after database migration. Found: ${tables}, expected ${expected_tables}"; false; }

  # Check that the custom queries were executed.
  LXD_DIR="${LXD_MIGRATE_DIR}" lxd sql local "SELECT * FROM test" | grep -q "1"
  LXD_DIR="${LXD_MIGRATE_DIR}" lxd sql global "SELECT * FROM certificates" | grep -q "cert1"
  LXD_DIR="${LXD_MIGRATE_DIR}" lxd sql global "SELECT * FROM test" | grep -q "1"

  # The custom patch files were deleted.
  ! [ -e "${LXD_MIGRATE_DIR}/database/patch.local.sql" ]
  ! [ -e "${LXD_MIGRATE_DIR}/database/patch.global.sql" ]

  kill_lxd "$LXD_MIGRATE_DIR"
}

# Test restore database backups after a failed upgrade.
test_database_restore(){
  LXD_RESTORE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  spawn_lxd "${LXD_RESTORE_DIR}" true

  # Set a config value before the broken upgrade.
  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_RESTORE_DIR}
    lxc config set "core.https_allowed_credentials" "true"
  )

  shutdown_lxd "${LXD_RESTORE_DIR}"

  # Simulate a broken update by dropping in a buggy patch.global.sql
  cat << EOF > "${LXD_RESTORE_DIR}/database/patch.global.sql"
UPDATE config SET value='false' WHERE key='core.https_allowed_credentials';
INSERT INTO broken(n) VALUES(1);
EOF

  # Starting LXD fails.
  ! LXD_DIR="${LXD_RESTORE_DIR}" lxd --logfile "${LXD_RESTORE_DIR}/lxd.log" "${DEBUG-}" 2>&1

  # Remove the broken patch
  rm -f "${LXD_RESTORE_DIR}/database/patch.global.sql"

  # Restore the backup
  rm -rf "${LXD_RESTORE_DIR}/database/global"
  cp -a "${LXD_RESTORE_DIR}/database/global.bak" "${LXD_RESTORE_DIR}/database/global"

  # Restart the daemon and check that our previous settings are still there
  respawn_lxd "${LXD_RESTORE_DIR}" true
  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_RESTORE_DIR}
    lxc config get "core.https_allowed_credentials" | grep -q "true"
  )

  kill_lxd "${LXD_RESTORE_DIR}"
}
