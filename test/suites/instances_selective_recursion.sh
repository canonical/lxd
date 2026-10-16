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
  # Test recursion=2;fields=state.disk returns disk but not network
  result="$(lxc query '/1.0/instances?recursion=2%3Bfields%3Dstate.disk' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 3: Selective recursion - network only"
  # Test recursion=2;fields=state.network returns network but not disk
  result="$(lxc query '/1.0/instances?recursion=2%3Bfields%3Dstate.network' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 4: Selective recursion - both disk and network"
  # Test recursion=2;fields=state.disk,state.network returns both
  result="$(lxc query '/1.0/instances?recursion=2%3Bfields%3Dstate.disk%2Cstate.network' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network != null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 5: Selective recursion - no state fields"
  # Test recursion=2;fields= returns no disk or network
  result="$(lxc query '/1.0/instances?recursion=2%3Bfields%3D' | jq --exit-status --raw-output '.[0].state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network == null'
  echo "${result}" | jq --exit-status '.status != null'

  echo "==> Test 6: Single instance with selective recursion"
  # Test single instance endpoint with selective recursion
  result="$(lxc query '/1.0/instances/test-selective-1?recursion=2%3Bfields%3Dstate.disk' | jq --exit-status --raw-output '.state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'

  result="$(lxc query '/1.0/instances/test-selective-1?recursion=2%3Bfields%3Dstate.network' | jq --exit-status --raw-output '.state')"
  echo "${result}" | jq --exit-status '.disk == null'
  echo "${result}" | jq --exit-status '.network != null'

  echo "==> Test 7: CLI automatic optimization"
  # Test that lxc list automatically optimizes queries based on displayed columns
  # Start background monitor to capture API request URLs
  monitor_urls="${TEST_DIR}/monitor-urls.log"
  lxc monitor --format=json | stdbuf -oL jq --unbuffered --exit-status --raw-output 'if .type == "logging" and .metadata.message == "Event listener server handler started" then "READY" elif (.metadata.context.url? // "" | startswith("/1.0/instances")) then .metadata.context.url else empty end' >> "${monitor_urls}" &
  monitor_pid=$!

  # Runs the optional command once, then polls the monitor log for the pattern.
  wait_for_monitor_line() {
    local pattern="${1}"
    shift

    if [ "$#" -gt 0 ]; then
      "$@" > /dev/null
    fi

    for _ in $(seq 100); do
      if grep -qaE "${pattern}" "${monitor_urls}"; then
        return 0
      fi

      sleep 0.1
    done

    echo "Monitor did not log a line matching ${pattern}"
    cat "${monitor_urls}"
    return 1
  }

  # This debug event is logged once the listener is registered.
  wait_for_monitor_line '^READY$'

  # Test 7a: No disk/network columns - should use recursion=2;fields= (empty fields)
  wait_for_monitor_line 'recursion=2%3Bfields%3D(&|$)|recursion=2;fields=(&|$)' lxc list -c n,s,p

  # Test 7b: Network column only - should use recursion=2;fields=state.network
  wait_for_monitor_line 'recursion=2(%3B|;)fields(=|%3D)state(\.|%2E)network(&|$)' lxc list -c n,s,4

  # Test 7c: Disk column only - should use recursion=2;fields=state.disk
  wait_for_monitor_line 'recursion=2(%3B|;)fields(=|%3D)state(\.|%2E)disk(&|$)' lxc list -c n,s,D

  # Test 7d: Both columns - should use recursion=2;fields=state.disk,state.network
  wait_for_monitor_line 'recursion=2(%3B|;)fields(=|%3D)state(\.|%2E)(disk(,|%2C)state(\.|%2E)network|network(,|%2C)state(\.|%2E)disk)(&|$)' lxc list -c n,s,4,D

  # Stop monitoring
  kill_go_proc "${monitor_pid}" 2>/dev/null || true
  rm "${monitor_urls}"

  echo "==> Test 8: Invalid field name"
  # Test that invalid field names return an error
  ! lxc query '/1.0/instances?recursion=2%3Bfields%3Dstate.invalid' || false
  ! lxc query '/1.0/instances?recursion=2%3Bfields%3Dinvalid' || false

  echo "==> Test 9: Filtering with selective recursion"
  # Test that filtering works with selective recursion
  result="$(curl --silent --get --unix-socket "${LXD_DIR}/unix.socket" \
    "lxd/1.0/instances" \
    --data-urlencode "recursion=2;fields=state.disk" \
    --data-urlencode "filter=name eq test-selective-1" | \
    jq --exit-status --raw-output '.metadata[0].state')"
  echo "${result}" | jq --exit-status '.disk != null'
  echo "${result}" | jq --exit-status '.network == null'

  echo "==> Test 10: Multiple instances with selective recursion"
  # Verify both instances are returned with selective recursion
  lxc query '/1.0/instances?recursion=2%3Bfields%3Dstate.disk' | jq --exit-status 'length == 2'

  # Cleanup
  lxc delete --force test-selective-1 test-selective-2
}


