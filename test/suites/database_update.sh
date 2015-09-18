test_database_update(){
  export LXD_MIGRATE_DIR=$(mktemp -d -p $(pwd))
  MIGRATE_DB=${LXD_MIGRATE_DIR}/lxd.db

  # Nuke preexisting database if it exists
  rm -f ${LXD_MIGRATE_DIR}/lxd.db
  # Create the version 1 schema as the database
  cat deps/schema1.sql | sqlite3 ${MIGRATE_DB} > /dev/null

  # Start an LXD demon in the tmp directory. This should start the updates.
  spawn_lxd 127.0.0.1:18447 "${LXD_MIGRATE_DIR}"

  # Assert there are enough tables.
  expected_tables=15
  tables=`sqlite3 ${MIGRATE_DB} ".dump" | grep "CREATE TABLE" | wc -l`
  [ $tables -eq $expected_tables ] || { echo "FAIL: Wrong number of tables after database migration. Found: $tables, expected $expected_tables"; false; }

  # There should be 10 "ON DELETE CASCADE" occurences
  expected_cascades=10
  cascades=`sqlite3 ${MIGRATE_DB} ".dump" | grep "ON DELETE CASCADE" | wc -l`
  [ $cascades -eq $expected_cascades ] || { echo "FAIL: Wrong number of ON DELETE CASCADE foreign keys. Found: $cascades, exected: $expected_cascades"; false; }
}
