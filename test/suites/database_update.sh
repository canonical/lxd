#!/bin/sh

test_database_update(){
  LXD_MIGRATE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  MIGRATE_DB=${LXD_MIGRATE_DIR}/lxd.db

  # Nuke preexisting database if it exists
  rm -f "${LXD_MIGRATE_DIR}/lxd.db"
  # Create the version 1 schema as the database
  sqlite3 "${MIGRATE_DB}" > /dev/null < deps/schema1.sql

  # Start an LXD demon in the tmp directory. This should start the updates.
  spawn_lxd "${LXD_MIGRATE_DIR}"

  # Assert there are enough tables.
  expected_tables=15
  tables=$(sqlite3 "${MIGRATE_DB}" ".dump" | grep -c "CREATE TABLE")
  [ "${tables}" -eq "${expected_tables}" ] || { echo "FAIL: Wrong number of tables after database migration. Found: ${tables}, expected ${expected_tables}"; false; }

  # There should be 10 "ON DELETE CASCADE" occurences
  expected_cascades=10
  cascades=$(sqlite3 "${MIGRATE_DB}" ".dump" | grep -c "ON DELETE CASCADE")
  [ "${cascades}" -eq "${expected_cascades}" ] || { echo "FAIL: Wrong number of ON DELETE CASCADE foreign keys. Found: ${cascades}, exected: ${expected_cascades}"; false; }
}
