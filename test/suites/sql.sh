# Test the lxd sql command.
test_sql() {
  # Invalid arguments
  ! lxd sql
  ! lxd sql foo "SELECT * FROM CONFIG"
  ! lxd sql global ""

  # Local database
  lxd sql local "SELECT * FROM config" | grep -q "core.https_address"

  # Global database
  lxd sql global "SELECT * FROM config" | grep -q "core.trust_password"

  # Standard input
  echo "SELECT * FROM config" | lxd sql global - | grep -q "core.trust_password"

  # Multiple queries
  lxd sql global "SELECT * FROM config; SELECT * FROM containers" | grep -q "=> Query 0"
}
