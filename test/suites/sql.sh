# Test the lxd sql command.
test_sql() {
  # Invalid arguments
  ! lxd sql foo "SELECT * FROM config" || false
  ! lxd sql global "" || false

  # Local database query
  [ "$(lxd sql local --format csv "SELECT COUNT(*) FROM config WHERE key = 'core.https_address'")" = 1 ]

  # Global database query
  lxc config set user.foo=bar
  [ "$(lxd sql global --format csv "SELECT value FROM config WHERE key = 'user.foo'")" = "bar" ]

  # Test formats
  lxd sql global --format sql 'SELECT key FROM config' | grep -wF 'key'
  lxd sql global --format table 'SELECT key FROM config' | grep -wF 'KEY'
  lxd sql global --format compact 'SELECT key FROM config' | grep -wF 'KEY'

  # Global database insert
  lxd sql global "INSERT INTO config(key,value) VALUES('core.https_allowed_credentials','true')" | grep -xF "Rows affected: 1"
  lxd sql global "DELETE FROM config WHERE key='core.https_allowed_credentials'" | grep -xF "Rows affected: 1"

  # Standard input
  [ "$(echo "SELECT value FROM config WHERE key = 'user.foo'" | lxd sql global --format csv -)" = "bar" ]

  # Multiple queries
  lxd sql global "SELECT * FROM config; SELECT * FROM instances" | grep -xF "=> Query 0:"

  # Local database dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  lxd sql local .dump | sqlite3 "${SQLITE_DUMP}"
  sqlite3 "${SQLITE_DUMP}" "SELECT * FROM patches" | grep -F "|dnsmasq_entries_include_device_name|"
  rm -f "${SQLITE_DUMP}"

  # Local database schema dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  lxd sql local .schema | sqlite3 "${SQLITE_DUMP}"
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM schema')" = "" ]
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM patches')" = "" ]
  rm -f "${SQLITE_DUMP}"

  # Global database dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  GLOBAL_DUMP="$(lxd sql global .dump)"
  echo "$GLOBAL_DUMP" | grep -F "CREATE TRIGGER" # ensure triggers are captured.
  echo "$GLOBAL_DUMP" | grep -F "CREATE INDEX"   # ensure indices are captured.
  echo "$GLOBAL_DUMP" | grep -F "CREATE VIEW"    # ensure views are captured.
  echo "$GLOBAL_DUMP" | sqlite3 "${SQLITE_DUMP}"
  sqlite3 "${SQLITE_DUMP}" "SELECT * FROM profiles" | grep -F "|Default LXD profile|"
  rm -f "${SQLITE_DUMP}"

  # Global database schema dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  lxd sql global .schema | sqlite3 "${SQLITE_DUMP}"
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM schema')" = "" ]
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM profiles')" = "" ]
  rm -f "${SQLITE_DUMP}"

  # Sync the global database to disk
  SQLITE_SYNC="${LXD_DIR}/database/global/db.bin"
  echo "SYNC ${SQLITE_SYNC}"
  lxd sql global .sync
  sqlite3 "${SQLITE_SYNC}" "SELECT * FROM schema" | grep "^1|"
  sqlite3 "${SQLITE_SYNC}" "SELECT * FROM profiles" | grep -F "|Default LXD profile|"
}
