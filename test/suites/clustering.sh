test_clustering_enable() {
  # Override LXD_DIR for the test scope
  local LXD_DIR
  LXD_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
  spawn_lxd "${LXD_DIR}" false

  # Test specified core.https_address with no cluster.https_address
  [[ "$(lxc config get core.https_address)" =~ ^127\.0\.0\.1:[0-9]{4,5}$ ]]
  # Create a container.
  lxc storage create default dir
  lxc profile device add default root disk path="/" pool="default"
  lxc init --empty c1

  # Enable clustering.
  lxc cluster enable node1

  # Test the non-recursive mode to list cluster members.
  lxc query /1.0/cluster/members | jq --exit-status '.[0] == "/1.0/cluster/members/node1"'

  # Test the recursive mode to list cluster members.
  # The command implicitly sets the recursive=1 query paramter.
  lxc cluster list | grep -wF node1

  # The container is still there and now shows up as
  # being on node 1.
  [ "$(lxc list -f csv -c nL c1)" = "c1,node1" ]

  # Clustering can't be enabled on an already clustered instance.
  ! lxc cluster enable node2 || false

  # Delete the container
  lxc delete c1

  kill_lxd "${LXD_DIR}"

  # Test wildcard core.https_address with no cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set core.https_address=::
  # Enable clustering.
  ! lxc cluster enable node1 || false

  kill_lxd "${LXD_DIR}"

  # Test default port core.https_address with no cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set core.https_address=127.0.0.1
  # Enable clustering.
  lxc cluster enable node1
  lxc cluster list | grep -F 127.0.0.1:8443

  kill_lxd "${LXD_DIR}"

  # Test wildcard core.https_address with valid cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set core.https_address=:: cluster.https_address=127.0.0.1:8443
  # Enable clustering.
  lxc cluster enable node1
  lxc cluster list | grep -F 127.0.0.1:8443

  kill_lxd "${LXD_DIR}"

  # Test empty core.https_address with no cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config unset core.https_address
  # Enable clustering.
  ! lxc cluster enable node1 || false

  kill_lxd "${LXD_DIR}"

  # Test empty core.https_address with valid cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set core.https_address= cluster.https_address=127.0.0.1:8443
  # Enable clustering.
  lxc cluster enable node1
  lxc cluster list | grep -F 127.0.0.1:8443

  kill_lxd "${LXD_DIR}"

  # Test empty core.https_address with default port cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set core.https_address= cluster.https_address=127.0.0.1
  # Enable clustering.
  lxc cluster enable node1
  lxc cluster list | grep -F 127.0.0.1:8443

  kill_lxd "${LXD_DIR}"

  # Test covered cluster.https_address
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set core.https_address=127.0.0.1:8443 cluster.https_address=127.0.0.1:8443
  # Enable clustering.
  lxc cluster enable node1
  lxc cluster list | grep -F 127.0.0.1:8443

  kill_lxd "${LXD_DIR}"

  # Test cluster listener after reload
  mkdir "${LXD_DIR}"
  spawn_lxd "${LXD_DIR}" false

  lxc config set cluster.https_address=127.0.0.1:8443
  kill_go_proc "$(< "${LXD_DIR}/lxd.pid")"
  respawn_lxd "${LXD_DIR}" true
  # Enable clustering.
  lxc cluster enable node1
  lxc cluster list | grep -F 127.0.0.1:8443

  kill_lxd "${LXD_DIR}"
}

test_clustering_membership() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  # shellcheck disable=SC2153
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Neither server certificate can be deleted
  LXD_ONE_FINGERPRINT="$(cert_fingerprint "${LXD_ONE_DIR}/server.crt")"
  LXD_TWO_FINGERPRINT="$(cert_fingerprint "${LXD_TWO_DIR}/server.crt")"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config trust remove "${LXD_ONE_FINGERPRINT}" || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc config trust remove "${LXD_ONE_FINGERPRINT}" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config trust remove "${LXD_TWO_FINGERPRINT}" || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc config trust remove "${LXD_TWO_FINGERPRINT}" || false

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster.offline_threshold)" = "11" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc config get cluster.offline_threshold)" = "11" ]

  # The preseeded network bridge exists on all nodes.
  # shellcheck disable=SC2154
  nsenter -m -n -t "$(< "${TEST_DIR}/ns/${ns1}/PID")" -- ip link show "${bridge}" > /dev/null
  nsenter -m -n -t "$(< "${TEST_DIR}/ns/${ns2}/PID")" -- ip link show "${bridge}" > /dev/null

  # Create a pending network and pool, to show that they are not
  # considered when checking if the joining node has all the required
  # networks and pools.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create net1 --target node2

  # Spawn a third node, using the non-leader node2 as join target.
  spawn_lxd_and_join_cluster "${cert}" 3 2 "${LXD_ONE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  spawn_lxd_and_join_cluster "${cert}" 5 4 "${LXD_ONE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # List all nodes, using clients points to different nodes and
  # checking which are database nodes and which are database-standby nodes.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -xF -- '- database-leader'
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep -wFc "database-standby")" = "2" ]
  [ "$(LXD_DIR="${LXD_FIVE_DIR}" lxc cluster list | grep -wFc "database-voter")" = "2" ]

  # Show a single node
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node5 | grep -F "server_name: node5"

  # Client certificate are shared across all nodes.
  token="$(LXD_DIR=${LXD_ONE_DIR} lxc config trust add --name foo -q)"
  lxc remote add cluster 100.64.1.101:8443 --token="${token}"
  lxc remote set-url cluster https://100.64.1.102:8443
  lxc network list cluster: | grep -F "${bridge}"
  lxc remote remove cluster

  # Check info for single node (from local and remote node).
  LXD_DIR="${LXD_FIVE_DIR}" lxc cluster info node5
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster info node5

  # Disable image replication
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.images_minimal_replica 1

  # Shutdown a database node, and wait a few seconds so it will be
  # detected as down.
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 11
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node3 | grep -xF "status: Offline"

  # Gracefully remove a node and check trust certificate is removed.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF node4
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM identities WHERE type = 3 and name = "node4"')" = 1 ]
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster remove node4
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF node4 || false
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM identities WHERE type = 3 and name = "node4"')" = 0 ]

  # The node isn't clustered anymore.
  ! LXD_DIR="${LXD_FOUR_DIR}" lxc cluster list || false

  # Generate a join token for the sixth node.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node6)"

  # Check token is associated to correct name.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep -wF node6 | grep -wF "${token}"

  # Spawn a sixth node, using join token.
  spawn_lxd_and_join_cluster "${cert}" 6 2 "${token}"

  # Check token has been deleted after join.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep -wF node6 || false

  # Generate a join token for a seventh node
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node7)"

  # Check token is associated to correct name
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep -wF node7 | grep -wF "${token}"

  # Revoke the token
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster revoke-token node7

  # Check token has been deleted
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node7 || false

  # Set cluster token expiry to 30 seconds
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.join_token_expiry=30S

  # Generate a join token for an eigth and ninth node
  token_valid="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node8)"

  # Spawn an eigth node, using join token.
  spawn_lxd_and_join_cluster "${cert}" 8 2 "${token_valid}"

  # This will cause the token to expire
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.join_token_expiry=1S
  token_expired="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node9)"
  sleep 1.1

  # Spawn a ninth node, using join token.
  ! spawn_lxd_and_join_cluster "${cert}" 9 2 "${token_expired}" || false

  # Unset join_token_expiry which will set it to the default value of 3h
  LXD_DIR="${LXD_ONE_DIR}" lxc config unset cluster.join_token_expiry

  LXD_DIR="${LXD_NINE_DIR}" lxd shutdown
  LXD_DIR="${LXD_EIGHT_DIR}" lxd shutdown
  LXD_DIR="${LXD_SIX_DIR}" lxd shutdown
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_NINE_DIR}/unix.socket"
  rm -f "${LXD_EIGHT_DIR}/unix.socket"
  rm -f "${LXD_SIX_DIR}/unix.socket"
  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_FIVE_DIR}"
  kill_lxd "${LXD_SIX_DIR}"
  kill_lxd "${LXD_EIGHT_DIR}"
  kill_lxd "${LXD_NINE_DIR}"
}

test_clustering_containers() {
  local pool_driver
  pool_driver="$(storage_backend "${LXD_INITIAL_DIR}")"

  echo "Create cluster with 3 nodes."
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  echo "Init a container on node2, using a client connected to node1."
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage foo

  echo "The container is visible through both nodes."
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c nsL)" = "foo,STOPPED,node2" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c nsL)" = "foo,STOPPED,node2" ]

  echo "Start the container via node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc start foo
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c nsL)" = "foo,RUNNING,node2" ]

  echo "Trying to delete a node which has container results in an error."
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node2 || false

  echo "Exec a command in the container via node1."
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc exec foo -- hostname)" = "foo" ]

  echo "Pull, push and delete files from the container via node1."
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/non-existing-file "${TEST_DIR}/non-existing-file" || false
  mkdir "${TEST_DIR}/hello-world"
  echo "hello world" > "${TEST_DIR}/hello-world/text"
  LXD_DIR="${LXD_ONE_DIR}" lxc file push "${TEST_DIR}/hello-world/text" foo/hello-world-text
  LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world-text "${TEST_DIR}/hello-world-text"
  [ "$(< "${TEST_DIR}/hello-world-text")" = "hello world" ]
  rm "${TEST_DIR}/hello-world-text"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world-text -)" = "hello world" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc file push --recursive "${TEST_DIR}/hello-world" foo/
  rm -r "${TEST_DIR}/hello-world"
  LXD_DIR="${LXD_ONE_DIR}" lxc file pull --recursive foo/hello-world "${TEST_DIR}"
  [ "$(< "${TEST_DIR}/hello-world/text")" = "hello world" ]
  rm -r "${TEST_DIR}/hello-world"
  LXD_DIR="${LXD_ONE_DIR}" lxc file delete foo/hello-world/text
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world/text "${TEST_DIR}/hello-world-text" || false

  echo "Stop the container via node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force

  echo "Rename the container via node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo foo2
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c n)" = "foo2" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo2 foo

  echo "Show lxc.log via node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc info --show-log foo | grep -xF 'Log:'

  echo "Create, rename and delete a snapshot of the container via node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo foo-bak
  LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -wF foo-bak
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo/foo-bak foo/foo-bak-2
  LXD_DIR="${LXD_ONE_DIR}" lxc delete foo/foo-bak-2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -wF foo-bak-2 || false

  echo "Export from node1 the image that was imported on node2."
  LXD_DIR="${LXD_ONE_DIR}" lxc image export testimage "${TEST_DIR}/testimage"
  rm "${TEST_DIR}/testimage.tar"*

  echo "Create a container on node1 using the image that was stored on node2."
  LXD_DIR="${LXD_TWO_DIR}" lxc launch --target node1 testimage bar
  LXD_DIR="${LXD_TWO_DIR}" lxc stop bar --force
  LXD_DIR="${LXD_ONE_DIR}" lxc delete bar
  ! LXD_DIR="${LXD_TWO_DIR}" lxc list -c n | grep -wF bar || false

  echo "Create a container on node1 using a snapshot from node2."
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo foo-bak
  LXD_DIR="${LXD_TWO_DIR}" lxc copy foo/foo-bak bar --target node1
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L bar)" = "node1" ]
  LXD_DIR="${LXD_THREE_DIR}" lxc delete bar

  echo "Copy the container on node2 without specifying a target, using a client connected to non-source node1."
  # Ensure the source container is on node2
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L foo)" = "node2" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc copy foo auto-copy
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c n auto-copy)" = "auto-copy" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L auto-copy)" != "node2" ]
  LXD_DIR="${LXD_THREE_DIR}" lxc delete auto-copy

  echo "Refresh a container and check its placement afterwards."
  # Create stopped base container.
  LXD_DIR="${LXD_ONE_DIR}" lxc copy foo test-refresh --target node1

  # Create additional target project.
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo
  LXD_DIR="${LXD_ONE_DIR}" lxc profile device add default root disk path="/" pool="data" --project foo

  # Perform copy/refresh matrix test from every cluster member to ensure the request forwards work as expected
  # and test with both started and stopped containers.
  for state in "" "--start"; do
    for project in "default" "foo"; do
      for member in "node1" "node2" "node3"; do
        echo "Copy base container to target ${member}."
        # shellcheck disable=SC2248
        LXD_DIR="${LXD_ONE_DIR}" lxc copy test-refresh test-refresh-target --target "${member}" --target-project "${project}" ${state}

        echo "Check placement is correct."
        LXD_DIR="${LXD_ONE_DIR}" lxc info test-refresh-target --project "${project}" | grep -xF "Location: ${member}"

        echo "Refresh target container."
        if [ "${state}" = "--start" ]; then
          local expected_error

          echo "Refresh should be blocked if the instance is running."

          # When using a remote driver or when copying on the same member the internal copy is used instead of the migration protocol.
          if [ "${pool_driver}" != "ceph" ] && [ "${member}" != "node1" ]; then
            expected_error='Error: Cannot refresh running instance "test-refresh-target"'
          else
            expected_error="Error: Failed getting exclusive access to target instance: Instance is running"
          fi

          [ "$(LXD_DIR="${LXD_ONE_DIR}" CLIENT_DEBUG="" SHELL_TRACING="" lxc copy test-refresh test-refresh-target --refresh --target-project "${project}" 2>&1)" = "${expected_error}" ]
        else
          LXD_DIR="${LXD_ONE_DIR}" lxc copy test-refresh test-refresh-target --refresh --target-project "${project}"
        fi

        echo "Check placement hasn't changed during refresh."
        LXD_DIR="${LXD_ONE_DIR}" lxc info test-refresh-target --project "${project}" | grep -xF "Location: ${member}"

        echo "Check project hasn't changed during refresh."
        LXD_DIR="${LXD_ONE_DIR}" lxc info test-refresh-target --project "${project}"

        LXD_DIR="${LXD_ONE_DIR}" lxc delete -f test-refresh-target --project "${project}"
      done
    done
  done

  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f test-refresh
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo

  echo "Copy the container on node2 to node3, using a client connected to node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc copy foo bar --target node3
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L bar)" = "node3" ]

  echo "Move the container on node3 to node1, using a client connected to node2 and a different container name than the original one."
  echo "Verify volatile.apply_template config key is preserved."
  local initial_template
  initial_template="$(LXD_DIR="${LXD_TWO_DIR}" lxc config get bar volatile.apply_template)"

  LXD_DIR="${LXD_TWO_DIR}" lxc move bar egg --target node2
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L egg)" = "node2" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)" = "${initial_template}" ]

  echo "Move back to node3 the container on node1, keeping the same name."
  LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node3
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L egg)" = "node3" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)" = "${initial_template}" ]

  echo "Live migration is not supported for containers."
  LXD_DIR="${LXD_TWO_DIR}" lxc start egg
  ! LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node1 || false
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c sL egg)" = "RUNNING,node3" ]
  LXD_DIR="${LXD_TWO_DIR}" lxc stop -f egg

  echo "Create backup that will make the instance impossible to move."
  LXD_DIR="${LXD_THREE_DIR}" lxc query -X POST --wait -d '{"name":"eggroll"}' /1.0/instances/egg/backups
  echo "Move should fail and container should remain on node3."
  [ "$(LXD_DIR="${LXD_THREE_DIR}" CLIENT_DEBUG="" SHELL_TRACING="" lxc move egg --target node2 2>&1)" = "Error: Migration API failure: Instance has backups" ]
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc list -f csv -c nsL egg)" = "egg,STOPPED,node3" ]

  LXD_DIR="${LXD_THREE_DIR}" lxc delete egg

  echo "Shutdown node 2, wait for it to be considered offline, and list containers."
  LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 11
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c ns)" = "foo,ERROR" ]

  echo "For an instance on an offline member, we can get its config but not use recursion nor get instance state."
  LXD_DIR="${LXD_ONE_DIR}" lxc config show foo
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/instances/foo" | jq --exit-status '.status == "Error"'
  ! LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/instances/foo?recursion=1" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/instances/foo/state" || false

  echo "Init a container without specifying any target. It will be placed on node1 since node2 is offline and both node1 and node3 have zero containers, but node1 has a lower node ID."
  LXD_DIR="${LXD_THREE_DIR}" lxc init --empty bar
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc list -f csv -c L bar)" = "node1" ]

  echo "Init a container without specifying any target. It will be placed on node3 since node2 is offline and node1 already has a container."
  LXD_DIR="${LXD_THREE_DIR}" lxc init --empty egg
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc list -f csv -c L egg)" = "node3" ]

  LXD_DIR="${LXD_ONE_DIR}" lxc delete egg bar

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

test_clustering_storage() {
  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  local poolDriver
  poolDriver="$(storage_backend "${LXD_INITIAL_DIR}")"

  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  # Check both nodes show preseeded storage pool created.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node1'")" = "node1,1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node2'")" = "node2,1" ]

  if [ "${poolDriver}" != "ceph" ]; then
    # Create a volume on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create data web
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume list data | grep -F web | grep -wF node1
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume list data | grep -F web | grep -wF node1

    # Since the volume name is unique to node1, it's possible to show, rename,
    # get the volume without specifying the --target parameter.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show data web | grep -F "location: node1"
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume rename data web webbaz
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename data webbaz web
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume get data web size

    # Create another volume on node2 with the same name of the one on
    # node1.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create --target node2 data web

    # Trying to show, rename or delete the web volume without --target
    # fails, because it's not unique.
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show data web || false
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename data web webbaz || false
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete data web || false

    LXD_DIR="${LXD_TWO_DIR}" lxc init --empty c1 --target node1
    LXD_DIR="${LXD_TWO_DIR}" lxc init --empty c2 --target node2
    LXD_DIR="${LXD_TWO_DIR}" lxc init --empty c3 --target node2

    LXD_DIR="${LXD_TWO_DIR}" lxc config device add c1 web disk pool=data source=web path=/mnt/web
    LXD_DIR="${LXD_TWO_DIR}" lxc config device add c2 web disk pool=data source=web path=/mnt/web

    # Specifying the --target parameter shows the proper volume.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show --target node1 data web | grep -F "location: node1"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show --target node2 data web | grep -F "location: node2"

    # Rename updates the disk devices that refer to the disk.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node1 data web webbaz

    [ "$(LXD_DIR=${LXD_TWO_DIR} lxc config device get c1 web source)" = "webbaz" ]
    [ "$(LXD_DIR=${LXD_TWO_DIR} lxc config device get c2 web source)" = "web" ]

    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node2 data web webbaz

    [ "$(LXD_DIR=${LXD_TWO_DIR} lxc config device get c1 web source)" = "webbaz" ]
    [ "$(LXD_DIR=${LXD_TWO_DIR} lxc config device get c2 web source)" = "webbaz" ]

    LXD_DIR="${LXD_TWO_DIR}" lxc config device remove c1 web

    # Renaming a local storage volume when attached via profile succeeds.
    LXD_DIR="${LXD_TWO_DIR}" lxc profile create stovol-webbaz
    LXD_DIR="${LXD_TWO_DIR}" lxc profile device add stovol-webbaz webbaz disk pool=data source=webbaz path=/mnt/web

    LXD_DIR="${LXD_TWO_DIR}" lxc profile add c3 stovol-webbaz # c2 and c3 both have webbaz attached

    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node2 data webbaz webbaz2
    [ "$(LXD_DIR=${LXD_TWO_DIR} lxc profile device get stovol-webbaz webbaz source)" = "webbaz2" ]
    [ "$(LXD_DIR=${LXD_TWO_DIR} lxc config device get c2 web source)" = "webbaz2" ]

    LXD_DIR="${LXD_TWO_DIR}" lxc profile remove c3 stovol-webbaz
    LXD_DIR="${LXD_TWO_DIR}" lxc profile delete stovol-webbaz

    # Clean up.
    LXD_DIR="${LXD_TWO_DIR}" lxc delete c1 c2 c3

    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete --target node2 data webbaz2

    # Since now there's only one volume in the pool left named webbaz,
    # it's possible to delete it without specifying --target.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete data webbaz
  fi

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_TWO_DIR}" lxc storage delete data

  # Ensure there are no left over storage pools in the preseeded cluster.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc storage list -f json | jq length)" = "0" ]

  # Trying to pass config values other than 'source' results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir source=/foo size=123 --target node1 || false

  # Test storage pool node state tracking using a dir pool.
  if [ "${poolDriver}" = "dir" ]; then
    # Create pending nodes.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" --target node1
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" --target node2
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'")" = "node1,0" ]
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'")" = "node2,0" ]

    # Modify first pending node with invalid config and check it fails and all nodes are pending.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage set pool1 source=/tmp/not/exist --target node1
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" || false
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'")" = "node1,0" ]
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'")" = "node2,0" ]

    # Run create on second node, so it succeeds and then fails notifying first node.
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" || false
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'")" = "node1,0" ]
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'")" = "node2,1" ]

    # Check we cannot update global config while in pending state.
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage set pool1 rsync.bwlimit 10 || false
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage set pool1 rsync.bwlimit 10 || false

    # Check can delete pending pool and created nodes are cleaned up.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 --target=node2
    LXD_TWO_SOURCE="$(LXD_DIR="${LXD_TWO_DIR}" lxc storage get pool1 source --target=node2)"
    stat "${LXD_TWO_SOURCE}/containers"
    LXD_DIR="${LXD_ONE_DIR}" lxc storage delete pool1
    ! stat "${LXD_TWO_SOURCE}/containers" || false

    # Create new partially created pool and check we can fix it.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" source=/tmp/not/exist --target node1
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" --target node2
    LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F status: | grep -wF Pending
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" || false
    LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F status: | grep -wF Errored
    LXD_DIR="${LXD_ONE_DIR}" lxc storage unset pool1 source --target node1
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" rsync.bwlimit=1000 || false # Check global config is rejected on re-create.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}"
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" || false # Check re-create after successful create is rejected.
    LXD_ONE_SOURCE="$(LXD_DIR="${LXD_ONE_DIR}" lxc storage get pool1 source --target=node1)"
    LXD_TWO_SOURCE="$(LXD_DIR="${LXD_TWO_DIR}" lxc storage get pool1 source --target=node2)"
    stat "${LXD_ONE_SOURCE}/containers"
    stat "${LXD_TWO_SOURCE}/containers"

    # Check both nodes marked created.
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'")" = "node1,1" ]
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'")" = "node2,1" ]

    # Check copying storage volumes works.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 vol1 --target=node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2 --refresh
    LXD_DIR="${LXD_ONE_DIR}" lxc project create foo

    # Check project-specific node settings work.
    LXD_DIR="${LXD_ONE_DIR}" lxc config set storage.project.foo.images_volume=pool1/vol1
    LXD_DIR="${LXD_TWO_DIR}" lxc config set storage.project.foo.images_volume=pool1/vol1
    LXD_DIR="${LXD_ONE_DIR}" lxc config set storage.project.foo.backups_volume=pool1/vol1
    LXD_DIR="${LXD_TWO_DIR}" lxc config set storage.project.foo.backups_volume=pool1/vol1
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get "storage.project.foo.images_volume")" = "pool1/vol1" ]
    [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc config get "storage.project.foo.images_volume")" = "pool1/vol1" ]
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get "storage.project.foo.backups_volume")" = "pool1/vol1" ]
    [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc config get "storage.project.foo.backups_volume")" = "pool1/vol1" ]
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol1 --target=node1 || false
    LXD_DIR="${LXD_ONE_DIR}" lxc config unset storage.project.foo.images_volume
    LXD_DIR="${LXD_TWO_DIR}" lxc config unset storage.project.foo.images_volume
    LXD_DIR="${LXD_ONE_DIR}" lxc config unset storage.project.foo.backups_volume
    LXD_DIR="${LXD_TWO_DIR}" lxc config unset storage.project.foo.backups_volume

    # Check copying storage volumes works on projects.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2 --target-project foo
    ! LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo || false

    # Check snapshotting storage volumes works.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume snapshot pool1 custom/vol1 snapNode1 --target=node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume snapshot pool1 custom/vol1 snapNode2 --target=node2
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc storage volume info pool1 custom/vol1 --target=node1 | grep -cF snapNode1)" = 1 ]
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc storage volume info pool1 custom/vol1 --target=node2 | grep -cF snapNode2)" = 1 ]

    # Check renaming storage volume works.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 vol2 --target=node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume move pool1/vol2 pool1/vol3 --target=node1
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show pool1 vol3 | grep -wF node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume move pool1/vol3 pool1/vol2 --target=node1 --destination-target=node2
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show pool1 vol2 | grep -wF node2
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume rename pool1 vol2 vol3 --target=node2
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show pool1 vol3 | grep -wF node2

    # Delete pool and check cleaned up.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol1 --target=node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol1 --target=node2
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol1 --target=node2 --project=foo
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol3 --target=node2
    LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo
    LXD_DIR="${LXD_TWO_DIR}" lxc storage delete pool1
    ! stat "${LXD_ONE_SOURCE}/containers" || false
    ! stat "${LXD_TWO_SOURCE}/containers" || false
  fi

  # Set up node-specific storage pool keys for the selected backend.
  driver_config=""
  if [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "lvm" ] || [ "${poolDriver}" = "zfs" ]; then
      driver_config="size=1GiB"
  fi

  if [ "${poolDriver}" = "ceph" ]; then
      driver_config="source=lxdtest-$(basename "${TEST_DIR}")-pool1"
  fi

  # Define storage pools on the two nodes
  driver_config_node1="${driver_config}"
  driver_config_node2="${driver_config}"

  if [ "${poolDriver}" = "zfs" ]; then
      driver_config_node1="${driver_config_node1} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
      driver_config_node2="${driver_config_node1} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns2}"
  fi

  if [ "${poolDriver}" = "lvm" ]; then
      driver_config_node1="${driver_config_node1} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
      driver_config_node2="${driver_config_node1} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns2}"
  fi

  if [ -n "${driver_config_node1}" ]; then
    # shellcheck disable=SC2086
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" ${driver_config_node1} --target node1
  else
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" --target node1
  fi

  LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -wF node1
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -wF node2 || false
  if [ -n "${driver_config_node2}" ]; then
    # shellcheck disable=SC2086
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" ${driver_config_node2} --target node2
  else
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" --target node2
  fi
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F status: | grep -wF Pending

  # A container can't be created when associated with a pending pool.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -s pool1 --empty bar || false

  # The source config key is not legal for the final pool creation
  if [ "${poolDriver}" = "dir" ]; then
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir source=/foo || false
  fi

  # Create the storage pool
  if [ "${poolDriver}" = "lvm" ]; then
      LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" volume.size="${DEFAULT_VOLUME_SIZE}"
  elif [ "${poolDriver}" = "ceph" ]; then
      LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" volume.size="${DEFAULT_VOLUME_SIZE}" ceph.osd.pg_num=16
  else
      LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}"
  fi
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F status: | grep -wF Created

  # Add the new pool to the default profile
  LXD_DIR="${LXD_ONE_DIR}" lxc profile device add default root disk pool=pool1 path=/

  # The 'source' config key is omitted when showing the cluster
  # configuration, and included when showing the node-specific one.
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -wF source || false
  source1="$(basename "${LXD_ONE_DIR}")"
  source2="$(basename "${LXD_TWO_DIR}")"
  if [ "${poolDriver}" = "ceph" ]; then
    # For ceph volume the source field is the name of the underlying ceph pool
    source1="lxdtest-$(basename "${TEST_DIR}")"
    source2="${source1}"
  fi
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 --target node1 | grep -wF source | grep -F "${source1}"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 --target node2 | grep -wF source | grep -F "${source2}"

  # Update the storage pool
  if [ "${poolDriver}" = "dir" ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc storage set pool1 rsync.bwlimit 10
    [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc storage get pool1 rsync.bwlimit)" = "10" ]
    LXD_DIR="${LXD_TWO_DIR}" lxc storage unset pool1 rsync.bwlimit
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F rsync.bwlimit || false
  fi

  if [ "${poolDriver}" = "ceph" ]; then
    # Test migration of ceph-based containers
    LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
    LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 -s pool1 testimage foo

    # The container can't be moved if it's running
    ! LXD_DIR="${LXD_TWO_DIR}" lxc move foo --target node1 || false

    # Stop the container and create a snapshot
    LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force
    LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo snap-test

    # Move the container to node1
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo --target node1
    [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L foo)" = "node1" ]
    LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -wF "snap-test"

    # Start and stop the container on its new node1 host
    LXD_DIR="${LXD_TWO_DIR}" lxc start foo
    LXD_DIR="${LXD_TWO_DIR}" lxc stop foo --force

    # Init a new container on node2 using the snapshot on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc copy foo/snap-test egg --target node2
    LXD_DIR="${LXD_TWO_DIR}" lxc start egg
    LXD_DIR="${LXD_ONE_DIR}" lxc delete egg --force
  fi

  # If the driver has the same per-node storage pool config (e.g. size), make sure it's included in the
  # member_config, and actually added to a joining node so we can validate it.
  if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "ceph" ] || [ "${poolDriver}" = "lvm" ]; then
    # Spawn a third node
    setup_clustering_netns 3
    LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    # shellcheck disable=SC2154
    ns3="${prefix}3"
    LXD_NETNS="${ns3}" spawn_lxd "${LXD_THREE_DIR}" false

    key=$(echo "${driver_config}" | cut -d'=' -f1)
    value=$(echo "${driver_config}" | cut -d'=' -f2-)

    # Set member_config to match `spawn_lxd_and_join_cluster` for 'data' and `driver_config` for 'pool1'.
    member_config='{"entity": "storage-pool","name":"pool1","key":"'"${key}"'","value":"'"${value}"'"}'
    if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "lvm" ] ; then
      member_config='{"entity": "storage-pool","name":"data","key":"size","value":"1GiB"},'"${member_config}"
    fi

    # Manually send the join request.
    local cert_json
    cert_json="$(cert_to_json "${LXD_ONE_DIR}/cluster.crt")"
    token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add node3 --quiet)"
    op="$(curl --silent --unix-socket "${LXD_THREE_DIR}/unix.socket" --fail-with-body -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d '{"server_name":"node3","enabled":true,"member_config":['"${member_config}"'],"server_address":"100.64.1.103:8443","cluster_address":"100.64.1.101:8443","cluster_certificate":'"${cert_json}"',"cluster_token":"'"${token}"'"}' | jq --exit-status --raw-output '.operation')"
    curl --silent --unix-socket "${LXD_THREE_DIR}/unix.socket" --fail-with-body "lxd${op}/wait"

    # Ensure that node-specific config appears on all nodes,
    # regardless of the pool being created before or after the node joined.
    for n in node1 node2 node3 ; do
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc storage get pool1 "${key}" --target "${n}")" = "${value}" ]
    done

    # Other storage backends will be finished with the third node, so we can remove it.
    if [ "${poolDriver}" != "ceph" ]; then
      LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --yes
    fi
  fi

  if [ "${poolDriver}" = "ceph" ]; then
    # Move the container to node3, renaming it
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo bar --target node3
    [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L bar)" = "node3" ]
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -wF "snap-test"

    # Shutdown node 3, and wait for it to be considered offline.
    LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 11
    LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
    sleep 11

    # Move the container back to node2, even if node3 is offline
    LXD_DIR="${LXD_ONE_DIR}" lxc move bar --target node2
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L bar)" = "node2" ]
    LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -wF "snap-test"

    # Start and stop the container on its new node2 host
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --force --yes

    LXD_DIR="${LXD_ONE_DIR}" lxc delete bar

    # Attach a custom volume to a container on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 v1
    LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 -s pool1 --empty baz
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume attach pool1 custom/v1 baz testDevice /opt

    # Trying to attach a custom volume to a container on another node fails
    LXD_DIR="${LXD_TWO_DIR}" lxc init --target node2 -s pool1 --empty buz
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume attach pool1 custom/v1 buz testDevice /opt || false

    # Create an unrelated volume and rename it on a node which differs from the
    # one running the container (issue #6435).
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume create pool1 v2
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename pool1 v2 v2-renamed
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete pool1 v2-renamed

    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume detach pool1 v1 baz

    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 v1
    LXD_DIR="${LXD_ONE_DIR}" lxc delete baz buz

    LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  fi

  # Test migration of zfs/btrfs-based containers
  if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ]; then
    # Launch a container on node2
    LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
    LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 testimage foo
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L foo)" = "node2" ]

    # Stop the container and move it to node1
    LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo bar --target node1
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L bar)" = "node1" ]

    # Start and stop the migrated container on node1
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    # Rename the container locally on node1
    LXD_DIR="${LXD_TWO_DIR}" lxc rename bar foo
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L foo)" = "node1" ]

    # Copy the container without specifying a target, it will be placed on node2
    # since it's the one with the least number of containers (0 vs 1)
    sleep 6 # Wait for pending operations to be removed from the database
    LXD_DIR="${LXD_ONE_DIR}" lxc copy foo bar
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L bar)" = "node2" ]

    # Start and stop the copied container on node2
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    # Purge the containers
    LXD_DIR="${LXD_ONE_DIR}" lxc delete bar foo

    # Delete the image too.
    LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  fi

  # Delete the storage pool
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete pool1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF pool1 || false

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  if [ -n "${LXD_THREE_DIR:-}" ]; then
    kill_lxd "${LXD_THREE_DIR}"
  fi
}

# On a single-node cluster storage pools can be created either with the
# two-stage process required multi-node clusters, or directly with the normal
# procedure for non-clustered daemons.
test_clustering_storage_single_node() {
  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  local poolDriver
  poolDriver="$(storage_backend "${LXD_INITIAL_DIR}")"

  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  # Create a pending storage pool on the node.
  driver_config=""
  if [ "${poolDriver}" = "btrfs" ]; then
      driver_config="size=1GiB"
  fi
  if [ "${poolDriver}" = "zfs" ]; then
      driver_config="size=1GiB"
  fi
  if [ "${poolDriver}" = "ceph" ]; then
      driver_config="source=lxdtest-$(basename "${TEST_DIR}")-pool1"
  fi
  driver_config_node="${driver_config}"
  if [ "${poolDriver}" = "zfs" ]; then
      driver_config_node="${driver_config_node} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
  fi

  if [ -n "${driver_config_node}" ]; then
    # shellcheck disable=SC2086
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" ${driver_config_node} --target node1
  else
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" --target node1
  fi

  # Finalize the storage pool creation
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F status: | grep -wF Created

  # Delete the storage pool
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete pool1

  # Create the storage pool directly, without the two-stage process.
  if [ -n "${driver_config_node}" ]; then
    # shellcheck disable=SC2086
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" ${driver_config_node}
  else
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}"
  fi

  # Delete the storage pool
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete pool1

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
}

test_clustering_network() {
  spawn_lxd_and_bootstrap_cluster

  # The state of the preseeded network shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network list | grep -F "${bridge}" | grep -wF CREATED

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Create a project with restricted.networks.subnets set to check the default networks are created before projects
  # when a member joins the cluster.
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${bridge}" ipv4.routes=192.0.2.0/24
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo \
    -c restricted=true \
    -c features.networks=true \
    -c restricted.networks.subnets="${bridge}":192.0.2.0/24

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # The state of the preseeded network is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network list | grep -F "${bridge}" | grep -wF CREATED

  # Check both nodes show network created.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${bridge}' AND nodes.name = 'node1'")" = "node1,1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${bridge}' AND nodes.name = 'node2'")" = "node2,1" ]

  # Trying to pass config values other than
  # 'bridge.external_interfaces' results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create foo ipv4.address=auto --target node1 || false

  net="${bridge}x"

  # Define networks on the two nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_TWO_DIR}" lxc network show  "${net}" | grep -wF node1
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network show "${net}" | grep -wF node2 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Pending

  # A container can't be created when its NIC is associated with a pending network.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -n "${net}" --empty bar || false

  # The bridge.external_interfaces config key is not legal for the final network creation
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" bridge.external_interfaces=foo || false

  # Since the lxc create command cannot create a network with a description, create a network with a description using the API.
  LXD_DIR="${LXD_ONE_DIR}" lxc query -X POST /1.0/networks --data "{
    \"name\": \"${net}\",
    \"type\": \"bridge\",
    \"description\": \"bar\",
    \"config\": {
      \"ipv4.address\": \"none\",
      \"ipv6.address\": \"none\"
    }
  }"

  # Verify network is now created with description
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Created
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc network get --property "${net}" description)" = "bar" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" --target node2 | grep -F status: | grep -wF Created

  # FIXME: rename the network is not supported with clustering
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network rename "${net}" "${net}-foo" || false

  # Delete the networks
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${net}"
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  LXD_PID1="$(< "${LXD_ONE_DIR}/lxd.pid")"
  LXD_PID2="$(< "${LXD_TWO_DIR}/lxd.pid")"

  # Test network create partial failures.
  nsenter -n -t "${LXD_PID1}" -- ip link add "${net}" type dummy # Create dummy interface to conflict with network.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Pending # Check has pending status.

  # Run network create on other node1 (expect this to fail early due to existing interface).
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Errored # Check has errored status.

  # Check each node status (expect both node1 and node2 to be pending as local member running created failed first).
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'")" = "node1,0" ]
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'")" = "node2,0" ]

  # Run network create on other node2 (still excpect to fail on node1, but expect node2 create to succeed).
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network create "${net}" || false

  # Check each node status (expect node1 to be pending and node2 to be created).
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'")" = "node1,0" ]
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'")" = "node2,1" ]

  # Check interfaces are expected types (dummy on node1 and bridge on node2).
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep -wF dummy
  nsenter -n -t "${LXD_PID2}" -- ip -details link show "${net}" | grep -wF bridge

  # Check we cannot update network global config while in pending state on either node.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network set "${net}" ipv4.dhcp false || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network set "${net}" ipv4.dhcp false || false

  # Check we can update node-specific config on the node that has been created (and that it is applied).
  nsenter -n -t "${LXD_PID2}" -- ip link add "ext-${net}" type dummy # Create dummy interface to add to bridge.
  LXD_DIR="${LXD_TWO_DIR}" lxc network set "${net}" bridge.external_interfaces "ext-${net}" --target node2
  nsenter -n -t "${LXD_PID2}" -- ip link show "ext-${net}" | grep -wF "master ${net}"

  # Check we can update node-specific config on the node that hasn't been created (and that only DB is updated).
  nsenter -n -t "${LXD_PID1}" -- ip link add "ext-${net}" type dummy # Create dummy interface to add to bridge.
  nsenter -n -t "${LXD_PID1}" -- ip address add 192.0.2.1/32 dev "ext-${net}" # Add address to prevent attach.
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${net}" bridge.external_interfaces "ext-${net}" --target node1
  ! nsenter -n -t "${LXD_PID1}" -- ip link show "ext-${net}" | grep -wF "master ${net}" || false  # Don't expect to be attached.

  # Delete partially created network and check nodes that were created are cleaned up.
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net}"
  ! nsenter -n -t "${LXD_PID2}" -- ip link show "${net}" || false # Check bridge is removed.
  nsenter -n -t "${LXD_PID2}" -- ip link show "ext-${net}" # Check external interface still exists.
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep -wF dummy # Check node1 conflict still exists.

  # Create new partially created network and check we can fix it.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.address=192.0.2.1/24 ipv6.address=2001:db8::1/64 || false  # Fails due to NIC conflict but will set ipv{4,6}.address
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Errored # Check has errored status.
  nsenter -n -t "${LXD_PID1}" -- ip link delete "${net}" # Remove conflicting interface.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.dhcp=false || false # Check supplying global config on re-create is blocked.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" # Check re-create succeeds.
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Created # Check is created after fix.
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep -wF bridge # Check bridge exists.
  nsenter -n -t "${LXD_PID2}" -- ip -details link show "${net}" | grep -wF bridge # Check bridge exists.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.address=192.0.2.1/24 ipv6.address=2001:db8::1/64 || false # Check re-create is blocked after success.

  # Check both nodes marked created.
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'")" = "node1,1" ]
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'")" = "node2,1" ]

  # Check instance can be connected to created network and assign static DHCP allocations.
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 -n "${net}" --empty c1
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c1 eth0 ipv4.address=192.0.2.2

  # Check cannot assign static IPv6 without stateful DHCPv6 enabled.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c1 eth0 ipv6.address=2001:db8::2 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${net}" ipv6.dhcp.stateful=true
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c1 eth0 ipv6.address=2001:db8::2

  # Check duplicate static DHCP allocation detection is working for same server as c1.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 -n "${net}" --empty c2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c2 eth0 ipv4.address=192.0.2.2 || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c2 eth0 ipv6.address=2001:db8::2 || false

  # Check duplicate static DHCP allocation is allowed for instance on a different server.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -n "${net}" --empty c3
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c3 eth0 ipv4.address=192.0.2.2
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c3 eth0 ipv6.address=2001:db8::2

  # Check duplicate MAC address assignment detection is working using both network and parent keys.
  c1MAC=$(LXD_DIR="${LXD_ONE_DIR}" lxc config get c1 volatile.eth0.hwaddr)
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c2 eth0 hwaddr="${c1MAC}" || false
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c3 eth0 hwaddr="${c1MAC}"

  # Check duplicate static MAC assignment detection is working for same server as c1.
  LXD_DIR="${LXD_ONE_DIR}" lxc config device remove c2 eth0
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device add c2 eth0 nic hwaddr="${c1MAC}" nictype=bridged parent="${net}" || false

  # Check duplicate static MAC assignment is allowed for instance on a different server.
  LXD_DIR="${LXD_ONE_DIR}" lxc config device remove c3 eth0
  LXD_DIR="${LXD_ONE_DIR}" lxc config device add c3 eth0 nic hwaddr="${c1MAC}" nictype=bridged parent="${net}"

  # Check networks local to a cluster member show up when targeting that member
  # and hidden when targeting other cluster members. Setup is in includes/clustering.sh
  LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node1 | grep -wF localBridge1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node1 | grep -wF localBridge2 || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node2 | grep -wF localBridge1 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node2 | grep -wF localBridge2

  # Cleanup instances and image.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3

  # Delete network.
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net}"
  ! nsenter -n -t "${LXD_PID1}" -- ip link show "${net}" || false # Check bridge is removed.
  ! nsenter -n -t "${LXD_PID2}" -- ip link show "${net}" || false # Check bridge is removed.

  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo

  echo "Test creating physical networks."
  net1="${prefix}network1"
  net2="${prefix}network2"

  echo "Create two dummy interfaces (i1 and i2) on both nodes."
  nsenter -n -t "${LXD_PID1}" -- ip link add i1 type dummy
  nsenter -n -t "${LXD_PID1}" -- ip link add i2 type dummy
  nsenter -n -t "${LXD_PID2}" -- ip link add i1 type dummy
  nsenter -n -t "${LXD_PID2}" -- ip link add i2 type dummy

  echo "Create a physical network net1."
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net1}" --type=physical parent=i1 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net1}" --type=physical parent=i1 --target=node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net1}" --type=physical
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net1}" | grep -xF 'status: Created'

  echo "Check that parent interface i1 on node1 cannot be used for another physical network net2."
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical parent=i1 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical parent=i2 --target=node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net2}" | grep -xF 'status: Errored'
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net2}"

  echo "Check that parent interface i1 on node2 cannot be used for another physical network net2."
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical parent=i2 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical parent=i1 --target=node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net2}" | grep -xF 'status: Errored'
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net2}"

  echo "Create a physical network net2."
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical parent=i2 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical parent=i2 --target=node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net2}" --type=physical
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net2}" | grep -xF 'status: Created'

  echo "Clean up physical networks."
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net2}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net1}"

  echo "Delete dummy interfaces."
  nsenter -n -t "${LXD_PID1}" -- ip link delete i2
  nsenter -n -t "${LXD_PID1}" -- ip link delete i1
  nsenter -n -t "${LXD_PID2}" -- ip link delete i2
  nsenter -n -t "${LXD_PID2}" -- ip link delete i1

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_heal_networks_stop() {
  echo "==> Test: cluster healing does not shut down networks on the leader node when evacuating an offline member"
  # Regression test for https://github.com/canonical/lxd/issues/16642.

  echo "Create a cluster with 3 nodes"
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  echo "Create bridge network to start BGP listener on"
  bgpbr="${prefix}bgpbr"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${bgpbr}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${bgpbr}" --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${bgpbr}" --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${bgpbr}" ipv4.address=100.64.2.1/24 ipv6.address=fd42:4242:4242:2021::1/64
  bgpIP=$(LXD_DIR="${LXD_ONE_DIR}" lxc network get "${bgpbr}" ipv4.address | cut -d/ -f1)

  echo "Create bridge network on all nodes"
  net="${prefix}net"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.address=192.0.2.1/24 ipv6.address=fd42:4242:4242:1010::1/64

  echo "Verify the network exists on all nodes"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc network list -f csv | grep -cwF "${net}")" = "1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc network list -f csv | grep -cwF "${net}")" = "1" ]
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc network list -f csv | grep -cwF "${net}")" = "1" ]

  echo "Create network forward"
  LXD_DIR="${LXD_ONE_DIR}" lxc network forward create "${net}" 198.51.100.1

  echo "Check forward is exported via BGP prefixes"
  LXD_DIR="${LXD_ONE_DIR}" lxc query /internal/testing/bgp | grep -F "198.51.100.1/32"

  echo "Enable the BGP listener"
  LXD_DIR="${LXD_ONE_DIR}" lxc config set core.bgp_address="${bgpIP}:8874" core.bgp_asn=65536 core.bgp_routerid="${bgpIP}"

  echo "Verify the prefix is exported on the leader before triggering healing"
  LXD_DIR="${LXD_ONE_DIR}" lxc query /internal/testing/bgp # For debugging
  LXD_DIR="${LXD_ONE_DIR}" lxc query /internal/testing/bgp | grep -F "198.51.100.1/32"

  echo "Set offline threshold"
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11

  # Cluster healing will be triggered using the /internal/testing/cluster/heal endpoint.
  # "cluster.healing_threshold" must be set.
  echo "Enable cluster healing"
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.healing_threshold 11

  echo "Kill node2 (a non-leader member)"
  # XXX: intentionally not using `kill_go_proc` helper as we want abrupt termination (sacrificing some coverage data)
  kill -9 "$(< "${LXD_TWO_DIR}/lxd.pid")"

  echo "Wait for node2 to be marked offline"
  sleep 11

  echo "Trigger cluster healing"
  LXD_DIR="${LXD_ONE_DIR}" lxc query -X POST --raw --wait /internal/testing/cluster/heal

  echo "Verify BGP prefix is still exported on the leader after healing"
  # Expected: after healing, the leader should still be exporting the forward prefix
  LXD_DIR="${LXD_ONE_DIR}" lxc query /internal/testing/bgp # For debugging
  LXD_DIR="${LXD_ONE_DIR}" lxc query /internal/testing/bgp | grep -F "198.51.100.1/32"

  echo "Clean up"
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

# Perform an upgrade of a 2-member cluster, then a join a third member and
# perform one more upgrade
test_clustering_upgrade() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Respawn the second node, making it believe it has an higher
  # version than it actually has.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=1
  shutdown_lxd "${LXD_TWO_DIR}"
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false

  # The second daemon is blocked waiting for the other to be upgraded
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5 || false

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -F "message: LXD version is older than other members"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "message: LXD version is newer than other members"

  # Respawn the first node, so it matches the version the second node
  # believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The second daemon has now unblocked
  LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=30

  # The cluster is again operational
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -c "Fully operational")" -eq 2 ]

  # Now spawn a third node and test the upgrade with a 3-node cluster.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Respawn the second node, making it believe it has an higher
  # version than it actually has.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=2
  shutdown_lxd "${LXD_TWO_DIR}"
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false

  # The second daemon is blocked waiting for the other two to be
  # upgraded
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5 || false

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -F "message: LXD version is older than other members"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "message: LXD version is newer than other members"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node3 | grep -F "message: LXD version is older than other members"

  # Respawn the first node and third node, so they match the version
  # the second node believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" false
  shutdown_lxd "${LXD_THREE_DIR}"
  LXD_NETNS="${ns3}" respawn_lxd "${LXD_THREE_DIR}" true

  # The cluster is again operational
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -c "Fully operational")" -eq 3 ]

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

# Perform a downgrade of a 2-member cluster, then a join a third member and perform one more downgrade.
test_clustering_downgrade() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Respawn the second node, making it believe it has an lower version than it actually has.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=-1
  shutdown_lxd "${LXD_TWO_DIR}"
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false

  # The second daemon is blocked waiting for the other to be upgraded
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5 || false

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -F "message: LXD version is newer than other members"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "message: LXD version is older than other members"

  # Respawn the first node, so it matches the version the second node believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The second daemon has now unblocked
  LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=30

  # The cluster is again operational
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -c "Fully operational")" -eq 2 ]

  # Now spawn a third node and test the upgrade with a 3-node cluster.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Respawn the second node, making it believe it has an lower version than it actually has.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=-2
  shutdown_lxd "${LXD_TWO_DIR}"
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false

  # The second daemon is blocked waiting for the other two to be upgraded.
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5 || false

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -F "message: LXD version is newer than other members"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "message: LXD version is older than other members"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node3 | grep -F "message: LXD version is newer than other members"

  # Respawn the first node and third node, so they match the version the second node believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" false
  shutdown_lxd "${LXD_THREE_DIR}"
  LXD_NETNS="${ns3}" respawn_lxd "${LXD_THREE_DIR}" true

  # The cluster is again operational.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -c "Fully operational")" -eq 3 ]

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

# Perform an upgrade of an 8-member cluster.
test_clustering_upgrade_large() {
  local N=8
  local LXD_CLUSTER_DIR
  LXD_CLUSTER_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"

  LXD_DIR_KEEP="${LXD_CLUSTER_DIR}/1" spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  for i in $(seq 2 "${N}"); do
    LXD_DIR_KEEP="${LXD_CLUSTER_DIR}/${i}" spawn_lxd_and_join_cluster "${cert}" "${i}" 1 "${LXD_ONE_DIR}"
  done

  # Respawn all nodes in sequence, as if their version had been upgrade.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=1
  for i in $(seq "${N}" -1 1); do
    shutdown_lxd "${LXD_CLUSTER_DIR}/${i}"
    LXD_NETNS="${prefix}${i}" respawn_lxd "${LXD_CLUSTER_DIR}/${i}" false
  done

  LXD_DIR="${LXD_ONE_DIR}" lxd waitready --timeout=10
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "OFFLINE" || false

  for i in $(seq "${N}" -1 1); do
    LXD_DIR="${LXD_CLUSTER_DIR}/${i}" lxd shutdown
  done

  for i in $(seq "${N}"); do
    rm -f "${LXD_CLUSTER_DIR}/${i}/unix.socket"
  done

  teardown_clustering_netns
  teardown_clustering_bridge

  for i in $(seq "${N}"); do
    kill_lxd "${LXD_CLUSTER_DIR}/${i}"
  done
}

test_clustering_publish() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Give LXD a couple of seconds to get event API connected properly
  sleep 2

  # Init a container on node2, using a client connected to node1
  sub_test "Test image publishing from instance and snapshot"
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage foo

  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo --alias=foo-image
  LXD_DIR="${LXD_ONE_DIR}" lxc image show foo-image | grep -F "public: false"
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete foo-image

  LXD_DIR="${LXD_TWO_DIR}" lxc snapshot foo backup
  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo/backup --alias=foo-backup-image
  LXD_DIR="${LXD_ONE_DIR}" lxc image show foo-backup-image | grep -F "public: false"

  LXD_DIR="${LXD_ONE_DIR}" lxc image delete foo-backup-image
  LXD_DIR="${LXD_ONE_DIR}" lxc delete foo --force

  sub_test "Test image publishing in project with disabled image feature"
  project="img-publish-test"
  LXD_DIR="${LXD_ONE_DIR}" lxc project create "${project}"
  LXD_DIR="${LXD_ONE_DIR}" lxc project set "${project}" features.images=false
  LXD_DIR="${LXD_ONE_DIR}" lxc project set "${project}" features.storage.volumes=false
  LXD_DIR="${LXD_ONE_DIR}" lxc project set "${project}" features.profiles=false

  # Create and publish instance as an image in that project.
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage foo --project "${project}"
  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo --project "${project}" --alias foo-image

  # Cleanup
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete foo-image --project "${project}"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete foo --force --project "${project}"
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete "${project}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_profiles() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  # TODO: Fix known race in importing small images that complete before event listener is setup.
  sleep 1

  # Create an empty profile.
  LXD_DIR="${LXD_TWO_DIR}" lxc profile create web

  # Launch two containers on the two nodes, using the above profile.
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 -p default -p web testimage c1
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 -p default -p web testimage c2

  # Edit the profile.
  local source
  source="$(mktemp -d -p "${TEST_DIR}" XXX)"
  touch "${source}/hello"
  chmod 755 "${source}"
  chmod 644 "${source}/hello"
  LXD_DIR="${LXD_TWO_DIR}" lxc profile edit web <<EOF
config: {}
description: ""
devices:
  web:
    path: /mnt
    source: "${source}"
    type: disk
name: web
used_by:
- /1.0/instances/c1
- /1.0/instances/c2
EOF

  LXD_DIR="${LXD_TWO_DIR}" lxc exec c1 -- test -e /mnt/hello
  LXD_DIR="${LXD_TWO_DIR}" lxc exec c2 -- test -e /mnt/hello

  LXD_DIR="${LXD_TWO_DIR}" lxc stop c1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc stop c2 --force

  rm -rf "${source}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_update_cert() {
  spawn_lxd_and_bootstrap_cluster

  local cert_path key_path
  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${LXD_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_lxd "${LXD_ONE_DIR}"

  # Set up again
  # Bootstrap the first node
  LXD_DIR_KEEP="${LXD_ONE_DIR}" spawn_lxd_and_bootstrap_cluster

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Send update request
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster update-cert "${cert_path}" "${key_path}" -q

  cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cmp -s "${LXD_TWO_DIR}/cluster.crt" "${cert_path}"

  cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}"
  cmp -s "${LXD_TWO_DIR}/cluster.key" "${key_path}"

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -F "server_name: node1"

  rm "${cert_path}" "${key_path}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_update_cert_reversion() {
  spawn_lxd_and_bootstrap_cluster

  local cert_path key_path
  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${LXD_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_lxd "${LXD_ONE_DIR}"

  # Set up again
  # Bootstrap the first node
  LXD_DIR_KEEP="${LXD_ONE_DIR}" spawn_lxd_and_bootstrap_cluster

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Shutdown third node
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  kill_lxd "${LXD_THREE_DIR}"

  # Send update request
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster update-cert "${cert_path}" "${key_path}" -q || false

  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_TWO_DIR}/cluster.crt" "${cert_path}" || false

  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false
  ! cmp -s "${LXD_TWO_DIR}/cluster.key" "${key_path}" || false

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -F "server_name: node1"

  LXD_DIR="${LXD_ONE_DIR}" lxc warning list | grep -F "Unable to update cluster certificate"

  rm "${cert_path}" "${key_path}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_update_cert_token() {
  spawn_lxd_and_bootstrap_cluster

  local cert_path key_path
  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${LXD_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_lxd "${LXD_ONE_DIR}"

  # Set up again
  # Bootstrap the first node
  LXD_DIR_KEEP="${LXD_ONE_DIR}" spawn_lxd_and_bootstrap_cluster

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Get a token embedding the current cluster cert fingerprint
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"

  # Change the cluster cert
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster update-cert "${cert_path}" "${key_path}" -q

  cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cmp -s "${LXD_TWO_DIR}/cluster.crt" "${cert_path}"

  cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}"
  cmp -s "${LXD_TWO_DIR}/cluster.key" "${key_path}"

  # Verify the token with the wrong cert fingerprint is not usable due to the fingerprint mismatch
  url="https://100.64.1.101:8443"
  ! lxc remote add cluster "${token}" || false
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" lxc remote add cluster "${token}" 2>&1)" = "Error: Certificate fingerprint mismatch between certificate token and server \"${url}\"" ]
  ! lxc remote add cluster --token "${token}" "${url}" || false
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" lxc remote add cluster --token "${token}" "${url}" 2>&1)" = "Error: Certificate fingerprint mismatch between certificate token and server \"${url}\"" ]

  # Get a fresh token embedding the new cluster cert fingerprint
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  lxc remote add cluster "${token}"
  lxc cluster list cluster:
  lxc remote remove cluster

  rm "${cert_path}" "${key_path}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_join_api() {
  spawn_lxd_and_bootstrap_cluster

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  LXD_NETNS="${ns2}" spawn_lxd "${LXD_TWO_DIR}" false

  # Check a join token cannot be created for the reserved name 'none'
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster add none --quiet || false

  local cert_json
  cert_json="$(cert_to_json "${LXD_ONE_DIR}/cluster.crt")"

  # Check a server with the name 'valid' cannot be joined when modifying the token.
  # Therefore replace the valid name in the token with 'none'.
  malicious_token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add valid --quiet | base64 -d | jq --exit-status '.server_name |= "none"' | base64 --wrap=0)"
  op="$(curl --silent --unix-socket "${LXD_TWO_DIR}/unix.socket" --fail-with-body -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d '{"server_name":"valid","enabled":true,"member_config":[{"entity": "storage-pool","name":"data","key":"source","value":""}],"server_address":"100.64.1.102:8443","cluster_address":"100.64.1.101:8443","cluster_certificate":'"${cert_json}"',"cluster_token":"'"${malicious_token}"'"}' | jq --exit-status --raw-output '.operation')"
  curl --silent --unix-socket "${LXD_TWO_DIR}/unix.socket" "lxd${op}/wait" | jq --exit-status '.error_code == 403'

  # Check that the server cannot be joined using a valid token by changing it's name to 'none'.
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add valid2 --quiet)"
  curl --silent --unix-socket "${LXD_TWO_DIR}/unix.socket" -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d '{"server_name":"none","enabled":true,"member_config":[{"entity": "storage-pool","name":"data","key":"source","value":""}],"server_address":"100.64.1.102:8443","cluster_address":"100.64.1.101:8443","cluster_certificate":'"${cert_json}"',"cluster_token":"'"${token}"'"}' | jq --exit-status '.error_code == 400'

  # Check the server can be joined.
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add node2 --quiet)"
  op="$(curl --silent --unix-socket "${LXD_TWO_DIR}/unix.socket" --fail-with-body -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d '{"server_name":"node2","enabled":true,"member_config":[{"entity": "storage-pool","name":"data","key":"source","value":""}],"server_address":"100.64.1.102:8443","cluster_address":"100.64.1.101:8443","cluster_certificate":'"${cert_json}"',"cluster_token":"'"${token}"'"}' | jq --exit-status --raw-output '.operation')"
  curl --silent --unix-socket "${LXD_TWO_DIR}/unix.socket" --fail-with-body "lxd${op}/wait"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "message: Fully operational"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_ONE_DIR}"
}

test_clustering_shutdown_nodes() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Init a container on node1, using a client connected to node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 testimage foo

  # Get container PID
  instance_pid="$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c p foo)"

  # Get server PIDs
  daemon_pid1=$(< "${LXD_ONE_DIR}/lxd.pid")
  daemon_pid2=$(< "${LXD_TWO_DIR}/lxd.pid")
  daemon_pid3=$(< "${LXD_THREE_DIR}/lxd.pid")

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  wait "${daemon_pid2}"

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  wait "${daemon_pid3}"

  # Wait for raft election to take place and become aware that quorum has been lost (should take 3-6s).
  sleep 7

  # Make sure the database is not available to the first node
  ! LXD_DIR="${LXD_ONE_DIR}" timeout -k 5 5 lxc cluster ls || false

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  # Wait for LXD to terminate, otherwise the db will not be empty, and the
  # cleanup code will fail
  wait "${daemon_pid1}"

  # Container foo shouldn't be running anymore
  [ ! -e "/proc/${instance_pid}" ]

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

test_clustering_projects() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Create a test project
  LXD_DIR="${LXD_ONE_DIR}" lxc project create p1
  LXD_DIR="${LXD_ONE_DIR}" lxc project switch p1
  LXD_DIR="${LXD_ONE_DIR}" lxc profile device add default root disk path="/" pool="data"

  # Create a container in the project.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 --empty c1

  # The container is visible through both nodes
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c n)" = "c1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c n)" = "c1" ]

  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1

  # Remove the image file and DB record from node1.
  LXD_DIR="${LXD_TWO_DIR}" lxd sql global 'DELETE FROM images_nodes WHERE node_id = 1'

  # Check image import from node2 by creating container on node1 in other project.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 --empty c2 --project p1
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c2 --project p1

  LXD_DIR="${LXD_ONE_DIR}" lxc project switch default

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_metrics() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Create one running container in each node and a stopped one on the leader.
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 -d "${SMALL_ROOT_DISK}" testimage c1
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 --empty -d "${SMALL_ROOT_DISK}" stopped
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 -d "${SMALL_ROOT_DISK}" testimage c2

  # Check that scraping metrics on each node only includes started instances on that node.
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep 'name="c1"'
  ! LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep 'name="stopped"' || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep 'name="c2"' || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep 'name="c1"' || false
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep 'name="c2"'

  # Stopped container is counted on lxd_instances.
  LXD_DIR="${LXD_ONE_DIR}" lxc query /1.0/metrics | grep -xF 'lxd_instances{project="default",type="container"} 2'
  LXD_DIR="${LXD_TWO_DIR}" lxc query /1.0/metrics | grep -xF 'lxd_instances{project="default",type="container"} 1'

  # Remove previously existing warnings so they don't interfere with tests.
  LXD_DIR="${LXD_ONE_DIR}" lxc warning delete --all

  # Populate database with dummy warnings and check that each node only counts their own warnings.
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{"location": "node1", "type_code": 0, "message": "node1 is in a bad mood"}' /internal/testing/warnings
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{"location": "node1", "type_code": 1, "message": "node1 is bored"}' /internal/testing/warnings
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{"location": "node2", "type_code": 0, "message": "node2 is too cool for this"}' /internal/testing/warnings

  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 2"
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 1"

  # Add a nodeless warning and check if count incremented only on the leader node.
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{"type_code": 0, "message": "nodeless warning"}' /internal/testing/warnings

  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 3"
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 1"

  # Acknowledge/resolve a warning and check if the count decremented on the node relative to the resolved warning.
  uuid=$(LXD_DIR="${LXD_ONE_DIR}" lxc warning list --format json | jq --exit-status --raw-output '.[] | select(.last_message=="node1 is bored") | .uuid')
  LXD_DIR="${LXD_ONE_DIR}" lxc warning ack "${uuid}"

  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 2"
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 1"

  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1 stopped c2
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_address() {
  spawn_lxd_and_bootstrap_cluster "dir" "8444"

  # The bootstrap node appears in the list with its cluster-specific port
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -F :8444
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF "database: true"

  # Add a remote using the core.https_address of the bootstrap node, and check
  # that the REST API is exposed.
  url="https://100.64.1.101:8443"
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  lxc remote add cluster --token "${token}" "${url}"
  lxc storage list cluster: | grep -F data

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node using a custom cluster port
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "dir" "8444"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -F node2
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node2 | grep -xF "database: true"

  # The new node appears with its custom cluster port
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep ^url | grep ':8444$'

  # The core.https_address config value can be changed and the REST API is still
  # accessible.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set "core.https_address" 100.64.1.101:9999
  url="https://100.64.1.101:9999"
  lxc remote set-url cluster "${url}"
  lxc storage list cluster:| grep -wF data

  # The cluster.https_address config value can't be changed.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config set "cluster.https_address" "100.64.1.101:8448" || false

  # Create a container using the REST API exposed over core.https_address.
  lxc init --target node2 --empty cluster:c1
  [ "$(lxc list -f csv -c nL cluster:)" = "c1,node2" ]

  # The core.https_address config value can be set to a wildcard address if
  # the port is the same as cluster.https_address.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set "core.https_address" "0.0.0.0:8444"

  LXD_DIR="${LXD_TWO_DIR}" lxc delete c1

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  lxc remote remove cluster

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_image_replication() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Image replication will be performed across all nodes in the cluster by default
  images_minimal_replica1=$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster.images_minimal_replica)
  images_minimal_replica2=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get cluster.images_minimal_replica)
  [ "$images_minimal_replica1" = "" ]
  [ "$images_minimal_replica2" = "" ]

  # Import the test image on node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  # The image is visible through both nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc image list | grep -wF testimage
  LXD_DIR="${LXD_TWO_DIR}" lxc image list | grep -wF testimage

  # Configure dedicated images storage on node2
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume create data images
  LXD_DIR="${LXD_TWO_DIR}" lxc config set storage.images_volume "data/images"

  # The image tarball is available on both nodes
  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_ONE_DIR}/images/${fingerprint}" ]
  [ -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ]

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Wait for the test image to be synced into the joined node on the background
  retries=10
  while [ "${retries}" != "0" ]; do
    if [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ]; then
        sleep 0.5
        retries=$((retries-1))
        continue
    fi
    break
  done

  if [ "${retries}" -eq 0 ]; then
      echo "Images failed to synced into the joined node"
      return 1
  fi

  # Delete the imported image
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Import the test image on node3
  LXD_DIR="${LXD_THREE_DIR}" ensure_import_testimage

  # The image is visible through all three nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc image list | grep -wF testimage
  LXD_DIR="${LXD_TWO_DIR}" lxc image list | grep -wF testimage
  LXD_DIR="${LXD_THREE_DIR}" lxc image list | grep -wF testimage

  # The image tarball is available on all three nodes
  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_ONE_DIR}/images/${fingerprint}" ]
  [ -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ]
  [ -f "${LXD_THREE_DIR}/images/${fingerprint}" ]

  # Delete the imported image
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ]
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ]
  [ ! -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ]

  # Import the image from the container
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1

  # Modify the container's rootfs and create a new image from the container
  LXD_DIR="${LXD_ONE_DIR}" lxc exec c1 -- touch /a
  LXD_DIR="${LXD_ONE_DIR}" lxc stop c1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc publish c1 --alias new-image

  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info new-image | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_ONE_DIR}/images/${fingerprint}" ]
  [ -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ]
  [ -f "${LXD_THREE_DIR}/images/${fingerprint}" ]

  # Delete the imported image
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete new-image
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the container
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1

  # Delete the imported image
  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Disable the image replication
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.images_minimal_replica 1
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F 'cluster.images_minimal_replica: "1"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F 'cluster.images_minimal_replica: "1"'
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F 'cluster.images_minimal_replica: "1"'

  # Import the test image on node2
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage

  # The image is visible through all three nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc image list | grep -wF testimage
  LXD_DIR="${LXD_TWO_DIR}" lxc image list | grep -wF testimage
  LXD_DIR="${LXD_THREE_DIR}" lxc image list | grep -wF testimage

  # The image tarball is only available on node2
  fingerprint=$(LXD_DIR="${LXD_TWO_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ]
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/storage-pools/data/custom/default_images/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Unset the dedicated image storage on node2
  LXD_DIR="${LXD_TWO_DIR}" lxc config unset storage.images_volume
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete data images

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

test_clustering_dns() {
  # Because we do not want tests to only run on Ubuntu (due to cluster's fan network dependency)
  # instead we will just spawn forkdns directly and check DNS resolution.

  local ipRand forkdns_pid1 forkdns_pid2
  ipRand=$(shuf -i 0-9 -n 1)

  # Create first dummy interface for forkdns
  ip link add "${prefix}1" type dummy
  ip link set "${prefix}1" up
  ip a add 127.0.1.1"${ipRand}"/32 dev "${prefix}1"

  # Create forkdns config directory
  mkdir "${LXD_DIR}"/networks/lxdtest1/forkdns.servers -p

  # Launch forkdns (we expect syslog error about missing servers.conf file)
  lxd forkdns 127.0.1.1"${ipRand}":1053 lxd lxdtest1 &
  forkdns_pid1=$!

  # Create first dummy interface for forkdns
  ip link add "${prefix}2" type dummy
  ip link set "${prefix}2" up
  ip a add 127.0.1.2"${ipRand}"/32 dev "${prefix}2"

  # Create forkdns config directory
  mkdir "${LXD_DIR}"/networks/lxdtest2/forkdns.servers -p

  # Launch forkdns (we expect syslog error about missing servers.conf file)
  lxd forkdns 127.0.1.2"${ipRand}":1053 lxd lxdtest2 &
  forkdns_pid2=$!

  # Let the processes come up
  sleep 0.1

  # Create servers list file for forkdns1 pointing at forkdns2 (should be live reloaded)
  echo "127.0.1.2${ipRand}" > "${LXD_DIR}"/networks/lxdtest1/forkdns.servers/servers.conf.tmp
  mv "${LXD_DIR}"/networks/lxdtest1/forkdns.servers/servers.conf.tmp "${LXD_DIR}"/networks/lxdtest1/forkdns.servers/servers.conf

  # Create fake DHCP lease file on forkdns2 network
  echo "$(date +%s) 00:16:3e:98:05:40 10.140.78.145 test1 ff:2b:a8:0a:df:00:02:00:00:ab:11:36:ea:11:e5:37:e0:85:45" > "${LXD_DIR}"/networks/lxdtest2/dnsmasq.leases

  # Test querying forkdns1 for A record that is on forkdns2 network
  if ! dig @127.0.1.1"${ipRand}" -p1053 test1.lxd | grep -F "10.140.78.145" ; then
    echo "test1.lxd A DNS resolution failed"
    false
  fi

  # Test querying forkdns1 for AAAA record when equivalent A record is on forkdns2 network
  if ! dig @127.0.1.1"${ipRand}" -p1053 AAAA test1.lxd | grep -F "status: NOERROR" ; then
    echo "test1.lxd empty AAAAA DNS resolution failed"
    false
  fi

  # Test querying forkdns1 for PTR record that is on forkdns2 network
  if ! dig @127.0.1.1"${ipRand}" -p1053 -x 10.140.78.145 | grep -F "test1.lxd" ; then
    echo "10.140.78.145 PTR DNS resolution failed"
    false
  fi

  # Test querying forkdns1 for A record that is on forkdns2 network with recursion disabled to
  # ensure request isn't relayed
  if ! dig @127.0.1.1"${ipRand}" -p1053 +norecurse test1.lxd | grep -F "NXDOMAIN" ; then
    echo "test1.lxd A norecurse didnt return NXDOMAIN"
    false
  fi

  # Test querying forkdns1 for PTR record that is on forkdns2 network with recursion disabled to
  # ensure request isn't relayed
  if ! dig @127.0.1.1"${ipRand}" -p1053 +norecurse -x 10.140.78.145 | grep -F "NXDOMAIN" ; then
    echo "10.140.78.145 PTR norecurse didnt return NXDOMAIN"
    false
  fi

  # Cleanup
  kill_go_proc "${forkdns_pid1}"
  kill_go_proc "${forkdns_pid2}"
  ip link delete "${prefix}1"
  ip link delete "${prefix}2"
}

test_clustering_fan() {
  # FAN bridge is not working on Noble+6.14 kernel
  # https://bugs.launchpad.net/ubuntu/+source/linux/+bug/2141703 and https://bugs.launchpad.net/ubuntu/+source/linux/+bug/2141715
  if grep -qxF 'VERSION_ID="24.04"' /etc/os-release && runsMinimumKernel 6.14; then
    local kernel_version
    kernel_version="$(uname -r)"
    export TEST_UNMET_REQUIREMENT="Broken FAN bridge on ${kernel_version} kernel"
    return 0
  fi

  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Import the test image on node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  local fanbridge="${prefix}f"

  echo "Create a fan bridge"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create --target node1 "${fanbridge}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create --target node2 "${fanbridge}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${fanbridge}" bridge.mode=fan dns.domain=fantastic
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${fanbridge}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc network get "${fanbridge}" bridge.mode)" = "fan" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc network get "${fanbridge}" dns.domain)" = "fantastic" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc network get "${fanbridge}" fan.underlay_subnet)" = "100.64.0.0/16" ]

  echo "Create 2 containers"
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 testimage c1 -d "${SMALL_ROOT_DISK}" -n "${fanbridge}"
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 testimage c2 -d "${SMALL_ROOT_DISK}" -n "${fanbridge}"

  echo "Get DHCP leases"
  IP_C1="$(LXD_DIR="${LXD_ONE_DIR}" lxc exec c1 -- udhcpc -f -i eth0 -n -q -t5 2>&1 | awk '/obtained/ {print $4}')"
  IP_C2="$(LXD_DIR="${LXD_ONE_DIR}" lxc exec c2 -- udhcpc -f -i eth0 -n -q -t5 2>&1 | awk '/obtained/ {print $4}')"

  echo "Configure IP addresses"
  LXD_DIR="${LXD_ONE_DIR}" lxc exec c1 -- ip addr add "${IP_C1}"/8 dev eth0
  LXD_DIR="${LXD_ONE_DIR}" lxc exec c2 -- ip addr add "${IP_C2}"/8 dev eth0
  LXD_DIR="${LXD_ONE_DIR}" lxc list

  echo "Check that the containers are reachable from each other using IPs"
  LXD_DIR="${LXD_ONE_DIR}" lxc exec c1 -- ping -nc2 -i0.1 -W1 "${IP_C2}"
  LXD_DIR="${LXD_ONE_DIR}" lxc exec c2 -- ping -nc2 -i0.1 -W1 "${IP_C1}"

  echo "Check that the DHCP leases are cleaned up post-migration"
  grep -qF " c1 " "${LXD_ONE_DIR}/networks/${fanbridge}/dnsmasq.leases"
  LXD_DIR="${LXD_ONE_DIR}" lxc stop -f c1
  LXD_DIR="${LXD_ONE_DIR}" lxc move c1 --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc start c1
  if grep -qF " c1 " "${LXD_ONE_DIR}/networks/${fanbridge}/dnsmasq.leases" ; then
    echo "DHCP lease not released"
    false
  fi

  echo "Cleaning up"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1 c2
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${fanbridge}"

  echo "Tearing down cluster"
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_recover() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check the current database nodes
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -F "100.64.1.101:8443"
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -F "100.64.1.102:8443"
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -F "100.64.1.103:8443"

  # Create a test project, just to insert something in the database.
  LXD_DIR="${LXD_ONE_DIR}" lxc project create p1

  # Trying to recover a running daemon results in an error.
  ! LXD_DIR="${LXD_ONE_DIR}" lxd cluster recover-from-quorum-loss || false

  # Shutdown all nodes.
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  # Now recover the first node and restart it.
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster recover-from-quorum-loss -q
  respawn_lxd_cluster_member "${ns1}" "${LXD_ONE_DIR}"

  # The project we had created is still there
  LXD_DIR="${LXD_ONE_DIR}" lxc project list | grep -wF p1

  # The database nodes have been updated
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -F "100.64.1.101:8443"
  ! LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -F "100.64.1.102:8443" || false

  # Cleanup the dead node.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node2 --force --yes
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --force --yes

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

# Putting HAproxy in front of a cluster allows to use a single address to access
# the cluster, filter out some bogus/spam/malicious requests without terminating
# TLS and while preserving the original client IP addresses.
test_clustering_ha() {
  local successes
  local failures
  local FOUND_RADOSGW

  # Workaround radosgw binding port 80
  FOUND_RADOSGW="false"
  if command -v microceph >/dev/null && ss --no-header -nltp 'sport inet:80' | grep -wF radosgw >/dev/null; then
    FOUND_RADOSGW="true"
    microceph disable rgw
  fi

  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  echo "Get IP:port of all cluster members"
  LXD_ONE_ADDR="$(LXD_DIR="${LXD_ONE_DIR}" lxc config get core.https_address)"
  LXD_TWO_ADDR="$(LXD_DIR="${LXD_TWO_DIR}" lxc config get core.https_address)"

  # Extract host and port of the first member
  LXD_ONE_HOST="$(echo "${LXD_ONE_ADDR}" | cut -d: -f1)"
  LXD_ONE_PORT="$(echo "${LXD_ONE_ADDR}" | cut -d: -f2)"

  echo "Configure HAproxy"
  HOSTNAME="$(hostname)"
  PROXY_PROTOCOL="true"
  CONN_RATE="20"
  setup_haproxy
  configure_haproxy "${HOSTNAME}" "${PROXY_PROTOCOL}" "${CONN_RATE}" "${LXD_ONE_ADDR}" "${LXD_TWO_ADDR}" > /etc/haproxy/haproxy.cfg
  start_haproxy

  # Add a host entry for the HAproxy frontend address
  echo "127.1.2.3 ${HOSTNAME}" >> /etc/hosts

  echo "Get a remote add token"
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"

  if [ "${PROXY_PROTOCOL}" = "true" ]; then
    echo "Check that the communication fails due to using the PROXY protocol while LXD does not expect it"
    ! lxc remote add ha-cluster "https://${HOSTNAME}:443" --token "${token}" || false

    echo "Configure LXD to accept the PROXY protocol from HAproxy's address"
    HAPROXY_ADDR="$(ip route get "${LXD_ONE_HOST}" | sed -n '/src/ s/.* src \([^ ]\+\) .*/\1/p')"
    LXD_DIR="${LXD_ONE_DIR}" lxc config set core.https_trusted_proxy "${HAPROXY_ADDR}"
  fi

  echo "Add a remote going through the HAproxy"
  lxc remote add ha-cluster "https://${HOSTNAME}:443" --token "${token}"

  echo "Test connectivity through the HAproxy"
  lxc cluster list ha-cluster:

  echo "Test the HTTP listener for ACME support"
  # Wrong vhost
  [ "$(curl -s -o /dev/null -w "%{http_code}" "http://localhost/.well-known/acme-challenge/")" = "403" ]
  # Wrong path
  [ "$(curl -s -o /dev/null -w "%{http_code}" "http://${HOSTNAME}/.well-known/foo-bar")" = "403" ]
  # Valid path
  [ "$(curl -s -o /dev/null -w "%{http_code}" "http://${HOSTNAME}/.well-known/acme-challenge/")" = "301" ]
  [ "$(curl -s -o /dev/null -w "%{redirect_url}" "http://${HOSTNAME}/.well-known/acme-challenge/")" = "https://${HOSTNAME}/.well-known/acme-challenge/" ]

  echo "Verify direct connectivity to a member that will later be removed"
  nc -zv "${LXD_ONE_HOST}" "${LXD_ONE_PORT}"

  echo "Remove one of the cluster members"
  lxc cluster remove ha-cluster:node1 --yes
  sleep 0.5
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  rm -f "${LXD_ONE_DIR}/unix.socket"
  ! nc -zv "${LXD_ONE_HOST}" "${LXD_ONE_PORT}" || false

  # Allow time for dqlite to reshuffle roles.
  sleep 0.5

  echo "Verify that remaining members are able to serve requests"
  lxc cluster list ha-cluster:

  echo "Test rate limit is enforced and some connections are rejected"
  successes=0
  failures=0
  for i in $(seq "$((CONN_RATE + 5))"); do
    echo "Connection attempt (${i})"
    if lxc query ha-cluster:/ >/dev/null; then
      successes="$((successes+1))"
    else
      failures="$((failures+1))"
    fi
  done

  echo "Successes: ${successes}, Failures: ${failures}"
  [ "${successes}" -ge 1 ]
  [ "${failures}" -ge 10 ]

  echo "Cleanup"
  lxc remote remove ha-cluster

  stop_haproxy
  sed -i '/^127\.1\.2\.3/ d' /etc/hosts

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"

  # Restore the original state of the system
  if [ "${FOUND_RADOSGW}" = "true" ]; then
    microceph enable rgw
  fi
}

# When a voter cluster member is shutdown, its role gets transferred to a spare
# node.
test_clustering_handover() {
  spawn_lxd_and_bootstrap_cluster

  echo "Launched member 1"

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  echo "Launched member 2"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  echo "Launched member 3"

  # Spawn a fourth node, this will be a non-voter, stand-by node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  echo "Launched member 4"

  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -wFc "database-standby")" = "1" ]

  # Shutdown the first node.
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  echo "Stopped member 1"

  # The fourth node has been promoted, while the first one demoted.
  LXD_DIR="${LXD_THREE_DIR}" lxd sql local 'SELECT * FROM raft_nodes'
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster ls
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node4
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node1
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node4 | grep -xF -- "- database-voter"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node1 | grep -xF "database: false"

  # Even if we shutdown one more node, the cluster is still available.
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  echo "Stopped member 2"

  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list

  # Respawn the first node, which is now a spare, and the second node, which
  # is still a voter.
  echo "Respawning cluster members 1 and 2..."
  respawn_lxd_cluster_member "${ns1}" "${LXD_ONE_DIR}"
  respawn_lxd_cluster_member "${ns2}" "${LXD_TWO_DIR}"

  echo "Started members 1 and 2"

  # Shutdown two voters concurrently.
  echo "Shutting down cluster members 2 and 3..."
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown &
  pid1="$!"
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown &
  pid2="$!"

  wait "$pid1"
  wait "$pid2"
  echo "Cluster members 2 and 3 stopped..."

  echo "Stopped members 2 and 3"

  # Bringing back one of them restore the quorum.
  echo "Respawning cluster member 2..."
  respawn_lxd_cluster_member "${ns2}" "${LXD_TWO_DIR}"

  echo "Started member 2"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
}

# If a voter node crashes and is detected as offline, its role is migrated to a
# stand-by.
test_clustering_rebalance() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Spawn a fourth node
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check there is one database-standby member.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -wFc "database-standby")" = "1" ]

  # Kill the second node.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11
  # XXX: intentionally not using `kill_go_proc` helper as we want abrupt termination (sacrificing some coverage data).
  kill -9 "$(< "${LXD_TWO_DIR}/lxd.pid")"

  # Wait for the second node to be considered offline and be replaced by the
  # fourth node.
  sleep 11

  # The second node is offline and has been demoted.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "status: Offline"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "database: false"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF "status: Online"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF -- "- database-voter"

  # Respawn the second node. It won't be able to disrupt the current leader,
  # since dqlite uses pre-vote.
  respawn_lxd_cluster_member "${ns2}" "${LXD_TWO_DIR}"
  sleep 12

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "status: Online"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "database: true"

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
}

test_clustering_rebalance_remove_leader() {
  echo "Create two node cluster"
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  echo "Verify clustering enabled on both nodes"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -F node1
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -F node2

  # Wait for cluster to stabilize and role rebalancing to complete
  echo "Waiting for both nodes to have database role..."
  for _ in $(seq 10); do
    if LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -F "database: true" && \
       LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "database: true"; then
      break
    fi
    sleep 0.5
  done

  echo "Verify we have two database nodes"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -F "database: true"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "database: true"

  echo "Remove the leader node from the cluster"
  # When a leader removes itself, clusterPutDisable() is called, which in turn calls ReplaceDaemon().
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node1 --force --yes

  echo "Wait for node1 daemon to restart after removal"
  # The daemon restarts itself after cluster removal via ReplaceDaemon()
  LXD_DIR="${LXD_ONE_DIR}" lxd waitready --timeout=30

  echo "Verify node1 is still functional with clustering disabled"
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_clustered: false"

  echo "Verify node2 is still clustered and is now the only member"
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list # For debugging
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc cluster list -f csv | wc -l)" = "1" ]
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node2 | grep -F "database: true"

  echo "Clean up"
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

# Recover a cluster where a raft node was removed from the nodes table but not
# from the raft configuration.
test_clustering_remove_raft_node() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F 'cluster.offline_threshold: "11"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  nsenter -m -n -t "$(< "${TEST_DIR}/ns/${ns1}/PID")" -- ip link show "${bridge}" > /dev/null
  nsenter -m -n -t "$(< "${TEST_DIR}/ns/${ns2}/PID")" -- ip link show "${bridge}" > /dev/null

  # Create a pending network and pool, to show that they are not
  # considered when checking if the joining node has all the required
  # networks and pools.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create net1 --target node2

  # Spawn a third node, using the non-leader node2 as join target.
  spawn_lxd_and_join_cluster "${cert}" 3 2 "${LXD_ONE_DIR}"

  # Spawn a fourth node, this will be a database-standby node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list

  # Kill the second node, to prevent it from transferring its database role at shutdown.
  # XXX: intentionally not using `kill_go_proc` helper as we want abrupt termination (sacrificing some coverage data).
  kill -9 "$(< "${LXD_TWO_DIR}/lxd.pid")"

  # Remove the second node from the database but not from the raft configuration.
  retries=10
  while [ "${retries}" != "0" ]; do
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "DELETE FROM nodes WHERE address = '100.64.1.102:8443'" && break
    sleep 0.5
    retries=$((retries-1))
  done

  if [ "${retries}" -eq 0 ]; then
      echo "Failed to remove node from database"
      return 1
  fi

  # Let the heartbeats catch up.
  sleep 11

  # The node does not appear anymore in the cluster list.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node2" || false

  # There are only 2 database nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- database-leader"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node3 | grep -xF -- "- database-voter"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF -- "- database-voter"

  # The second node is still in the raft_nodes table.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes WHERE address = '100.64.1.102:8443'")" = 1 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes")" = 4 ]

  # Force removing the raft node.
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster remove-raft-node -q "100.64.1.102"

  # Wait for a heartbeat to propagate and a rebalance to be performed.
  sleep 11

  # We're back to 3 database nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- database-leader"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node3 | grep -xF -- "- database-voter"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF -- "- database-voter"

  # The second node is gone from the raft_nodes_table.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes WHERE address = '100.64.1.102:8443'")" = 0 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes")" = 3 ]
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
}

test_clustering_failure_domains() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node, using the non-leader node2 as join target.
  spawn_lxd_and_join_cluster "${cert}" 3 2 "${LXD_ONE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  spawn_lxd_and_join_cluster "${cert}" 5 4 "${LXD_ONE_DIR}"

  # Spawn a sixth node, using non-database node4 as join target.
  spawn_lxd_and_join_cluster "${cert}" 6 4 "${LXD_ONE_DIR}"

  # Default failure domain
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "failure_domain: default"

  # Test the new failure-domain subcommand by setting failure domains
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain set node1 az1
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain set node2 az2
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain set node3 az3
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain set node4 az1
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain set node5 az2
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain set node6 az3

  # Verify failure domain was set
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "failure_domain: az2"

  # Test the get subcommand
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc cluster failure-domain get node2)" = "az2" ]

  # Shutdown a node in az2, its replacement is picked from az2.
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 3

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "database: false"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node5 | grep -xF "database: true"

  LXD_DIR="${LXD_SIX_DIR}" lxd shutdown
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_SIX_DIR}/unix.socket"
  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_FIVE_DIR}"
  kill_lxd "${LXD_SIX_DIR}"
}

test_clustering_image_refresh() {
  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  local poolDriver
  poolDriver="$(storage_backend "$LXD_INITIAL_DIR")"

  # Spawn first node
  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.images_minimal_replica=1 images.auto_update_interval=1

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # Spawn public node which has a public testimage
  setup_clustering_netns 4
  LXD_REMOTE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"

  LXD_NETNS="${ns4}" spawn_lxd "${LXD_REMOTE_DIR}" false
  dir_configure "${LXD_REMOTE_DIR}"
  LXD_DIR="${LXD_REMOTE_DIR}" deps/import-busybox --alias testimage --public

  LXD_DIR="${LXD_REMOTE_DIR}" lxc config set core.https_address "100.64.1.104:8443"

  # Add remotes
  lxc remote add public "https://100.64.1.104:8443" --accept-certificate --public
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  lxc remote add cluster "https://100.64.1.101:8443" --token "${token}"

  LXD_DIR="${LXD_REMOTE_DIR}" lxc init testimage c1

  # Create additional projects
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo
  LXD_DIR="${LXD_ONE_DIR}" lxc project create bar

  # Copy default profile to all projects (this includes the root disk)
  LXD_DIR="${LXD_ONE_DIR}" lxc profile show default | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default --project foo
  LXD_DIR="${LXD_ONE_DIR}" lxc profile show default | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default --project bar

  for project in default foo bar; do
    # Copy the public image to each project
    LXD_DIR="${LXD_ONE_DIR}" lxc image copy public:testimage local: --alias testimage --target-project "${project}"

    # Disable autoupdate for testimage in project foo
    if [ "${project}" = "foo" ]; then
      auto_update=false
    else
      auto_update=true
    fi

    LXD_DIR="${LXD_ONE_DIR}" lxc image show testimage --project "${project}" | sed -r "s/auto_update: .*/auto_update: ${auto_update}/g" | LXD_DIR="${LXD_ONE_DIR}" lxc image edit testimage --project "${project}"

    # Create a container in each project
    LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 --project "${project}"
  done

  old_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  # Check the image file was distributed initially to all members (because it was needed when creating an instance on each member).
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    stat --terse "${lxd_dir}/images/${old_fingerprint}"
  done

  # Modify public testimage
  echo "${RANDOM}" | LXD_DIR="${LXD_REMOTE_DIR}" lxc file push - c1/foo
  LXD_DIR="${LXD_REMOTE_DIR}" lxc publish c1 --alias testimage --reuse --public
  new_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  pids=""

  if [ "${poolDriver}" != "dir" ]; then
    # Check image storage volume records exist.
    if [ "${poolDriver}" = "ceph" ]; then
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 1 ]
    else
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 3 ]
    fi
  fi

  # Trigger image refresh on all nodes
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    LXD_DIR="${lxd_dir}" lxc query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    # Don't fail if PID isn't available as the process could be done already.
    wait "${pid}" || true
  done

  # Check the image files were updated correctly.
  # Node 1 should have both old and new images because:
  # - It originally had the old image in a project with auto update disabled
  # - It also has an instance in a project with auto update enabled.
  # Node 2 should have only the old image because it only has an instance in a project with auto update disabled.
  # Node 3 should have only the new image because it only has an instance in a project with auto update enabled.
  stat --terse "${LXD_ONE_DIR}/images/${old_fingerprint}"
  stat --terse "${LXD_ONE_DIR}/images/${new_fingerprint}"
  stat --terse "${LXD_TWO_DIR}/images/${old_fingerprint}"
  stat --terse "${LXD_THREE_DIR}/images/${new_fingerprint}"

  if [ "${poolDriver}" != "dir" ]; then
    # Check image storage volume records actually removed from relevant members and replaced with new fingerprint.
    if [ "${poolDriver}" = "ceph" ]; then
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 0 ]
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${new_fingerprint}'")" = 1 ]
    else
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 1 ]
      [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${new_fingerprint}'")" = 2 ]
    fi
  fi

  # The projects default and bar should have received the new image
  # while project foo should still have the old image.
  # Also, it should only show 1 entry for the old image and 2 entries
  # for the new one.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="foo"')" = "${old_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM images WHERE fingerprint = '${old_fingerprint}'")" = 1 ]

  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="default"')" = "${new_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="bar"')" = "${new_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM images WHERE fingerprint = '${new_fingerprint}'")" = 2 ]

  pids=""

  # Trigger image refresh on all nodes. This shouldn't do anything as the image
  # is already up-to-date.
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    LXD_DIR="${lxd_dir}" lxc query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    # Don't fail if PID isn't available as the process could be done already.
    wait "${pid}" || true
  done

  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="foo"')" = "${old_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM images WHERE fingerprint = '${old_fingerprint}'")" = 1 ]

  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="default"')" = "${new_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="bar"')" = "${new_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM images WHERE fingerprint = '${new_fingerprint}'")" = 2 ]

  # Modify public testimage
  echo "${RANDOM}" | LXD_DIR="${LXD_REMOTE_DIR}" lxc file push - c1/foo
  LXD_DIR="${LXD_REMOTE_DIR}" lxc publish c1 --alias testimage --reuse --public
  new_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  pids=""

  # Trigger image refresh on all nodes
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    LXD_DIR="${lxd_dir}" lxc query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    # Don't fail if PID isn't available as the process could be done already.
    wait "${pid}" || true
  done

  pids=""

  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="foo"')" = "${old_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM images WHERE fingerprint = '${old_fingerprint}'")" = 1 ]

  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="default"')" = "${new_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT images.fingerprint FROM images JOIN projects ON images.project_id=projects.id WHERE projects.name="bar"')" = "${new_fingerprint}" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT COUNT(*) FROM images WHERE fingerprint = '${new_fingerprint}'")" = 2 ]

  # Clean up everything
  for project in default foo bar; do
    # shellcheck disable=SC2046
    LXD_DIR="${LXD_ONE_DIR}" lxc delete --project "${project}" $(LXD_DIR="${LXD_ONE_DIR}" lxc list --format csv --columns n --project "${project}")
    # shellcheck disable=SC2046
    LXD_DIR="${LXD_ONE_DIR}" lxc image delete --project "${project}" $(LXD_DIR="${LXD_ONE_DIR}" lxc image list --format csv --project "${project}" | cut -d, -f2)
  done

  # shellcheck disable=SC2046
  LXD_DIR="${LXD_REMOTE_DIR}" lxc delete $(LXD_DIR="${LXD_REMOTE_DIR}" lxc list --format csv --columns n)
  # shellcheck disable=SC2046
  LXD_DIR="${LXD_REMOTE_DIR}" lxc image delete $(LXD_DIR="${LXD_REMOTE_DIR}" lxc image list --format csv | cut -d, -f2)

  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete bar

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_REMOTE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_REMOTE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_REMOTE_DIR}"

  lxc remote rm cluster

  # shellcheck disable=SC2034
  LXD_NETNS=
}

test_clustering_evacuation() {
  wait_for_evacuation_op() {
    local lxd_dir="$1"
    local delay max_attempts i
    delay=0.2
    max_attempts=60

    for i in $(seq "${max_attempts}"); do
      if ! LXD_DIR="${lxd_dir}" lxc operation list --format csv | grep -F "Evacuating cluster member" >/dev/null; then
        return 0
      fi

      sleep "${delay}"
    done

    echo "Evacuation operation still present after ${i} attempts (~${delay}s interval)"
    return 1
  }
  echo "Create cluster with 3 nodes"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  local poolDriver
  poolDriver="$(storage_backend "$LXD_INITIAL_DIR")"

  # Spawn first node
  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  echo "Check the state of the preseeded storage pool shows up as CREATED"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}" "${poolDriver}"

  echo "Create local pool"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir

  echo "Create local storage volume"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 vol1

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1 --target=node1 -c boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c2 --target=node1 -c boot.host_shutdown_timeout=1 -c cluster.evacuate=auto -s pool1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c3 --target=node1 -c boot.host_shutdown_timeout=1 -c cluster.evacuate=stop

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c4 --target=node1 -c boot.host_shutdown_timeout=1 -c cluster.evacuate=migrate -s pool1

  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c5 -c boot.host_shutdown_timeout=1 --target=node1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c6 --target=node2 -c boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create foo
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group assign node3 default,foo

  echo 'Create c7 on node1 with "volatile.cluster.group" set to "foo" to test evacuation respects the group constraint.'
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c7 --target=node1 -c volatile.cluster.group=foo
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc config get c7 volatile.cluster.group)" = "foo" ]
  # "volatile.cluster.group" is only checked during scheduling events (creation, migration, evacuation).
  # Expected: c7 created on node1.
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c7)" = "STOPPED,node1" ]

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list -c nsL

  echo "Evacuate first node"
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster evacuate node1 --force

  echo "Ensure the node is evacuated"
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -F "status: Evacuated"

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list -c nsL

  echo 'Check c7 respects "volatile.cluster.group" and evacuates to the "foo" group (node3).'
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c7)" = "STOPPED,node3" ]

  echo "Check status and location of all instances post-evacuation."
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c s  c1)" = "RUNNING" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L  c1)" != "node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c s  c2)" = "RUNNING" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L  c2)" != "node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c3)" = "STOPPED,node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c s  c4)" = "RUNNING" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L  c4)" != "node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c s  c5)" = "STOPPED" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L  c5)" != "node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c6)" = "RUNNING,node2" ]

  c1_location="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c1)"
  c2_location="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c2)"
  c4_location="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c4)"
  c5_location="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c5)"
  c6_location="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c6)"
  c7_location="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c7)"

  echo "Verify that instance migration to an evacuated node is not allowed."
  [[ "$(LXD_DIR="${LXD_TWO_DIR}" lxc move c5 --target=node1 2>&1)" == *"Error: Migration operation failure: The destination cluster member is evacuated"* ]]

  echo 'Restore first node with "skip" mode.'
  # "skip" mode restores cluster member status without starting instances or migrating back evacuated instances.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster restore node1 --action=skip --force

  echo 'Ensure the node is restored'
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -xF "status: Online"

  echo 'Verify that instances remain in their evacuated state/location'
  # c1 should stay on the node it was migrated to
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c1)" = "RUNNING,${c1_location}" ]
  # c2 should stay on the node it was migrated to
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c2)" = "RUNNING,${c2_location}" ]
  # c3 should remain stopped on node1
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c3)" = "STOPPED,node1" ]
  # c4 should stay on the node it was migrated to
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c4)" = "RUNNING,${c4_location}" ]
  # c5 should remain stopped on the node it was migrated to
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c5)" = "STOPPED,${c5_location}" ]
  # c6 should stay on the node it was already on
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c6)" = "RUNNING,${c6_location}" ]
  # c7 should stay on the node it was already on
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c7)" = "STOPPED,${c7_location}" ]

  wait_for_evacuation_op "${LXD_TWO_DIR}"

  # Now test a full restore for comparison
  echo 'Evacuate node1 again'
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster evacuate node1 --force

  echo 'Ensure instances cannot be created on the evacuated node'
  ! LXD_DIR="${LXD_TWO_DIR}" lxc init --empty c8 --target=node1 || false

  echo 'Ensure the node is evacuated'
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -xF "status: Evacuated"

  echo 'Restore first node (without "skip" mode)'
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster restore node1 --force

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list -c nsL

  echo 'Ensure the instances were moved back to the origin'
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c1)" = "RUNNING,node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c2)" = "RUNNING,node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c3)" = "RUNNING,node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c4)" = "RUNNING,node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c5)" = "STOPPED,node1" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c6)" = "RUNNING,node2" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c sL c7)" = "STOPPED,node1" ]

  echo 'Move c7 to @default to check "volatile.cluster.group" is updated.'
  LXD_DIR="${LXD_TWO_DIR}" lxc move c7 --target=@default
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get c7 volatile.cluster.group)" = "default" ]

  echo 'Move c7 to verify "volatile.cluster.group" is cleared when moving to an explicit node outside the group.'
  LXD_DIR="${LXD_TWO_DIR}" lxc move c7 --target=node1

  echo 'Verify c7 is on the target node and "volatile.cluster.group" is cleared'
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c L c7)" = "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get c7 volatile.cluster.group || echo fail)" = "" ]

  echo 'Clean up'
  LXD_DIR="${LXD_TWO_DIR}" lxc delete -f c1 c2 c3 c4 c5 c6 c7

  echo "==> Test cluster evacuation with placement groups"

  echo "Create placement groups for evacuation tests"
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-evac-compact-permissive policy=compact rigor=permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-evac-compact-strict policy=compact rigor=strict
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-evac-spread-permissive policy=spread rigor=permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-evac-spread-strict policy=spread rigor=strict

  echo "==> Test: --target with placement.group is allowed"
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage target-test1 -c placement.group=pg-evac-compact-permissive -c cluster.evacuate=migrate --target node1
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L target-test1)" = "node1" ]

  echo "Verify migration with --target works when placement.group is set"
  LXD_DIR="${LXD_ONE_DIR}" lxc move target-test1 --target node2
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L target-test1)" = "node2" ]

  LXD_DIR="${LXD_ONE_DIR}" lxc delete target-test1 --force

  echo "==> Test evacuation: compact/permissive"
  # Expected: Instances preferentially on same node during evacuation, but not strictly enforced.
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-c1 -c placement.group=pg-evac-compact-permissive -c cluster.evacuate=migrate --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-c2 -c placement.group=pg-evac-compact-permissive -c cluster.evacuate=migrate --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-c3 -c placement.group=pg-evac-compact-permissive -c cluster.evacuate=migrate --target node1

  echo "Evacuating..."
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force

  echo "Verify all instances moved off node1"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c3)" != "node1" ]

  echo "Verify instances preferably on the same node"
  LXD_DIR="${LXD_ONE_DIR}" lxc list
  evac_node1=$(LXD_DIR="${LXD_ONE_DIR}" lxc list evac-c1 -f csv -c L)
  evac_node2=$(LXD_DIR="${LXD_ONE_DIR}" lxc list evac-c2 -f csv -c L)
  evac_node3=$(LXD_DIR="${LXD_ONE_DIR}" lxc list evac-c3 -f csv -c L)
  echo "evac-c1: ${evac_node1}, evac-c2: ${evac_node2}, evac-c3: ${evac_node3}"
  evac_nodes=$(printf "%s\n%s\n%s\n" "${evac_node1}" "${evac_node2}" "${evac_node3}" | sort -u | wc -l)
  echo "Instances on ${evac_nodes} different nodes"
  [ "${evac_nodes}" -le "3" ]

  echo "Restore node1 and move instances back"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force

  echo "Verify instances are back on node1"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" = "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" = "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c3)" = "node1" ]

  echo "==> Test evacuation: compact/strict"
  # Expected: All 3 instances end up on same cluster member.
  echo "Update placement group to compact/strict for existing instances"
  LXD_DIR="${LXD_ONE_DIR}" lxc config set evac-c1 placement.group pg-evac-compact-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc config set evac-c2 placement.group pg-evac-compact-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc config set evac-c3 placement.group pg-evac-compact-strict

  wait_for_evacuation_op "${LXD_ONE_DIR}"

  echo "Evacuating..."
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force

  echo "Verify all instances moved off node1"
  LXD_DIR="${LXD_ONE_DIR}" lxc list
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c3)" != "node1" ]

  echo "Verify all instances are on the same node"
  LXD_DIR="${LXD_ONE_DIR}" lxc list
  evac_node1=$(LXD_DIR="${LXD_ONE_DIR}" lxc list evac-c1 -f csv -c L)
  evac_node2=$(LXD_DIR="${LXD_ONE_DIR}" lxc list evac-c2 -f csv -c L)
  evac_node3=$(LXD_DIR="${LXD_ONE_DIR}" lxc list evac-c3 -f csv -c L)
  [ "${evac_node1}" = "${evac_node2}" ] && [ "${evac_node2}" = "${evac_node3}" ]

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force

  echo "Verify instances are back on node1"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" = "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" = "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c3)" = "node1" ]

  echo "==> Test evacuation: spread/permissive"
  # Expected: Instances distributed across at least 2 nodes (spread preference), but fallback acceptable.
  echo "Update placement group to spread/permissive for existing instances"
  LXD_DIR="${LXD_ONE_DIR}" lxc config set evac-c1 placement.group pg-evac-spread-permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc config set evac-c2 placement.group pg-evac-spread-permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc config set evac-c3 placement.group pg-evac-spread-permissive

  wait_for_evacuation_op "${LXD_ONE_DIR}"

  echo "Evacuating..."
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force

  echo "Verify all instances have moved off node1"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c3)" != "node1" ]

  echo "Verify instances are on at least 2 different nodes"
  LXD_DIR="${LXD_ONE_DIR}" lxc list
  evac_c1_node=$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)
  evac_c2_node=$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)
  evac_c3_node=$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c3)
  echo "evac-c1: ${evac_c1_node}, evac-c2: ${evac_c2_node}, evac-c3: ${evac_c3_node}"
  evac_nodes=$(printf "%s\n%s\n%s\n" "${evac_c1_node}" "${evac_c2_node}" "${evac_c3_node}" | sort -u | wc -l)
  echo "Instances on ${evac_nodes} different nodes"
  [ "${evac_nodes}" -ge "2" ]

  # For spread/strict, we need all instances on same node first, but that contradicts spread/strict's requirement
  # of one instance per node. So we delete and recreate for this test to get clean state.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc delete evac-c1 evac-c2 evac-c3 --force

  echo "==> Test evacuation: spread/strict"
  # Expected: Both instances on different cluster members (strict enforcement).
  echo "Create 2 fresh instances with spread/strict placement group (only 2 nodes available after evacuation)"
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-c1 -c placement.group=pg-evac-spread-strict -c cluster.evacuate=migrate --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-c2 -c placement.group=pg-evac-spread-strict -c cluster.evacuate=migrate --target node1

  echo "Verify instances are on node1"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" = "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" = "node1" ]

  wait_for_evacuation_op "${LXD_ONE_DIR}"

  echo "Evacuating..."
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force

  echo "Verify all instances have moved off node1"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)" != "node1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)" != "node1" ]

  echo "Verify instances are on different nodes"
  LXD_DIR="${LXD_ONE_DIR}" lxc list
  evac_c1_node=$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c1)
  evac_c2_node=$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-c2)
  echo "evac-c1: ${evac_c1_node}, evac-c2: ${evac_c2_node}"
  [ "${evac_c1_node}" != "${evac_c2_node}" ]

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc delete evac-c1 evac-c2 --force

  echo "==> Test: spread/strict with insufficient nodes for strict enforcement"
  # With 3 nodes and 3 instances, we can only place 2 instances on different nodes (only 2 available after evacuation).
  # The 3rd instance will be skipped and remain on the evacuated node.
  echo "Create 3 instances for spread/strict (more than available nodes)"
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-4a -c placement.group=pg-evac-spread-strict -c cluster.evacuate=migrate --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-4b -c placement.group=pg-evac-spread-strict -c cluster.evacuate=migrate --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-4c -c placement.group=pg-evac-spread-strict -c cluster.evacuate=migrate --target node1

  wait_for_evacuation_op "${LXD_ONE_DIR}"

  echo "Evacuating..."
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc list # For debugging

  echo "Verify instances evacuated (with fallback behavior during evacuation)"
  node1_count=0
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-4a)" != "node1" ] && node1_count=$((node1_count+1))
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-4b)" != "node1" ] && node1_count=$((node1_count+1))
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L evac-4c)" != "node1" ] && node1_count=$((node1_count+1))
  echo "Instances successfully evacuated from node1: ${node1_count}/3"
  # We only expect 2 instances to evacuate (spread/strict has 2 nodes available excluding evacuated node)
  [ "${node1_count}" = "2" ]

  echo "Verify creating a 4th instance with spread/strict fails due to insufficient nodes"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init testimage evac-4d -c placement.group=pg-evac-spread-strict -c cluster.evacuate=migrate || false

  echo "Cleaning up..."
  LXD_DIR="${LXD_ONE_DIR}" lxc delete evac-4a evac-4b evac-4c --force
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete testimage

  # Clean up placement groups
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-evac-compact-permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-evac-compact-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-evac-spread-permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-evac-spread-strict

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  # Shut down cluster
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"

  # shellcheck disable=SC2034
  LXD_NETNS=
}

test_clustering_evacuation_restore_operations() {
  echo "Create cluster with 2 nodes"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  local poolDriver
  poolDriver="$(storage_backend "$LXD_INITIAL_DIR")"

  # Spawn first node
  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  echo "Launch 3 containers on node1"
  for c in c{1..3}; do LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage "${c}" --target node1; done

  echo "Start node1 evacuation in background"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --quiet --force &
  evac_pid=$!
  sleep 1 # Wait a bit for the operation to start

  echo "Check restore fails while evacuation operation in progress"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force 2>&1)" = 'Error: Failed updating cluster member state: Cannot restore "node1" while an evacuate operation is in progress' ]

  echo "Wait for all containers to be evacuated"
  wait "${evac_pid}"

  echo "Verify all containers are no longer on node1 and have been evacuated to node2"
  for c in c{1..3}; do
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L "${c}")" = "node2" ]
  done

  echo "Start node1 restore in background"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --quiet --force &
  restore_pid=$!
  sleep 1 # Wait a bit for the operation to start

  echo "Check evacuation fails while restore operation in progress"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force 2>&1)" = 'Error: Failed updating cluster member state: Cannot evacuate "node1" while a restore operation is in progress' ]

  echo "Wait for all containers to be restored to node1"
  wait "${restore_pid}"

  echo "Verify all containers are no longer on node2 and have been restored to node1"
  for c in c{1..3}; do
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L "${c}")" = "node1" ]
  done

  echo "Clean up"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c{1..3} --force
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${bridge}"

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_edit_configuration() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn 6 nodes in total for role coverage.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  spawn_lxd_and_join_cluster "${cert}" 5 1 "${LXD_ONE_DIR}"

  spawn_lxd_and_join_cluster "${cert}" 6 1 "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11

  # Ensure successful communication
  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_SIX_DIR}" lxc info --target node1 | grep -F "server_name: node1"

  # Shut down all nodes, de-syncing the roles tables
  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"
  shutdown_lxd "${LXD_THREE_DIR}"
  shutdown_lxd "${LXD_FOUR_DIR}"

  # Force-kill the last two to prevent leadership loss.
  # XXX: intentionally not using `kill_go_proc` helper as we want abrupt termination (sacrificing some coverage data).
  daemon_pid=$(< "${LXD_FIVE_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(< "${LXD_SIX_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true

  # Update the cluster configuration with new port numbers
  # lxd cluster edit generates ${LXD_DIR}/database/lxd_recovery_db.tar.gz
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster show | sed -e "s/:8443/:9393/" | LXD_DIR="${LXD_ONE_DIR}" lxd cluster edit

  for other_dir in "${LXD_TWO_DIR}" "${LXD_THREE_DIR}" "${LXD_FOUR_DIR}" "${LXD_FIVE_DIR}" "${LXD_SIX_DIR}"; do
    cp "${LXD_ONE_DIR}/database/lxd_recovery_db.tar.gz" "${other_dir}/database/"
  done

  # While it does work to load the recovery DB on the node which generated it,
  # we should test to make sure that the recovery operation left the database
  # ready to go.
  rm "${LXD_ONE_DIR}/database/lxd_recovery_db.tar.gz"

  # Respawn the nodes
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" false
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false
  LXD_NETNS="${ns3}" respawn_lxd "${LXD_THREE_DIR}" false
  LXD_NETNS="${ns4}" respawn_lxd "${LXD_FOUR_DIR}" false
  # shellcheck disable=SC2154
  LXD_NETNS="${ns5}" respawn_lxd "${LXD_FIVE_DIR}" false
  # Only wait on the last node, because we don't know who the voters are
  # shellcheck disable=SC2154
  LXD_NETNS="${ns6}" respawn_lxd "${LXD_SIX_DIR}" true

  # Let the heartbeats catch up
  sleep 11

  # Sanity check of the automated backup
  # We can't check that the backup has the same files as even LXD_ONE_DIR, because
  # the recovery process adds a segment to the global db dir, and may otherwise
  # alter dqlite files. This makes sure that the backup at least looks like `database/`.
  for dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}" "${LXD_FOUR_DIR}" "${LXD_FIVE_DIR}" "${LXD_SIX_DIR}"; do
    backupFilename=$(find "${dir}" -name "db_backup.*.tar.gz")
    files=$(tar --list -f "${backupFilename}")
    # Check for dqlite segment files
    echo "${files}" | grep -xE -e "database/global/open-[0-9]" -e "database/global/[0-9]{16}-[0-9]{16}"
    echo "${files}" | grep -xF "database/local.db"

    # Recovery tarballs shouldn't be included in backups
    ! echo "${files}" | grep -F lxd_recovery_db.tar.gz || false
  done

  # Ensure successful communication
  LXD_DIR="${LXD_ONE_DIR}"   lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}"   lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_FOUR_DIR}"  lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_FIVE_DIR}"  lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_SIX_DIR}"   lxc info --target node1 | grep -F "server_name: node1"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -F "No heartbeat" || false

  # Clean up
  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"
  shutdown_lxd "${LXD_THREE_DIR}"
  shutdown_lxd "${LXD_FOUR_DIR}"

  # Force-kill the last two to prevent leadership loss.
  # XXX: intentionally not using `kill_go_proc` helper as we want abrupt termination (sacrificing some coverage data).
  daemon_pid=$(< "${LXD_FIVE_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(< "${LXD_SIX_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_SIX_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_FIVE_DIR}"
  kill_lxd "${LXD_SIX_DIR}"
}

test_clustering_remove_members() {
  # Bootstrap the first node
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Spawn a fourth node
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Spawn a fifth node
  spawn_lxd_and_join_cluster "${cert}" 5 1 "${LXD_ONE_DIR}"

  # Spawn a sixth node
  spawn_lxd_and_join_cluster "${cert}" 6 1 "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info --target node1 | grep -F "server_name: node1"
  LXD_DIR="${LXD_SIX_DIR}" lxc info --target node1 | grep -F "server_name: node1"

  # stop node 6
  shutdown_lxd "${LXD_SIX_DIR}"

  # Remove node2 node3 node4 node5
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster rm node2
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster rm node3
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster rm node4
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster rm node5

  # Ensure the remaining node is working and node2, node3, node4,node5 successful reomve from cluster
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node2" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node3" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node4" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node5" || false
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node1"

  # Start node 6
  LXD_NETNS="${ns6}" respawn_lxd "${LXD_SIX_DIR}" true

  # make sure node6 is a spare node (no database roles)
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node6"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node6 | grep -E "\- database-(voter|standby|leader)$" || false

  # wait for leader update table raft_node of local database by heartbeat
  sleep 10s

  # Remove the leader, via the spare node
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster rm node1

  # Ensure the remaining node is working and node1 had successful remove
  ! LXD_DIR="${LXD_SIX_DIR}" lxc cluster list | grep -wF "node1" || false
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster list | grep -wF "node6"

  # Check whether node6 is changed from a spare node to a leader node.
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster show node6 | grep -xF -- "- database-leader"

  # Spawn a seventh node
  spawn_lxd_and_join_cluster "${cert}" 7 6 "${LXD_SIX_DIR}"

  # Ensure the remaining node is working by join a new node7
  LXD_DIR="${LXD_SIX_DIR}" lxc info --target node7 | grep -F "server_name: node7"
  LXD_DIR="${LXD_SEVEN_DIR}" lxc info --target node6 | grep -F "server_name: node6"

  # Clean up
  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"
  shutdown_lxd "${LXD_THREE_DIR}"
  shutdown_lxd "${LXD_FOUR_DIR}"
  shutdown_lxd "${LXD_FIVE_DIR}"
  shutdown_lxd "${LXD_SIX_DIR}"
  shutdown_lxd "${LXD_SEVEN_DIR}"

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_SIX_DIR}/unix.socket"
  rm -f "${LXD_SEVEN_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_FIVE_DIR}"
  kill_lxd "${LXD_SIX_DIR}"
  kill_lxd "${LXD_SEVEN_DIR}"
}

test_clustering_autotarget() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Use node1 for all cluster actions.
  LXD_DIR="${LXD_ONE_DIR}"

  # Spawn c1 on node2 from node1
  lxc init --empty --target node2 c1
  [ "$(lxc list -f csv -c nL c1)" = "c1,node2" ]

  # Set node1 config to disable autotarget
  lxc cluster set node1 scheduler.instance manual

  # Spawn another node, autotargeting node2 although it has more instances.
  lxc init --empty c2
  [ "$(lxc list -f csv -c nL c2)" = "c2,node2" ]

  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_groups() {
  echo 'Create cluster with 3 nodes'
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  LXD_DIR="${LXD_ONE_DIR}" lxc remote add cluster --token "${token}" "https://100.64.1.101:8443"

  # Initially, there is only the default group.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group show cluster:default
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups | jq --exit-status 'length == 1'

  # All nodes initially belong to the default group.
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/default | jq --exit-status '.members | length == 3'

  # Renaming the default group is not allowed.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster group rename cluster:default foobar || false

  lxc cluster list cluster:
  # Nodes need to belong to at least one group, removing it from the default group should therefore fail.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node1 default || false

  sub_test "Group creation and duplication checks"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:foo
  [ "$(! LXD_DIR="${LXD_ONE_DIR}" "${_LXC}" cluster group create cluster:foo 2>&1 1>/dev/null)" = 'Error: Cluster group "foo" already exists' ]
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:bar
  [ "$(! LXD_DIR="${LXD_ONE_DIR}" "${_LXC}" cluster group rename cluster:bar foo 2>&1 1>/dev/null)" = 'Error: Name "foo" already in use' ]
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:foo
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:bar

  sub_test "Group membership and rename rules"
  # Create new cluster group which should be empty.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:foobar
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/foobar | jq --exit-status '.members == []'

  # Copy both description and members from default group.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group show cluster:default | LXD_DIR="${LXD_ONE_DIR}" lxc cluster group edit cluster:foobar
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/foobar | jq --exit-status '.description == "Default cluster group"'
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/foobar | jq --exit-status '.members | length == 3'

  # Delete all members from new group.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node1 foobar
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node2 foobar
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node3 foobar

  # Add second node to new group. Node2 will now belong to both groups.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group assign cluster:node2 default,foobar
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node2 | jq --exit-status '.groups | any(. == "default")'
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node2 | jq --exit-status '.groups | any(. == "foobar")'

  # Deleting the "foobar" group should fail as it still has members.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:foobar || false

  # Since node2 now belongs to two groups, it can be removed from the default group.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node2 default
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node2

  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node2 | jq --exit-status '.groups | all(. != "default")'
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node2 | jq --exit-status '.groups | any(. == "foobar")'

  # Remove node2 from "foobar" group should fail as node2 is not in any other group.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node2 foobar || false

  # Rename group "foobar" to "blah".
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group rename cluster:foobar blah
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node2 | jq --exit-status '.groups | any(. == "blah")'

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:foobar2
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group assign cluster:node3 default,foobar2

  sub_test "Group CRUD"
  # Create a new group "newgroup".
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:newgroup
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/newgroup | jq --exit-status '.members == []'

  # Add node1 to the "newgroup" group.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group add cluster:node1 newgroup
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/members/node1 | jq --exit-status '.groups | any(. == "newgroup")'

  # Remove node1 from "newgroup".
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node1 newgroup

  # Delete cluster group "newgroup".
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:newgroup

  # Create a cluster group using yaml.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:yamlgroup <<EOF
description: foo
EOF

  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/yamlgroup | jq --exit-status '.description == "foo"'
  # Delete the cluster group "yamlgroup".
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:yamlgroup

  # Initialize a cluster group with multiple nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups -X POST -d '{"name":"multi-node-group","description":"","members":["node1","node2","node3"]}'

  # Ensure cluster group created with requested members.
  LXD_DIR="${LXD_ONE_DIR}" lxc query cluster:/1.0/cluster/groups/multi-node-group | jq --exit-status '.members | length == 3'

  # Remove nodes and delete cluster group.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node1 multi-node-group
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node2 multi-node-group
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group remove cluster:node3 multi-node-group

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:multi-node-group

  sub_test "Scheduling with groups and targeting"
  # With these settings:
  # - node1 will receive instances unless a different node is directly targeted (not via group)
  # - node2 will receive instances if either targeted by group or directly
  # - node3 will only receive instances if targeted directly
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster set cluster:node2 scheduler.instance=group
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster set cluster:node3 scheduler.instance=manual

  # Cluster group "foobar" does not exist and should therefore fail.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --target=@foobar || false

  # At this stage we have:
  # - node1 in group default accepting all instances
  # - node2 in group blah accepting group-only targeting
  # - node3 in group default accepting direct targeting only

  # c1 should go to node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c1)" = "node1" ]

  # c2 should go to node2. Additionally it should be possible to specify the network.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c2 --target=@blah --network "${bridge}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c2)" = "node2" ]

  # c3 should go to node2 again. Additionally it should be possible to specify the storage pool.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c3 --target=@blah --storage data
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c3)" = "node2" ]

  # Direct targeting of node2 should work.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c4 --target=node2
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c4)" = "node2" ]

  # Direct targeting of node3 should work.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c5 --target=node3
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c5)" = "node3" ]

  sub_test "volatile.cluster.group and placement.group behavior"
  # Check "volatile.cluster.group" is set correctly.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster:c1 volatile.cluster.group || echo fail)" = "" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster:c2 volatile.cluster.group)" = "blah" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster:c3 volatile.cluster.group)" = "blah" ]

  # Setting a "placement.group" on an instance should clear "volatile.cluster.group".
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-test policy=spread rigor=permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster:c2 placement.group=pg-test
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster:c2 volatile.cluster.group || echo fail)" = "" ]

  # Creating with "placement.group" should not set "volatile.cluster.group".
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c6 --target=@blah -c placement.group=pg-test
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster:c6 volatile.cluster.group || echo fail)" = "" ]

  # Verify that instances with "volatile.cluster.group" are reported in used_by for the blah group.
  LXD_DIR="${LXD_ONE_DIR}" lxc_remote query cluster:/1.0/cluster/groups/blah | jq --exit-status '.used_by | .[] == "/1.0/instances/c3"'

  # Check deleting an in use cluster group fails.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete blah || false

  # Clean up for restricted project tests.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3 c4 c5 c6

  sub_test "Restricted project group references and visibility"
  # Create an empty cluster group and reference it from project config.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create cluster:fizz
  LXD_DIR="${LXD_ONE_DIR}" lxc project create cluster:buzz -c restricted=true -c restricted.cluster.groups=fizz

  # Cannot launch an instance because fizz has no members.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --project buzz || false

  # Group fizz has no members, but it cannot be deleted because it is referenced by project buzz.
  LXD_DIR="${LXD_ONE_DIR}" lxc_remote query cluster:/1.0/cluster/groups/fizz | jq --exit-status '.used_by | .[] == "/1.0/projects/buzz"'
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:fizz || false

  # Restricted certificate does not see project fizz in cluster group used by URLs.
  token1="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add cluster: --name cg-cert1 --quiet --restricted --projects default)"
  LXD_CONF1=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF1}" gen_cert_and_key "client"
  LXD_CONF="${LXD_CONF1}" lxc remote add cluster_remote "${token1}"
  LXD_CONF="${LXD_CONF1}" lxc_remote query cluster_remote:/1.0/cluster/groups/fizz | jq --exit-status '.used_by == []'

  # Fine-grained TLS identity does not see project fizz in cluster group used by URLs unless any groups it is a member of have can_view on the project.
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group create cluster:test-group
  token2="$(LXD_DIR="${LXD_ONE_DIR}" lxc auth identity create cluster:tls/gc-cert2 --group test-group --quiet)"
  LXD_CONF2=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF2}" gen_cert_and_key "client"
  LXD_CONF="${LXD_CONF2}" lxc remote add cluster_remote "${token2}"
  LXD_CONF="${LXD_CONF2}" lxc_remote query cluster_remote:/1.0/cluster/groups/fizz | jq --exit-status '.used_by == []'
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group permission add cluster:test-group project buzz can_view
  LXD_CONF="${LXD_CONF2}" lxc_remote query cluster_remote:/1.0/cluster/groups/fizz | jq --exit-status '.used_by | .[] == "/1.0/projects/buzz"'

  # Clean up.
  LXD_DIR="${LXD_ONE_DIR}" lxc config trust remove "cluster:$(cert_fingerprint "${LXD_CONF1}/client.crt")"
  LXD_DIR="${LXD_ONE_DIR}" lxc auth identity delete cluster:tls/gc-cert2
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group delete cluster:test-group
  rm -rf "${LXD_CONF1}" "${LXD_CONF2}"
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete cluster:buzz
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group delete cluster:fizz

  sub_test "Restricted project targeting rules"
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo -c features.images=false -c restricted=true -c restricted.cluster.groups=blah
  LXD_DIR="${LXD_ONE_DIR}" lxc profile show default | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default --project foo

  # Check cannot create instance in restricted project that only allows blah group, when the only member that exists in the blah group also has scheduler.instance=group set (so it must be targeted via group or directly).
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --project foo || false

  # Check cannot create instance in restricted project when targeting a member that isn't in the restricted project's allowed cluster groups list.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --project foo --target=node1 || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --project foo --target=@foobar2 || false

  # Check can create instance in restricted project when not targeting any specific member, but that it will only be created on members within the project's allowed cluster groups list.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster unset cluster:node2 scheduler.instance
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --project foo
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c2 --project foo
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c1 --project foo)" = "node2" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c2 --project foo)" = "node2" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 --project foo

  # Check can specify any member or group when "restricted.cluster.groups" is empty.
  LXD_DIR="${LXD_ONE_DIR}" lxc project unset foo restricted.cluster.groups
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c1 --project foo --target=node1
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c1 --project foo)" = "node1" ]

  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty cluster:c2 --project foo --target=@blah
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L cluster:c2 --project foo)" = "node2" ]

  # Check "volatile.cluster.group" is set correctly.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster:c2 --project foo volatile.cluster.group)" = "blah" ]

  # Re-set "restricted.cluster.groups" so we can verify both project and instance are in used_by.
  LXD_DIR="${LXD_ONE_DIR}" lxc project set foo restricted.cluster.groups=blah

  # Verify that both project foo and instance c2 with "volatile.cluster.group" are reported in used_by.
  LXD_DIR="${LXD_ONE_DIR}" lxc_remote query cluster:/1.0/cluster/groups/blah | jq --exit-status '.used_by | contains(["/1.0/instances/c2?project=foo", "/1.0/projects/foo"])'

  # Clean up.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 --project foo
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo
  LXD_DIR="${LXD_ONE_DIR}" lxc remote rm cluster

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

test_clustering_events() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node.
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Spawn a fourth node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Spawn a fifth node.
  spawn_lxd_and_join_cluster "${cert}" 5 1 "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"

  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage

  # c1 should go to node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1 --target=node1
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c L c1)" = "node1" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c2 --target=node2

  LXD_DIR="${LXD_ONE_DIR}" stdbuf -oL lxc monitor --type=lifecycle > "${TEST_DIR}/node1.log" &
  monitorNode1PID=$!
  LXD_DIR="${LXD_TWO_DIR}" stdbuf -oL lxc monitor --type=lifecycle > "${TEST_DIR}/node2.log" &
  monitorNode2PID=$!
  LXD_DIR="${LXD_THREE_DIR}" stdbuf -oL lxc monitor --type=lifecycle > "${TEST_DIR}/node3.log" &
  monitorNode3PID=$!

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2
  sleep 0.1

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -wFc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "2" ]
  done

  # Switch into event-hub mode.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hub
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster role add node2 event-hub
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wFc event-hub)" = "2" ]

  # Check events were distributed.
  for i in 1 2 3; do
    [ "$(grep -wFc "cluster-member-updated" "${TEST_DIR}/node${i}.log")" = "2" ]
  done

  sleep 1 # Wait for notification heartbeat to distribute new roles.
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: hub-server"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: hub-server"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: hub-client"

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2
  sleep 0.1

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -wFc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "4" ]
  done

  # Init container on node3 to check image distribution events work during event-hub mode.
  LXD_DIR="${LXD_THREE_DIR}" lxc init testimage c3 --target=node3

  for i in 1 2 3; do
    [ "$(grep -wFc "instance-created" "${TEST_DIR}/node${i}.log")" = "1" ]
  done

  # Switch into full-mesh mode by removing one event-hub role so there is <2 in the cluster.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role remove node1 event-hub
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wFc event-hub)" = "1" ]

  sleep 1 # Wait for notification heartbeat to distribute new roles.
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"

  # Check events were distributed.
  for i in 1 2 3; do
    [ "$(grep -wFc "cluster-member-updated" "${TEST_DIR}/node${i}.log")" = "3" ]
  done

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2
  sleep 0.1

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -wFc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "6" ]
  done

  # Switch back into event-hub mode by giving the role to node4 and node5.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster role remove node2 event-hub
  LXD_DIR="${LXD_FOUR_DIR}" lxc cluster role add node4 event-hub
  LXD_DIR="${LXD_FIVE_DIR}" lxc cluster role add node5 event-hub

  sleep 1 # Wait for notification heartbeat to distribute new roles.
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: hub-server"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: hub-server"

  # Shutdown the hub servers.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster ls

  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown

  sleep 11
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster ls

  # Confirm that local operations are not blocked by having no event hubs running, but that events are not being
  # distributed.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  sleep 0.1

  [ "$(grep -wFc "instance-restarted" "${TEST_DIR}/node1.log")" = "7" ]
  for i in 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -wFc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "6" ]
  done

  # Kill monitors.
  kill_go_proc "${monitorNode1PID}" || true
  kill_go_proc "${monitorNode2PID}" || true
  kill_go_proc "${monitorNode3PID}" || true

  # Cleanup
  # XXX: deleting c1 c2 and c3 at once causes the test to fail with
  # `No active cluster event listener clients` and `Failed heartbeat`
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1
  LXD_DIR="${LXD_TWO_DIR}" lxc delete -f c2
  LXD_DIR="${LXD_THREE_DIR}" lxc delete c3
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_FIVE_DIR}"
}

test_clustering_roles() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node.
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Spawn a fourth node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Spawn a fifth node.
  spawn_lxd_and_join_cluster "${cert}" 5 1 "${LXD_ONE_DIR}"

  # Configure cluster with max_voters=3 and max_standby=1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.max_voters=3 cluster.max_standby=1 cluster.offline_threshold=11

  sleep 12 # Wait a bit for cluster to stabilize.

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster ls

  # Get cluster list once and reuse it for all queries.
  cluster_list=$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list -f json)

  # Find a member without database-voter role (to test adding it).
  non_voter_member="$(jq --exit-status --raw-output '[.[] | select(any(.roles[]; contains("database-voter")) | not) | .server_name] | first' <<< "${cluster_list}")"
  echo "Found non-voter member: ${non_voter_member}"

  # Find a member without database-standby role (to test adding it).
  non_standby_member="$(jq --exit-status --raw-output '[.[] | select(any(.roles[]; contains("database-standby")) | not) | .server_name] | first' <<< "${cluster_list}")"
  echo "Found non-standby member: ${non_standby_member}"

  # Find a member without database-leader role (to test adding it).
  non_leader_member="$(jq --exit-status --raw-output '[.[] | select(any(.roles[]; contains("database-leader")) | not) | .server_name] | first' <<< "${cluster_list}")"
  echo "Found non-leader member: ${non_leader_member}"

  echo "==> Reject adding automatic role 'database-voter'"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add "${non_voter_member}" database-voter 2>&1)" = 'Error: The automatically assigned "database-voter" role cannot be added manually' ]

  echo "==> Reject adding automatic role 'database-standby'"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add "${non_standby_member}" database-standby 2>&1)" = 'Error: The automatically assigned "database-standby" role cannot be added manually' ]

  echo "==> Reject adding automatic role 'database-leader'"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add "${non_leader_member}" database-leader 2>&1)" = 'Error: The automatically assigned "database-leader" role cannot be added manually' ]

  echo "==> Reject invalid role name"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 invalid-role 2>&1)" = 'Error: Invalid cluster role "invalid-role"' ]

  echo "==> Reject typo in role name"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hubb 2>&1)" = 'Error: Invalid cluster role "event-hubb"' ]

  echo "==> Reject duplicate roles in request"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hub,event-hub 2>&1)" = 'Error: Duplicate role "event-hub" in request' ]

  echo "==> Accept valid manual role addition"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hub
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- event-hub"

  echo "==> Accept adding multiple manual roles"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 ovn-chassis
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- event-hub"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- ovn-chassis"

  echo "==> Reject adding role member already has"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hub 2>&1)" = 'Error: Member "node1" already has role "event-hub"' ]

  echo "==> Accept removing manual role"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role remove node1 event-hub
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- event-hub" || false
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- ovn-chassis"

  echo "==> Reject removing role member does not have"
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster role remove node1 event-hub 2>&1)" = 'Error: Member "node1" does not have role "event-hub"' ]

  echo "==> Cleanup"
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_FIVE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_ONE_DIR}"
}

test_clustering_uuid() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # spawn an instance on the first LXD node
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 --target=node1
  # get its volatile.uuid
  uuid_before_move=$(LXD_DIR="${LXD_ONE_DIR}" lxc config get c1 volatile.uuid)
  # move the instance to the second LXD node
  LXD_DIR="${LXD_ONE_DIR}" lxc move c1 --target=node2
  # get the volatile.uuid of the moved instance on the second node
  uuid_after_move=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get c1 volatile.uuid)

  # check that the uuid have not changed, else return an error
  if [ "${uuid_before_move}" != "${uuid_after_move}" ]; then
    echo "UUID changed after move"
    false
  fi

  # cleanup
  LXD_DIR="${LXD_TWO_DIR}" lxc delete c1
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_trust_add() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Check using token that is expired

  # Set token expiry to 1 seconds
  LXD_DIR="${LXD_ONE_DIR}" lxc config set core.remote_token_expiry 1S

  # Get a certificate add token from LXD_ONE. The operation will run on LXD_ONE locally.
  lxd_one_token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  sleep 1.1

  # Expect one running token operation.
  operation_uuid="$(LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "TOKEN,Certificate add token,RUNNING" | cut -d, -f1 )"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Certificate add token,RUNNING"
  is_uuid_v7 "${operation_uuid}"

  # Get the address of LXD_TWO.
  lxd_two_address="https://$(LXD_DIR="${LXD_TWO_DIR}" lxc config get core.https_address)"

  # Test adding the remote using the address of LXD_TWO with the token operation running on LXD_ONE.
  # LXD_TWO does not have the operation running locally, so it should find the UUID of the operation in the database
  # and query LXD_ONE for it. LXD_TWO should cancel the operation by sending a DELETE /1.0/operations/{uuid} to LXD_ONE
  # and needs to parse the metadata of the operation into the correct type to complete the trust process.
  # The expiry time should be parsed and found to be expired so the add action should fail.
  ! lxc remote add lxd_two "${lxd_two_address}" --token "${lxd_one_token}" || false

  # Expect the operation to be cancelled.
  LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Certificate add token,CANCELLED"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Certificate add token,CANCELLED"

  # Set token expiry to 1 hour
  LXD_DIR="${LXD_ONE_DIR}" lxc config set core.remote_token_expiry 1H

  # Check using token that isn't expired

  # Get a certificate add token from LXD_ONE. The operation will run on LXD_ONE locally.
  lxd_one_token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"

  # Expect one running token operation.
  operation_uuid="$(LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "TOKEN,Certificate add token,RUNNING" | cut -d, -f1 )"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Certificate add token,RUNNING"
  is_uuid_v7 "${operation_uuid}"

  # Test adding the remote using the address of LXD_TWO with the token operation running on LXD_ONE.
  # LXD_TWO does not have the operation running locally, so it should find the UUID of the operation in the database
  # and query LXD_ONE for it. LXD_TWO should cancel the operation by sending a DELETE /1.0/operations/{uuid} to LXD_ONE
  # and needs to parse the metadata of the operation into the correct type to complete the trust process.
  lxc remote add lxd_two "${lxd_two_address}" --token "${lxd_one_token}"

  # Expect the operation to be cancelled.
  LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Certificate add token,CANCELLED"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Certificate add token,CANCELLED"

  # Clean up
  lxc remote rm lxd_two

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_projects_force_delete() {
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  echo "Capture baseline state before creating project."
  VOLUMES_BEFORE="$(LXD_DIR="${LXD_ONE_DIR}" lxc storage volume list -f csv --all-projects)"
  ACLS_BEFORE="$(LXD_DIR="${LXD_ONE_DIR}" lxc network acl list -f csv --all-projects)"
  ZONES_BEFORE="$(LXD_DIR="${LXD_ONE_DIR}" lxc network zone list -f csv --all-projects)"
  PROFILES_BEFORE="$(LXD_DIR="${LXD_ONE_DIR}" lxc profile list -f csv --all-projects)"
  IMAGES_BEFORE="$(LXD_DIR="${LXD_ONE_DIR}" lxc image list -f csv --all-projects)"
  INSTANCES_BEFORE="$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv --all-projects)"

  echo "Create project with all features enabled."
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo -c features.networks=true -c features.networks.zones=true -c features.images=true -c features.profiles=true -c features.storage.volumes=true -c features.storage.buckets=true

  echo "Create storage volume in project on node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 custom/vol1 --project foo --target node1

  echo "Create network ACL in project."
  LXD_DIR="${LXD_ONE_DIR}" lxc network acl create acl1 --project foo

  echo "Create network zone in project."
  LXD_DIR="${LXD_ONE_DIR}" lxc network zone create zone1 --project foo

  echo "Create profile in project."
  LXD_DIR="${LXD_ONE_DIR}" lxc profile create profile1 --project foo

  echo "Add image to project."
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage foo

  echo "Create instance in project on node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 --project foo --target node1 -s pool1

  echo "Create another instance on node2."
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c2 --project foo --target node2 -s pool1

  echo "Create storage volume on node2."
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 custom/vol2 --project foo --target node2

  echo "Check entities exist on both nodes."
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c n --all-projects | grep -c "c[12]")" = 2 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc storage volume list -f csv -c n --all-projects | grep -c "vol[12]")" = 2 ]

  echo "Check that regular delete fails on non-empty project."
  ! LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo || false

  echo "Check forced project deletion from node1."
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo --force

  echo "Check project is deleted from both nodes."
  ! LXD_DIR="${LXD_ONE_DIR}" lxc project show foo || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc project show foo || false

  echo "Verify all entities were cleaned up by comparing before/after state."
  VOLUMES_AFTER="$(LXD_DIR="${LXD_ONE_DIR}" lxc storage volume list -f csv --all-projects)"
  ACLS_AFTER="$(LXD_DIR="${LXD_ONE_DIR}" lxc network acl list -f csv --all-projects)"
  ZONES_AFTER="$(LXD_DIR="${LXD_ONE_DIR}" lxc network zone list -f csv --all-projects)"
  PROFILES_AFTER="$(LXD_DIR="${LXD_ONE_DIR}" lxc profile list -f csv --all-projects)"
  IMAGES_AFTER="$(LXD_DIR="${LXD_ONE_DIR}" lxc image list -f csv --all-projects)"
  INSTANCES_AFTER="$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv --all-projects)"

  [ "${VOLUMES_BEFORE}" = "${VOLUMES_AFTER}" ]
  [ "${ACLS_BEFORE}" = "${ACLS_AFTER}" ]
  [ "${ZONES_BEFORE}" = "${ZONES_AFTER}" ]
  [ "${PROFILES_BEFORE}" = "${PROFILES_AFTER}" ]
  [ "${IMAGES_BEFORE}" = "${IMAGES_AFTER}" ]
  [ "${INSTANCES_BEFORE}" = "${INSTANCES_AFTER}" ]

  echo "Verify same state from node2."
  VOLUMES_AFTER_NODE2="$(LXD_DIR="${LXD_TWO_DIR}" lxc storage volume list -f csv --all-projects)"
  INSTANCES_AFTER_NODE2="$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv --all-projects)"
  [ "${VOLUMES_BEFORE}" = "${VOLUMES_AFTER_NODE2}" ]
  [ "${INSTANCES_BEFORE}" = "${INSTANCES_AFTER_NODE2}" ]

  # Clean up cluster
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_placement_groups() {
  echo "Create cluster with 5 members."
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Spawn a fourth node.
  spawn_lxd_and_join_cluster "${cert}" 4 1 "${LXD_ONE_DIR}"

  # Spawn a fifth node.
  spawn_lxd_and_join_cluster "${cert}" 5 1 "${LXD_ONE_DIR}"

  echo "==> Test spread/strict: initial placement"
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-spread-strict policy=spread rigor=strict

  echo "Create first instance (any node)"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 -c placement.group=pg-spread-strict
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c s c1)" = "STOPPED" ]

  echo "Verify placement group reports the instance in used_by"
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/placement-groups/pg-spread-strict" | jq --exit-status '.used_by | .[] == "/1.0/instances/c1"'
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/placement-groups/pg-spread-strict?recursion=1" | jq --exit-status '.used_by | .[] == "/1.0/instances/c1"'

  echo "==> Test spread/strict: second instance should be on different node"
  node1=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c1 -f csv -c L)
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c2 -c placement.group=pg-spread-strict
  node2=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c2 -f csv -c L)
  [ "${node1}" != "${node2}" ]

  echo "Verify placement group used_by contains both instances"
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/placement-groups/pg-spread-strict" | jq --exit-status '.used_by | contains(["/1.0/instances/c1", "/1.0/instances/c2"])'
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/placement-groups/pg-spread-strict?recursion=1" | jq --exit-status '.used_by | contains(["/1.0/instances/c1", "/1.0/instances/c2"])'

  echo "==> Test spread/strict: add instances to all 5 nodes"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c3 -c placement.group=pg-spread-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c4 -c placement.group=pg-spread-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c5 -c placement.group=pg-spread-strict

  echo "Verify all 5 instances are on different nodes"
  nodes=$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c nL | grep "^c[1-5]," | cut -d, -f2 | sort | uniq | wc -l)
  [ "${nodes}" = "5" ]

  echo "==> Test spread/strict: instance creation should fail with all members occupied"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c6 -c placement.group=pg-spread-strict || false

  # Clean up for next test
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3 c4 c5

  echo "==> Test spread/permissive: initial placement"
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-spread-permissive policy=spread rigor=permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 -c placement.group=pg-spread-permissive
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c s c1)" = "STOPPED" ]

  echo "==> Test spread/permissive: prefer nodes with minimum instances"

  echo "Create uneven distribution: 2 instances on node1, 2 on node2, 1 on node3"

  echo "Create instance on first node"
  node1=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c1 -f csv -c L)
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c2 -c placement.group=pg-spread-permissive --target "${node1}"

  echo "Create pair of instances on second node"
  for node in node1 node2 node3 node4 node5; do
    if [ "${node}" != "${node1}" ]; then
      node2="${node}"
      break
    fi
  done
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c3 -c placement.group=pg-spread-permissive --target "${node2}"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c4 -c placement.group=pg-spread-permissive --target "${node2}"

  echo "Create instance on third node"
  for node in node1 node2 node3 node4 node5; do
    if [ "${node}" != "${node1}" ] && [ "${node}" != "${node2}" ]; then
      node3="${node}"
      break
    fi
  done
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c5 -c placement.group=pg-spread-permissive --target "${node3}"

  echo "Verify next instance goes to a node with 0 instances (node4 or node5)"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c6 -c placement.group=pg-spread-permissive
  node6=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c6 -f csv -c L)
  [ "${node6}" != "${node1}" ] && [ "${node6}" != "${node2}" ] && [ "${node6}" != "${node3}" ]

  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3 c4 c5 c6

  echo "==> Test compact/strict: initial placement using node2 as target member"
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-compact-strict policy=compact rigor=strict
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 -c placement.group=pg-compact-strict --target node2
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c s c1)" = "STOPPED" ]

  echo "==> Test compact/strict: second instance on same node"
  node1=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c1 -f csv -c L)
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c2 -c placement.group=pg-compact-strict
  node2=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c2 -f csv -c L)
  [ "${node1}" = "node2" ] && [ "${node2}" = "node2" ]

  echo "==> Test compact/strict: picks member with most instances"
  echo "Manually place an instance on node3 to create split placement"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c3 -c placement.group=pg-compact-strict --target node3
  node3=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c3 -f csv -c L)
  [ "${node3}" = "node3" ]

  echo "New instance should go to node2 (2 instances) not node3 (1 instance)"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c4 -c placement.group=pg-compact-strict
  node4=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c4 -f csv -c L)
  [ "${node4}" = "node2" ]

  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3 c4

  echo "==> Test compact/permissive: initial placement"
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-compact-permissive policy=compact rigor=permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 -c placement.group=pg-compact-permissive
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c s c1)" = "STOPPED" ]

  echo "==> Test compact/permissive: prefer same node"
  node1=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c1 -f csv -c L)
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c2 -c placement.group=pg-compact-permissive
  node2=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c2 -f csv -c L)
  [ "${node1}" = "${node2}" ]

  echo "==> Test compact/permissive: picks member with most instances"
  echo "Manually place an instance on different node to create split placement"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c3 -c placement.group=pg-compact-permissive --target node3
  node3=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c3 -f csv -c L)
  [ "${node3}" = "node3" ]

  echo "New instance should prefer node with most instances (node1/2) over node3"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c4 -c placement.group=pg-compact-permissive
  node4=$(LXD_DIR="${LXD_ONE_DIR}" lxc list c4 -f csv -c L)
  [ "${node4}" = "${node1}" ]

  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3 c4

  echo "==> Test placement groups are project-specific"
  LXD_DIR="${LXD_ONE_DIR}" lxc project create test-project -c features.images=false -c features.profiles=false
  LXD_DIR="${LXD_ONE_DIR}" lxc project switch test-project

  # Same name in different project should work
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-spread-strict policy=spread rigor=strict
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group list | grep pg-spread-strict

  # Check used_by
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 -c placement.group=pg-spread-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/placement-groups/pg-spread-strict?project=test-project" | jq --exit-status '.used_by | .[] == "/1.0/instances/c1?project=test-project"'

  # Switch back to default
  LXD_DIR="${LXD_ONE_DIR}" lxc project switch default

  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc placement-group list -f csv | wc -l)" = "4" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc placement-group list --project test-project -f csv | wc -l)" = "1" ]

  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete test-project --force

  echo "==> Test placement group validation: required fields"
  # Cannot create without policy
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid rigor=strict || false
  # Cannot create without rigor
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid policy=spread || false
  # Cannot create without both
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid || false

  echo "==> Test placement group validation: invalid values"
  # Invalid policy value
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid policy=invalid rigor=strict || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid policy=distribute rigor=strict || false
  # Invalid rigor value
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid policy=spread rigor=invalid || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid policy=spread rigor=hard || false
  # Create valid placement group for set validation tests
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-invalid-test policy=spread rigor=strict
  # Cannot set invalid policy
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group set pg-invalid-test policy invalid || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group set pg-invalid-test policy distribute || false
  # Cannot set invalid rigor
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group set pg-invalid-test rigor invalid || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group set pg-invalid-test rigor hard || false
  # Verify original values unchanged after failed sets
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc placement-group get pg-invalid-test policy)" = "spread" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc placement-group get pg-invalid-test rigor)" = "strict" ]
  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-invalid-test

  echo "==> Test placement group rename"
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group create pg-old policy=spread rigor=strict
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group rename pg-old pg-new
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group list | grep pg-new
  ! LXD_DIR="${LXD_ONE_DIR}" lxc placement-group list | grep pg-old || false

  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-new
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-spread-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-spread-permissive
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-compact-strict
  LXD_DIR="${LXD_ONE_DIR}" lxc placement-group delete pg-compact-permissive
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
  kill_lxd "${LXD_FOUR_DIR}"
  kill_lxd "${LXD_FIVE_DIR}"
}

test_clustering_force_removal() {
  echo "Create cluster with 3 members."
  spawn_lxd_and_bootstrap_cluster

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}"

  # Spawn a third node.
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}"

  # Spawn an instance on the third node.
  LXD_DIR="${LXD_THREE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage foo --target node3

  # Spawn another instance on another node using the same name.
  # This allows checking that the force removal doesn't accidentally clean too much.
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage foo
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage foo --storage data --target node2 --project foo

  # Create custom volumes in both projects with the same name.
  # This allows checking that the force removal doesn't accidentally clean too much.
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create data foo
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create data foo --project foo

  # Check the instances and volumes exist.
  LXD_DIR="${LXD_ONE_DIR}" lxc config show foo
  LXD_DIR="${LXD_ONE_DIR}" lxc config show foo --project foo
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume show data foo
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume show data foo --project foo

  # Check there are entries in the DB
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM instances WHERE name = "foo"')" = 2 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM storage_volumes WHERE name = "foo"')" = 4 ]

  # Force remove the third node.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --force --yes

  # Check the instance on the removed node is gone.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config show foo || false

  # Check the other instance and volumes still exist.
  LXD_DIR="${LXD_ONE_DIR}" lxc config show foo --project foo
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume show data foo
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume show data foo --project foo

  # Check there are no traces of the removed instance left in the DB.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM instances WHERE name = "foo"')" = 1 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM storage_volumes WHERE name = "foo"')" = 3 ]

  # Clean up.
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete data foo
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo --force

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

test_clustering_recovery() {
  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  local poolDriver
  poolDriver="$(storage_backend "$LXD_INITIAL_DIR")"

  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node.
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # Spawn a third node using a custom loop device outside of LXD's directory.
  configure_loop_device loop_file_1 loop_device_1 128M  # 128M to accommodate for btrfs
  # shellcheck disable=SC2154
  source="${loop_device_1}"
  if [ "${poolDriver}" = "dir" ]; then
    # The dir driver is special as it requires the source to be a directory.
    mkfs.ext4 -E assume_storage_prezeroed=1 -m0 "${source}"
    mkdir -p "${TEST_DIR}/pools/data"
    mount "${source}" "${TEST_DIR}/pools/data"
    source="${TEST_DIR}/pools/data"
  fi
  spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}" "${poolDriver}" 8443 "${source}"

  # Create an instance and custom volume on the third node's data pool.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 -s data --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create data vol1 --target node3 size=32MiB

  # Kill the third cluster member.
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  kill_lxd "${LXD_THREE_DIR}"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --force --yes

  # Check that both the instance and custom volume are gone.
  # When using Ceph RBD the volume is still present as it is not bound to any cluster member.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config show c1 || false
  if [ "${poolDriver}" != "ceph" ]; then
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage volume show data vol1 || false
  fi

  # Recreate the third cluster member.
  if [ "${poolDriver}" = "zfs" ]; then
    # Use the name of the existing ZFS zpool as source.
    source="lxdtest-$(basename "${TEST_DIR}")-${ns3}"
  fi
  # Recreate the original directory of the third cluster member.
  # We reuse the name (path) to ensure the same name of the underlying storage artifacts.
  LXD_DIR_KEEP="${LXD_THREE_DIR}" LXD_NETNS_KEEP="${ns3}" spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}" "${poolDriver}" 8443 "${source}" true

  # Recover instance and custom volume from the third node's data pool.
  # We also require recovery for remote drivers as the DB entries got purged when force removing the cluster member.
  LXD_DIR="${LXD_THREE_DIR}" lxd recover <<EOF
yes
yes
EOF

  # Confirm that both the instance and custom volume were recovered.
  LXD_DIR="${LXD_ONE_DIR}" lxc config show c1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume show data vol1

  # Cleanup.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete data vol1

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_TWO_DIR}" lxc storage delete data

  if [ "${poolDriver}" = "dir" ]; then
    umount "${TEST_DIR}/pools/data"
    rm -rf "${TEST_DIR}/pools/data"
  fi
  sed -i "\\|^${loop_device_1}|d" "${TEST_DIR}/loops"
  losetup -d "${loop_device_1}"
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}
