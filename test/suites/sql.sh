# Test the lxd sql command.
test_sql() {
  # Invalid arguments
  ! lxd sql ""
  ! lxd sql

  # Single query
  lxd sql "SELECT * FROM config" | grep "core.trust_password"

  # Standard input
  echo "SELECT * FROM config" | lxd sql - | grep "core.trust_password"
}
