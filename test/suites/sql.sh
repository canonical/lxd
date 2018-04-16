# Test the lxd sql command.
test_sql() {
  # Invalid arguments
  ! lxd sql ""
  ! lxd sql

  # Single query
  lxd sql "SELECT * FROM config" | grep -q "core.trust_password"

  # Standard input
  echo "SELECT * FROM config" | lxd sql - | grep -q "core.trust_password"

  # Multiple queries
  lxd sql "SELECT * FROM config; SELECT * FROM containers" | grep -q "=> Query 0"
}
