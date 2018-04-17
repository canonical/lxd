test_database_update(){
  LXD_MIGRATE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  mkdir -p "${LXD_MIGRATE_DIR}/database"
  MIGRATE_DB=${LXD_MIGRATE_DIR}/database/local.db

  # Create the version 1 schema as the database
  sqlite3 "${MIGRATE_DB}" > /dev/null < deps/schema1.sql

  # Start an LXD demon in the tmp directory. This should start the updates.
  spawn_lxd "${LXD_MIGRATE_DIR}" true

  # Assert there are enough tables.
  expected_tables=4
  tables=$(sqlite3 "${MIGRATE_DB}" ".dump" | grep -c "CREATE TABLE")
  [ "${tables}" -eq "${expected_tables}" ] || { echo "FAIL: Wrong number of tables after database migration. Found: ${tables}, expected ${expected_tables}"; false; }

  kill_lxd "$LXD_MIGRATE_DIR"
}
