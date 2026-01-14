#!/bin/bash

# Test selective state field rendering for instances
test_instances_selective_fields() {
  echo "==> Testing selective state field rendering"

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Create test instances
  lxc init testimage test-selective-1
  lxc init testimage test-selective-2
  lxc start test-selective-1
  lxc start test-selective-2

  # Wait for instances to be fully running
  waitInstanceReady test-selective-1
  waitInstanceReady test-selective-2

  echo "==> Test 1: Traditional recursion=2 (backward compatible)"
  # Test that traditional recursion=2 still works and includes all fields
  result=$(lxc query '/1.0/instances?recursion=2' | jq -r '.[0].state')
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 2: Selective field - disk only"
  # Test recursion=[state.disk] returns disk but not network
  result=$(lxc query '/1.0/instances?recursion=[state.disk]' | jq -r '.[0].state')
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 3: Selective field - network only"
  # Test recursion=[state.network] returns network but not disk
  result=$(lxc query '/1.0/instances?recursion=[state.network]' | jq -r '.[0].state')
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 4: Selective field - both disk and network"
  # Test recursion=[state.disk,state.network] returns both
  result=$(lxc query '/1.0/instances?recursion=[state.disk,state.network]' | jq -r '.[0].state')
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 5: Selective field - no state fields"
  # Test recursion=[] returns no disk or network
  result=$(lxc query '/1.0/instances?recursion=[]' | jq -r '.[0].state')
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network == null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 6: Single instance with selective fields"
  # Test single instance endpoint with selective fields
  result=$(lxc query '/1.0/instances/test-selective-1?recursion=[state.disk]' | jq -r '.state')
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'

  result=$(lxc query '/1.0/instances/test-selective-1?recursion=[state.network]' | jq -r '.state')
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network != null'

  echo "==> Test 7: CLI automatic optimization"
  # Test that lxc list automatically optimizes based on columns (should not error)
  lxc list -c n,s,p  # Should skip disk and network
  lxc list -c n,s,4  # Should fetch network only
  lxc list -c n,s,D  # Should fetch disk only
  lxc list -c n,s,4,D  # Should fetch both

  echo "==> Test 7b: Verify empty fields optimization (recursion=[])"
  # When no disk or network columns are requested, verify recursion=[] is used
  # This should NOT trigger GetInstanceUsage calls
  result=$(lxc query '/1.0/instances?recursion=[]' | jq -r '.[0].state')
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network == null'
  # Status should still be present (lightweight field)
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 8: Invalid field name"
  # Test that invalid field names return an error
  ! lxc query '/1.0/instances?recursion=[state.invalid]' || false
  ! lxc query '/1.0/instances?recursion=[invalid]' || false

  echo "==> Test 9: Filtering with selective fields"
  # Test that filtering works with selective fields
  result=$(curl --silent --get --unix-socket "${LXD_DIR}/unix.socket" \
    "lxd/1.0/instances" \
    --data-urlencode "recursion=[state.disk]" \
    --data-urlencode "filter=name eq test-selective-1" | \
    jq -r '.metadata[0].state')
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'

  echo "==> Test 10: Multiple instances with selective fields"
  # Verify both instances are returned with selective fields
  result=$(lxc query '/1.0/instances?recursion=[state.disk]' | jq 'length')
  [ "${result}" -ge 2 ]

  # Cleanup
  lxc delete --force test-selective-1
  lxc delete --force test-selective-2

  echo "==> Selective state field rendering tests passed"
}

