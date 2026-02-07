# Test selective recursion for instances
test_instances_selective_recursion() {
  echo "==> Testing selective recursion"

  ensure_import_testimage

  # Create and start test instances
  lxc launch testimage test-selective-1
  lxc launch testimage test-selective-2

  echo "==> Test 1: Traditional recursion=2 (backward compatible)"
  # Test that traditional recursion=2 still works and includes all fields
  result="$(lxc query '/1.0/instances?recursion=2' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 2: Selective recursion - disk only"
  # Test recursion=[state.disk] returns disk but not network
  result="$(lxc query '/1.0/instances?recursion=[state.disk]' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 3: Selective recursion - network only"
  # Test recursion=[state.network] returns network but not disk
  result="$(lxc query '/1.0/instances?recursion=[state.network]' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 4: Selective recursion - both disk and network"
  # Test recursion=[state.disk,state.network] returns both
  result="$(lxc query '/1.0/instances?recursion=[state.disk,state.network]' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 5: Selective recursion - no state fields"
  # Test recursion=[] returns no disk or network
  result="$(lxc query '/1.0/instances?recursion=[]' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network == null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 6: Single instance with selective recursion"
  # Test single instance endpoint with selective recursion
  result="$(lxc query '/1.0/instances/test-selective-1?recursion=[state.disk]' | jq --exit-status --raw-output '.state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'

  result="$(lxc query '/1.0/instances/test-selective-1?recursion=[state.network]' | jq --exit-status --raw-output '.state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network != null'

  echo "==> Test 7: CLI automatic optimization"
  # Test that lxc list automatically optimizes queries based on displayed columns
  # Start background monitor to capture API request URLs
  monitor_urls="${TEST_DIR}/monitor-urls.log"
  lxc monitor --format=json | stdbuf -oL jq --unbuffered --raw-output 'select(.metadata.context.url? // "" | startswith("/1.0/instances")) | .metadata.context.url' > "${monitor_urls}" &
  monitor_pid=$!
  sleep 0.1  # Give monitor time to initialize

  # Test 7a: No disk/network columns - should use recursion=[]
  lxc list -c n,s,p > /dev/null
  sleep 0.1
  grep -aF 'recursion=%5B%5D' "${monitor_urls}"
  echo -n > "${monitor_urls}"

  # Test 7b: Network column only - should use recursion=[state.network]
  lxc list -c n,s,4 > /dev/null
  sleep 0.1
  grep -aE 'state\.network|state%2Enetwork' "${monitor_urls}"
  echo -n > "${monitor_urls}"

  # Test 7c: Disk column only - should use recursion=[state.disk]
  lxc list -c n,s,D > /dev/null
  sleep 0.1
  grep -aE 'state\.disk|state%2Edisk' "${monitor_urls}"
  echo -n > "${monitor_urls}"

  # Test 7d: Both columns - should use recursion=[state.disk,state.network]
  lxc list -c n,s,4,D > /dev/null
  sleep 0.1
  grep -aE 'state\.disk|state%2Edisk' "${monitor_urls}"
  grep -aE 'state\.network|state%2Enetwork' "${monitor_urls}"

  # Stop monitoring
  kill_go_proc "${monitor_pid}" 2>/dev/null || true
  rm "${monitor_urls}"

  echo "==> Test 7b: Verify empty fields optimization (recursion=[])"
  # When no disk or network columns are requested, verify recursion=[] is used
  # This should NOT trigger GetInstanceUsage calls
  result="$(lxc query '/1.0/instances?recursion=[]' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network == null'
  # Status should still be present (lightweight field)
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 8: Invalid field name"
  # Test that invalid field names return an error
  ! lxc query '/1.0/instances?recursion=[state.invalid]' || false
  ! lxc query '/1.0/instances?recursion=[invalid]' || false

  echo "==> Test 9: Filtering with selective recursion"
  # Test that filtering works with selective recursion
  result="$(curl --silent --get --unix-socket "${LXD_DIR}/unix.socket" \
    "lxd/1.0/instances" \
    --data-urlencode "recursion=[state.disk]" \
    --data-urlencode "filter=name eq test-selective-1" | \
    jq --exit-status --raw-output '.metadata[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'

  echo "==> Test 10: Multiple instances with selective recursion"
  # Verify both instances are returned with selective recursion
  lxc query '/1.0/instances?recursion=[state.disk]' | jq --exit-status 'length == 2'

  # Cleanup
  lxc delete --force test-selective-1 test-selective-2
}


