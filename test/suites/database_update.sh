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
