test_clustering_enable() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  # Test specified core.https_address with no cluster.https_address
  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}

    lxc config show | grep "core.https_address" | grep -qE "127.0.0.1:[0-9]{4,5}$"
    # Launch a container.
    ensure_import_testimage
    lxc storage create default dir
    lxc profile device add default root disk path="/" pool="default"
    lxc launch testimage c1

    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q node1

    # The container is still there and now shows up as
    # running on node 1.
    lxc list | grep c1 | grep -q node1

    # Clustering can't be enabled on an already clustered instance.
    ! lxc cluster enable node2 || false

    # Delete the container
    lxc stop c1 --force
    lxc delete c1
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test wildcard core.https_address with no cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address ::
    # Enable clustering.
    ! lxc cluster enable node1 || false
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test default port core.https_address with no cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address 127.0.0.1
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test wildcard core.https_address with valid cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address ::
    lxc config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test empty core.https_address with no cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config unset core.https_address
    # Enable clustering.
    ! lxc cluster enable node1 || false
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test empty core.https_address with valid cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config unset core.https_address
    lxc config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test empty core.https_address with default port cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config unset core.https_address
    lxc config set cluster.https_address 127.0.0.1
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test covered cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address 127.0.0.1:8443
    lxc config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test cluster listener after reload
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set cluster.https_address 127.0.0.1:8443
    kill -9 "$(cat "${LXD_DIR}/lxd.pid")"
    respawn_lxd "${LXD_DIR}" true
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -q 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"
}

test_clustering_membership() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -q 'cluster.offline_threshold: "11"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -q 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(cat "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(cat "${TEST_DIR}/ns/${ns2}/PID")"
  nsenter -m -n -t "${ns1_pid}" -- ip link show "${bridge}" > /dev/null
  nsenter -m -n -t "${ns2_pid}" -- ip link show "${bridge}" > /dev/null

  # Create a pending network and pool, to show that they are not
  # considered when checking if the joining node has all the required
  # networks and pools.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create net1 --target node2

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 4 "${LXD_FIVE_DIR}"

  # List all nodes, using clients points to different nodes and
  # checking which are database nodes and which are database-standby nodes.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -q "\- database-leader$"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep -Fc "database-standby" | grep -Fx 2
  LXD_DIR="${LXD_FIVE_DIR}" lxc cluster list | grep -Fc "database " | grep -Fx 3

  # Show a single node
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node5 | grep -q "node5"

  # Client certificate are shared across all nodes.
  lxc remote add cluster 10.1.1.101:8443 --accept-certificate --password=sekret
  lxc remote set-url cluster https://10.1.1.102:8443
  lxc network list cluster: | grep -q "${bridge}"
  lxc remote remove cluster

  # Check info for single node (from local and remote node).
  LXD_DIR="${LXD_FIVE_DIR}" lxc cluster info node5
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster info node5

  # Disable image replication
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.images_minimal_replica 1

  # Shutdown a database node, and wait a few seconds so it will be
  # detected as down.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 12
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node3 | grep -q "status: Offline"

  # Gracefully remove a node and check trust certificate is removed.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep node4
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'SELECT name FROM certificates WHERE type = 2' | grep node4
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster remove node4
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep node4 || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'SELECT name FROM certificates WHERE type = 2' | grep node4 || false

  # The node isn't clustered anymore.
  ! LXD_DIR="${LXD_FOUR_DIR}" lxc cluster list || false

  # Generate a join token for the sixth node.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  token=$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add node6 | tail -n 1)

  # Check token is associated to correct name.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node6 | grep "${token}"

  # Spawn a sixth node, using join token.
  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_SIX_DIR}"
  ns6="${prefix}6"

  # shellcheck disable=SC2034
  LXD_SECRET="${token}"
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 2 "${LXD_SIX_DIR}"
  unset LXD_SECRET

  # Check token has been deleted after join.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node6 || false

  # Generate a join token for a seventh node
  token=$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add node7 | tail -n 1)

  # Check token is associated to correct name
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node7 | grep "${token}"

  # Revoke the token
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster revoke-token node7 | tail -n 1

  # Check token has been deleted
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node7 || false

  # Set cluster token expiry to 30 seconds
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.join_token_expiry=30S

  # Generate a join token for an eigth and ninth node
  token_valid=$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add node8 | tail -n 1)

  # Spawn an eigth node, using join token.
  setup_clustering_netns 8
  LXD_EIGHT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_EIGHT_DIR}"
  ns8="${prefix}8"

  # shellcheck disable=SC2034
  LXD_SECRET="${token_valid}"
  spawn_lxd_and_join_cluster "${ns8}" "${bridge}" "${cert}" 8 2 "${LXD_EIGHT_DIR}"
  unset LXD_SECRET

  # This will cause the token to expire
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.join_token_expiry=5S
  token_expired=$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add node9 | tail -n 1)
  sleep 6

  # Spawn a ninth node, using join token.
  setup_clustering_netns 9
  LXD_NINE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_NINE_DIR}"
  ns9="${prefix}9"

  # shellcheck disable=SC2034
  LXD_SECRET="${token_expired}"
  ! spawn_lxd_and_join_cluster "${ns9}" "${bridge}" "${cert}" 9 2 "${LXD_NINE_DIR}" || false
  unset LXD_SECRET

  # Unset join_token_expiry which will set it to the default value of 3h
  LXD_DIR="${LXD_ONE_DIR}" lxc config unset cluster.join_token_expiry

  LXD_DIR="${LXD_NINE_DIR}" lxd shutdown
  LXD_DIR="${LXD_EIGHT_DIR}" lxd shutdown
  LXD_DIR="${LXD_SIX_DIR}" lxd shutdown
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Init a container on node2, using a client connected to node1
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage foo

  # The container is visible through both nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep foo | grep -q STOPPED
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep foo | grep -q node2
  LXD_DIR="${LXD_TWO_DIR}" lxc list | grep foo | grep -q STOPPED

  # A Location: field indicates on which node the container is running
  LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q "Location: node2"

  # Start the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc start foo
  LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep foo | grep -q RUNNING

  # Trying to delete a node which has container results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node2 || false

  # Exec a command in the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc exec foo ls / | grep -q proc

  # Pull, push and delete files from the container via node1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/non-existing-file "${TEST_DIR}/non-existing-file" || false
  mkdir "${TEST_DIR}/hello-world"
  echo "hello world" > "${TEST_DIR}/hello-world/text"
  LXD_DIR="${LXD_ONE_DIR}" lxc file push "${TEST_DIR}/hello-world/text" foo/hello-world-text
  LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world-text "${TEST_DIR}/hello-world-text"
  grep -q "hello world" "${TEST_DIR}/hello-world-text"
  rm "${TEST_DIR}/hello-world-text"
  LXD_DIR="${LXD_ONE_DIR}" lxc file push --recursive "${TEST_DIR}/hello-world" foo/
  rm -r "${TEST_DIR}/hello-world"
  LXD_DIR="${LXD_ONE_DIR}" lxc file pull --recursive foo/hello-world "${TEST_DIR}"
  grep -q "hello world" "${TEST_DIR}/hello-world/text"
  rm -r "${TEST_DIR}/hello-world"
  LXD_DIR="${LXD_ONE_DIR}" lxc file delete foo/hello-world/text
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world/text "${TEST_DIR}/hello-world-text" || false

  # Stop the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force

  # Rename the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo foo2
  LXD_DIR="${LXD_TWO_DIR}" lxc list | grep -q foo2
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo2 foo

  # Show lxc.log via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc info --show-log foo | grep -q Log

  # Create, rename and delete a snapshot of the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo foo-bak
  LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q foo-bak
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo/foo-bak foo/foo-bak-2
  LXD_DIR="${LXD_ONE_DIR}" lxc delete foo/foo-bak-2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q foo-bak-2 || false

  # Export from node1 the image that was imported on node2
  LXD_DIR="${LXD_ONE_DIR}" lxc image export testimage "${TEST_DIR}/testimage"
  rm "${TEST_DIR}/testimage.tar.xz"

  # Create a container on node1 using the image that was stored on
  # node2.
  LXD_DIR="${LXD_TWO_DIR}" lxc launch --target node1 testimage bar
  LXD_DIR="${LXD_TWO_DIR}" lxc stop bar --force
  LXD_DIR="${LXD_ONE_DIR}" lxc delete bar
  ! LXD_DIR="${LXD_TWO_DIR}" lxc list | grep -q bar || false

  # Create a container on node1 using a snapshot from node2.
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo foo-bak
  LXD_DIR="${LXD_TWO_DIR}" lxc copy foo/foo-bak bar --target node1
  LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -q "Location: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc delete bar

  # Copy the container on node2 to node3, using a client connected to
  # node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc copy foo bar --target node3
  LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -q "Location: node3"

  # Move the container on node3 to node1, using a client connected to
  # node2 and a different container name than the original one. The
  # volatile.apply_template config key is preserved.
  apply_template1=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get bar volatile.apply_template)

  LXD_DIR="${LXD_TWO_DIR}" lxc move bar egg --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc info egg | grep -q "Location: node2"
  apply_template2=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)
  [ "${apply_template1}" =  "${apply_template2}" ] || false

  # Move back to node3 the container on node1, keeping the same name.
  apply_template1=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)
  LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc info egg | grep -q "Location: node3"
  apply_template2=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)
  [ "${apply_template1}" =  "${apply_template2}" ] || false

  if command -v criu >/dev/null 2>&1; then
    # If CRIU supported, then try doing a live move using same name,
    # as CRIU doesn't work when moving to a different name.
    LXD_DIR="${LXD_TWO_DIR}" lxc config set egg raw.lxc=lxc.console.path=none
    LXD_DIR="${LXD_TWO_DIR}" lxc start egg
    LXD_DIR="${LXD_TWO_DIR}" lxc exec egg -- umount /dev/.lxd-mounts
    LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node1
    LXD_DIR="${LXD_ONE_DIR}" lxc info egg | grep -q "Location: node1"
    LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node3 --stateless
    LXD_DIR="${LXD_TWO_DIR}" lxc stop -f egg
  fi

  # Create backup and attempt to move container. Move should fail and container should remain on node1.
  LXD_DIR="${LXD_THREE_DIR}" lxc query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/instances/egg/backups
  ! LXD_DIR="${LXD_THREE_DIR}" lxc move egg --target node2 || false
  LXD_DIR="${LXD_THREE_DIR}" lxc info egg | grep -q "Location: node3"

  LXD_DIR="${LXD_THREE_DIR}" lxc delete egg

  # Delete the network now, since we're going to shutdown node2 and it
  # won't be possible afterwise.
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Shutdown node 2, wait for it to be considered offline, and list
  # containers.
  LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 12
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep foo | grep -q ERROR

  # Start a container without specifying any target. It will be placed
  # on node1 since node2 is offline and both node1 and node3 have zero
  # containers, but node1 has a lower node ID.
  LXD_DIR="${LXD_THREE_DIR}" lxc launch testimage bar
  LXD_DIR="${LXD_THREE_DIR}" lxc info bar | grep -q "Location: node1"

  # Start a container without specifying any target. It will be placed
  # on node3 since node2 is offline and node1 already has a container.
  LXD_DIR="${LXD_THREE_DIR}" lxc launch testimage egg
  LXD_DIR="${LXD_THREE_DIR}" lxc info egg | grep -q "Location: node3"

  LXD_DIR="${LXD_ONE_DIR}" lxc stop egg --force
  LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Check both nodes show preseeded storage pool created.
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Trying to pass config values other than 'source' results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir source=/foo size=123 --target node1 || false

  # Test storage pool node state tracking using a dir pool.
  if [ "${poolDriver}" = "dir" ]; then
    # Create pending nodes.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" --target node1
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" --target node2
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 0     |"

    # Modify first pending node with invalid config and check it fails and all nodes are pending.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage set pool1 source=/tmp/not/exist --target node1
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" || false
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 0     |"

    # Run create on second node, so it succeeds and then fails notifying first node.
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" || false
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

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
    LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep status: | grep -q Pending
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" || false
    LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep status: | grep -q Errored
    LXD_DIR="${LXD_ONE_DIR}" lxc storage unset pool1 source --target node1
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" rsync.bwlimit=1000 || false # Check global config is rejected on re-create.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}"
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" || false # Check re-create after successful create is rejected.
    LXD_ONE_SOURCE="$(LXD_DIR="${LXD_ONE_DIR}" lxc storage get pool1 source --target=node1)"
    LXD_TWO_SOURCE="$(LXD_DIR="${LXD_TWO_DIR}" lxc storage get pool1 source --target=node2)"
    stat "${LXD_ONE_SOURCE}/containers"
    stat "${LXD_TWO_SOURCE}/containers"

    # Check both nodes marked created.
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

    # Check copying storage volumes works.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 vol1 --target=node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2 --refresh

    # Delete pool and check cleaned up.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol1 --target=node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 vol1 --target=node2
    LXD_DIR="${LXD_TWO_DIR}" lxc storage delete pool1
    ! stat "${LXD_ONE_SOURCE}/containers" || false
    ! stat "${LXD_TWO_SOURCE}/containers" || false
  fi

  # Define storage pools on the two nodes
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

  LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -q node1
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -q node2 || false
  if [ -n "${driver_config_node2}" ]; then
    # shellcheck disable=SC2086
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" ${driver_config_node2} --target node2
  else
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${poolDriver}" --target node2
  fi
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep status: | grep -q Pending

  # A container can't be created when associated with a pending pool.
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -s pool1 testimage bar || false
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage

  # The source config key is not legal for the final pool creation
  if [ "${poolDriver}" = "dir" ]; then
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir source=/foo || false
  fi

  # Create the storage pool
  if [ "${poolDriver}" = "lvm" ]; then
      LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" volume.size=25MiB
  elif [ "${poolDriver}" = "ceph" ]; then
      LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}" volume.size=25MiB ceph.osd.pg_num=16
  else
      LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 "${poolDriver}"
  fi
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep status: | grep -q Created

  # The 'source' config key is omitted when showing the cluster
  # configuration, and included when showing the node-specific one.
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -q source || false
  source1="$(basename "${LXD_ONE_DIR}")"
  source2="$(basename "${LXD_TWO_DIR}")"
  if [ "${poolDriver}" = "ceph" ]; then
    # For ceph volume the source field is the name of the underlying ceph pool
    source1="lxdtest-$(basename "${TEST_DIR}")"
    source2="${source1}"
  fi
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 --target node1 | grep source | grep -q "${source1}"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 --target node2 | grep source | grep -q "${source2}"

  # Update the storage pool
  if [ "${poolDriver}" = "dir" ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc storage set pool1 rsync.bwlimit 10
    LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep rsync.bwlimit | grep -q 10
    LXD_DIR="${LXD_TWO_DIR}" lxc storage unset pool1 rsync.bwlimit
    ! LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -q rsync.bwlimit || false
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
    LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -q "Location: node1"
    LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -q "snap-test"

    # Start and stop the container on its new node1 host
    LXD_DIR="${LXD_TWO_DIR}" lxc start foo
    LXD_DIR="${LXD_TWO_DIR}" lxc stop foo --force

    # Init a new container on node2 using the snapshot on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc copy foo/snap-test egg --target node2
    LXD_DIR="${LXD_TWO_DIR}" lxc start egg
    LXD_DIR="${LXD_ONE_DIR}" lxc stop egg --force
    LXD_DIR="${LXD_ONE_DIR}" lxc delete egg

    # Spawn a third node
    setup_clustering_netns 3
    LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_THREE_DIR}"
    ns3="${prefix}3"
    spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${poolDriver}"

    # Move the container to node3, renaming it
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo bar --target node3
    LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -q "Location: node3"
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -q "snap-test"

    # Shutdown node 3, and wait for it to be considered offline.
    LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 11
    LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
    sleep 12

    # Move the container back to node2, even if node3 is offline
    LXD_DIR="${LXD_ONE_DIR}" lxc move bar --target node2
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -q "Location: node2"
    LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -q "snap-test"

    # Start and stop the container on its new node2 host
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --force --yes

    LXD_DIR="${LXD_ONE_DIR}" lxc delete bar

    # Attach a custom volume to a container on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 v1
    LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 -s pool1 testimage baz
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume attach pool1 custom/v1 baz testDevice /opt

    # Trying to attach a custom volume to a container on another node fails
    LXD_DIR="${LXD_TWO_DIR}" lxc init --target node2 -s pool1 testimage buz
    ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume attach pool1 custom/v1 buz testDevice /opt || false

    # Create an unrelated volume and rename it on a node which differs from the
    # one running the container (issue #6435).
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume create pool1 v2
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename pool1 v2 v2-renamed
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete pool1 v2-renamed

    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume detach pool1 v1 baz

    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete pool1 v1
    LXD_DIR="${LXD_ONE_DIR}" lxc delete baz
    LXD_DIR="${LXD_ONE_DIR}" lxc delete buz

    LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  fi

  # Test migration of zfs/btrfs-based containers
  if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ]; then
    # Launch a container on node2
    LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
    LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 testimage foo
    LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q "Location: node2"

    # Stop the container and move it to node1
    LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo bar --target node1
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -q "Location: node1"

    # Start and stop the migrated container on node1
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    # Rename the container locally on node1
    LXD_DIR="${LXD_TWO_DIR}" lxc rename bar foo
    LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q "Location: node1"

    # Copy the container without specifying a target, it will be placed on node2
    # since it's the one with the least number of containers (0 vs 1)
    sleep 6 # Wait for pending operations to be removed from the database
    LXD_DIR="${LXD_ONE_DIR}" lxc copy foo bar
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -q "Location: node2"

    # Start and stop the copied container on node2
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    # Purge the containers
    LXD_DIR="${LXD_ONE_DIR}" lxc delete bar
    LXD_DIR="${LXD_ONE_DIR}" lxc delete foo

    # Delete the image too.
    LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  fi

  # Delete the storage pool
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete pool1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -q pool1 || false

  if [ "${poolDriver}" != "ceph" ]; then
    # Create a volume on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create data web
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume list data | grep web | grep -q node1
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume list data | grep web | grep -q node1

    # Since the volume name is unique to node1, it's possible to show, rename,
    # get the volume without specifying the --target parameter.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show data web | grep -q "location: node1"
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

    # Specifying the --target parameter shows, renames and deletes the
    # proper volume.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show --target node1 data web | grep -q "location: node1"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show --target node2 data web | grep -q "location: node2"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node1 data web webbaz
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node2 data web webbaz
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete --target node2 data webbaz

    # Since now there's only one volume in the pool left named webbaz,
    # it's possible to delete it without specifying --target.
    LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete data webbaz
  fi

  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_TWO_DIR}" lxc storage delete data

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

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

  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep status: | grep -q Created

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

  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
}

test_clustering_network() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # The state of the preseeded network shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network list | grep "${bridge}" | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Create a project with restricted.networks.subnets set to check the default networks are created before projects
  # when a member joins the cluster.
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${bridge}" ipv4.routes=192.0.2.0/24
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo \
    -c restricted=true \
    -c features.networks=true \
    -c restricted.networks.subnets="${bridge}":192.0.2.0/24

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # The state of the preseeded network is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network list| grep "${bridge}" | grep -q CREATED

  # Check both nodes show network created.
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${bridge}' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${bridge}' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Trying to pass config values other than
  # 'bridge.external_interfaces' results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create foo ipv4.address=auto --target node1 || false

  net="${bridge}x"

  # Define networks on the two nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_TWO_DIR}" lxc network show  "${net}" | grep -q node1
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network show "${net}" | grep -q node2 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep status: | grep -q Pending

  # A container can't be created when its NIC is associated with a pending network.
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -n "${net}" testimage bar || false

  # The bridge.external_interfaces config key is not legal for the final network creation
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" bridge.external_interfaces=foo || false

  # Create the network
  LXD_DIR="${LXD_TWO_DIR}" lxc network create "${net}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep status: | grep -q Created
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" --target node2 | grep status: | grep -q Created

  # FIXME: rename the network is not supported with clustering
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network rename "${net}" "${net}-foo" || false

  # Delete the networks
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${net}"
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  LXD_PID1="$(LXD_DIR="${LXD_ONE_DIR}" lxc query /1.0 | jq .environment.server_pid)"
  LXD_PID2="$(LXD_DIR="${LXD_TWO_DIR}" lxc query /1.0 | jq .environment.server_pid)"

  # Test network create partial failures.
  nsenter -n -t "${LXD_PID1}" -- ip link add "${net}" type dummy # Create dummy interface to conflict with network.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep status: | grep -q Pending # Check has pending status.

  # Run network create on other node1 (expect this to fail early due to existing interface).
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep status: | grep -q Errored # Check has errored status.

  # Check each node status (expect both node1 and node2 to be pending as local member running created failed first).
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'" | grep "| node2 | 0     |"

  # Run network create on other node2 (still excpect to fail on node1, but expect node2 create to succeed).
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network create "${net}" || false

  # Check each node status (expect node1 to be pending and node2 to be created).
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Check interfaces are expected types (dummy on node1 and bridge on node2).
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep dummy
  nsenter -n -t "${LXD_PID2}" -- ip -details link show "${net}" | grep bridge

  # Check we cannot update network global config while in pending state on either node.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network set "${net}" ipv4.dhcp false || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network set "${net}" ipv4.dhcp false || false

  # Check we can update node-specific config on the node that has been created (and that it is applied).
  nsenter -n -t "${LXD_PID2}" -- ip link add "ext-${net}" type dummy # Create dummy interface to add to bridge.
  LXD_DIR="${LXD_TWO_DIR}" lxc network set "${net}" bridge.external_interfaces "ext-${net}" --target node2
  nsenter -n -t "${LXD_PID2}" -- ip link show "ext-${net}" | grep "master ${net}"

  # Check we can update node-specific config on the node that hasn't been created (and that only DB is updated).
  nsenter -n -t "${LXD_PID1}" -- ip link add "ext-${net}" type dummy # Create dummy interface to add to bridge.
  nsenter -n -t "${LXD_PID1}" -- ip address add 192.0.2.1/32 dev "ext-${net}" # Add address to prevent attach.
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${net}" bridge.external_interfaces "ext-${net}" --target node1
  ! nsenter -n -t "${LXD_PID1}" -- ip link show "ext-${net}" | grep "master ${net}" || false  # Don't expect to be attached.

  # Delete partially created network and check nodes that were created are cleaned up.
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net}"
  ! nsenter -n -t "${LXD_PID2}" -- ip link show "${net}" || false # Check bridge is removed.
  nsenter -n -t "${LXD_PID2}" -- ip link show "ext-${net}" # Check external interface still exists.
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep dummy # Check node1 conflict still exists.

  # Create new partially created network and check we can fix it.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.address=192.0.2.1/24 ipv6.address=2001:db8::1/64|| false
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep status: | grep -q Errored # Check has errored status.
  nsenter -n -t "${LXD_PID1}" -- ip link delete "${net}" # Remove conflicting interface.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.dhcp=false || false # Check supplying global config on re-create is blocked.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" # Check re-create succeeds.
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep status: | grep -q Created # Check is created after fix.
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep bridge # Check bridge exists.
  nsenter -n -t "${LXD_PID2}" -- ip -details link show "${net}" | grep bridge # Check bridge exists.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" || false # Check re-create is blocked after success.

  # Check both nodes marked created.
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Check instance can be connected to created network and assign static DHCP allocations.
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}"
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 -n "${net}" testimage c1
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c1 eth0 ipv4.address=192.0.2.2

  # Check cannot assign static IPv6 without stateful DHCPv6 enabled.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c1 eth0 ipv6.address=2001:db8::2 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${net}" ipv6.dhcp.stateful=true
  LXD_DIR="${LXD_ONE_DIR}" lxc config device set c1 eth0 ipv6.address=2001:db8::2

  # Check duplicate static DHCP allocation detection is working for same server as c1.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 -n "${net}" testimage c2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c2 eth0 ipv4.address=192.0.2.2 || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config device set c2 eth0 ipv6.address=2001:db8::2 || false

  # Check duplicate static DHCP allocation is allowed for instance on a different server.
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -n "${net}" testimage c3
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

  # Cleanup instances and image.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1 c2 c3
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage

  # Delete network.
  LXD_DIR="${LXD_ONE_DIR}" lxc network delete "${net}"
  ! nsenter -n -t "${LXD_PID1}" -- ip link show "${net}" || false # Check bridge is removed.
  ! nsenter -n -t "${LXD_PID2}" -- ip link show "${net}" || false # Check bridge is removed.

  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

# Perform an upgrade of a 2-member cluster, then a join a third member and
# perform one more upgrade
test_clustering_upgrade() {
  # shellcheck disable=2039,3043
  local LXD_DIR LXD_NETNS

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # First, test the upgrade with a 2-node cluster
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Respawn the second node, making it believe it has an higher
  # version than it actually has.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=1
  shutdown_lxd "${LXD_TWO_DIR}"
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false

  # The second daemon is blocked waiting for the other to be upgraded
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5 || false

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "message: Fully operational"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "message: waiting for other nodes to be upgraded"

  # Respawn the first node, so it matches the version the second node
  # believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The second daemon has now unblocked
  LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=30

  # The cluster is again operational
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q "OFFLINE" || false

  # Now spawn a third node and test the upgrade with a 3-node cluster.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Respawn the second node, making it believe it has an higher
  # version than it actually has.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=2
  shutdown_lxd "${LXD_TWO_DIR}"
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false

  # The second daemon is blocked waiting for the other two to be
  # upgraded
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5 || false

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "message: Fully operational"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "message: waiting for other nodes to be upgraded"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node3 | grep -q "message: Fully operational"

  # Respawn the first node and third node, so they match the version
  # the second node believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" false
  shutdown_lxd "${LXD_THREE_DIR}"
  LXD_NETNS="${ns3}" respawn_lxd "${LXD_THREE_DIR}" true

  # The cluster is again operational
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q "OFFLINE" || false

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR LXD_NETNS N

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  LXD_CLUSTER_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  N=8

  setup_clustering_netns 1
  LXD_ONE_DIR="${LXD_CLUSTER_DIR}/1"
  mkdir -p "${LXD_ONE_DIR}"
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  for i in $(seq 2 "${N}"); do
    setup_clustering_netns "${i}"
    LXD_ITH_DIR="${LXD_CLUSTER_DIR}/${i}"
    mkdir -p "${LXD_ITH_DIR}"
    chmod +x "${LXD_ITH_DIR}"
    nsi="${prefix}${i}"
    spawn_lxd_and_join_cluster "${nsi}" "${bridge}" "${cert}" "${i}" 1 "${LXD_ITH_DIR}"
  done

  # Respawn all nodes in sequence, as if their version had been upgrade.
  export LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS=1
  for i in $(seq "${N}" -1 1); do
    shutdown_lxd "${LXD_CLUSTER_DIR}/${i}"
    LXD_NETNS="${prefix}${i}" respawn_lxd "${LXD_CLUSTER_DIR}/${i}" false
  done

  LXD_DIR="${LXD_ONE_DIR}" lxd waitready --timeout=10
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q "OFFLINE" || false

  for i in $(seq "${N}" -1 1); do
    LXD_DIR="${LXD_CLUSTER_DIR}/${i}" lxd shutdown
  done
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Give LXD a couple of seconds to get event API connected properly
  sleep 2

  # Init a container on node2, using a client connected to node1
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage foo

  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo --alias=foo-image
  LXD_DIR="${LXD_ONE_DIR}" lxc image show foo-image | grep -q "public: false"
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete foo-image

  LXD_DIR="${LXD_TWO_DIR}" lxc snapshot foo backup
  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo/backup --alias=foo-backup-image
  LXD_DIR="${LXD_ONE_DIR}" lxc image show foo-backup-image | grep -q "public: false"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_profiles() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Create an empty profile.
  LXD_DIR="${LXD_TWO_DIR}" lxc profile create web

  # Launch two containers on the two nodes, using the above profile.
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  # TODO: Fix known race in importing small images that complete before event listener is setup.
  sleep 2
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 -p default -p web testimage c1
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node2 -p default -p web testimage c2

  # Edit the profile.
  source=$(mktemp -d -p "${TEST_DIR}" XXX)
  touch "${source}/hello"
  chmod 755 "${source}"
  chmod 644 "${source}/hello"
  (
    cat <<EOF
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
  ) | LXD_DIR="${LXD_TWO_DIR}" lxc profile edit web

  LXD_DIR="${LXD_TWO_DIR}" lxc exec c1 ls /mnt | grep -q hello
  LXD_DIR="${LXD_TWO_DIR}" lxc exec c2 ls /mnt | grep -q hello

  LXD_DIR="${LXD_TWO_DIR}" lxc stop c1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc stop c2 --force

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_update_cert() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${LXD_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_lxd "${LXD_ONE_DIR}"

  # Set up again
  setup_clustering_bridge

  # Bootstrap the first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Send update request
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster update-cert "${cert_path}" "${key_path}" -q

  cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  cmp -s "${LXD_TWO_DIR}/cluster.crt" "${cert_path}" || false

  cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false
  cmp -s "${LXD_TWO_DIR}/cluster.key" "${key_path}" || false

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -q "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -q "server_name: node1"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_update_cert_reversion() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${LXD_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_lxd "${LXD_ONE_DIR}"

  # Set up again
  setup_clustering_bridge

  # Bootstrap the first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Shutdown third node
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_THREE_DIR}/unix.socket"
  kill_lxd "${LXD_THREE_DIR}"

  # Send update request
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster update-cert "${cert_path}" "${key_path}" -q || false

  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_TWO_DIR}/cluster.crt" "${cert_path}" || false

  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false
  ! cmp -s "${LXD_TWO_DIR}/cluster.key" "${key_path}" || false

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -q "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -q "server_name: node1"

  LXD_DIR="${LXD_ONE_DIR}" lxc warning list | grep -q "Unable to update cluster certificate"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_join_api() {
  # shellcheck disable=2039,2034,3043
  local LXD_DIR LXD_NETNS

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  LXD_NETNS="${ns2}" spawn_lxd "${LXD_TWO_DIR}" false

  op=$(curl --unix-socket "${LXD_TWO_DIR}/unix.socket" -X PUT "lxd/1.0/cluster" -d "{\"server_name\":\"node2\",\"enabled\":true,\"member_config\":[{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"source\",\"value\":\"\"}],\"server_address\":\"10.1.1.102:8443\",\"cluster_address\":\"10.1.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_password\":\"sekret\"}" | jq -r .operation)
  curl --unix-socket "${LXD_TWO_DIR}/unix.socket" "lxd${op}/wait"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "message: Fully operational"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_ONE_DIR}"
}

test_clustering_shutdown_nodes() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Init a container on node1, using a client connected to node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 testimage foo

  # Get container PID
  instance_pid=$(LXD_DIR="${LXD_ONE_DIR}" lxc info foo | awk '/^PID:/ {print $2}')

  # Get server PIDs
  daemon_pid1=$(LXD_DIR="${LXD_ONE_DIR}" lxc info | awk '/server_pid/{print $2}')
  daemon_pid2=$(LXD_DIR="${LXD_TWO_DIR}" lxc info | awk '/server_pid/{print $2}')
  daemon_pid3=$(LXD_DIR="${LXD_THREE_DIR}" lxc info | awk '/server_pid/{print $2}')

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  wait "${daemon_pid2}"

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  wait "${daemon_pid3}"

  # Wait for raft election to take place and become aware that quorum has been lost (should take 3-6s).
  sleep 10

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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Create a test project
  LXD_DIR="${LXD_ONE_DIR}" lxc project create p1
  LXD_DIR="${LXD_ONE_DIR}" lxc project switch p1
  LXD_DIR="${LXD_ONE_DIR}" lxc profile device add default root disk path="/" pool="data"

  # Create a container in the project.
  LXD_DIR="${LXD_ONE_DIR}" deps/import-busybox --project p1 --alias testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage c1

  # The container is visible through both nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep -q c1
  LXD_DIR="${LXD_TWO_DIR}" lxc list | grep -q c1

  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1

  # Remove the image file and DB record from node1.
  rm "${LXD_ONE_DIR}"/images/*
  LXD_DIR="${LXD_TWO_DIR}" lxd sql global 'delete from images_nodes where node_id = 1'

  # Check image import from node2 by creating container on node1 in other project.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 testimage c2 --project p1
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c2 --project p1

  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage

  LXD_DIR="${LXD_ONE_DIR}" lxc project switch default

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_address() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"

  # Bootstrap the first node using a custom cluster port
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "dir" "8444"

  # The bootstrap node appears in the list with its cluster-specific port
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q 8444
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "database: true"

  # Add a remote using the core.https_address of the bootstrap node, and check
  # that the REST API is exposed.
  url="https://10.1.1.101:8443"
  lxc remote add cluster --password sekret --accept-certificate "${url}"
  lxc storage list cluster: | grep -q data

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node using a custom cluster port
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "dir" "8444"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q node2
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node2 | grep -q "database: true"

  # The new node appears with its custom cluster port
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep ^url | grep -q 8444

  # The core.https_address config value can be changed and the REST API is still
  # accessible.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set "core.https_address" 10.1.1.101:9999
  url="https://10.1.1.101:9999"
  lxc remote set-url cluster "${url}"
  lxc storage list cluster:| grep -q data

  # The cluster.https_address config value can't be changed.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config set "cluster.https_address" "10.1.1.101:8448" || false

  # Create a container using the REST API exposed over core.https_address.
  LXD_DIR="${LXD_ONE_DIR}" deps/import-busybox --alias testimage
  lxc init --target node2 testimage cluster:c1
  lxc list cluster: | grep -q c1

  # The core.https_address config value can be set to a wildcard address if
  # the port is the same as cluster.https_address.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set "core.https_address" "0.0.0.0:8444"

  LXD_DIR="${LXD_TWO_DIR}" lxc delete c1

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  lxc remote remove cluster

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_image_replication() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Image replication will be performed across all nodes in the cluster by default
  images_minimal_replica1=$(LXD_DIR="${LXD_ONE_DIR}" lxc config get cluster.images_minimal_replica)
  images_minimal_replica2=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get cluster.images_minimal_replica)
  [ "$images_minimal_replica1" = "" ] || false
  [ "$images_minimal_replica2" = "" ] || false

  # Import the test image on node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  # The image is visible through both nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc image list | grep -q testimage
  LXD_DIR="${LXD_TWO_DIR}" lxc image list | grep -q testimage

  # The image tarball is available on both nodes
  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

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
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Import the test image on node3
  LXD_DIR="${LXD_THREE_DIR}" ensure_import_testimage

  # The image is visible through all three nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc image list | grep -q testimage
  LXD_DIR="${LXD_TWO_DIR}" lxc image list | grep -q testimage
  LXD_DIR="${LXD_THREE_DIR}" lxc image list | grep -q testimage

  # The image tarball is available on all three nodes
  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Import the image from the container
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  lxc launch testimage c1

  # Modify the container's rootfs and create a new image from the container
  lxc exec c1 -- touch /a
  lxc stop c1 --force
  lxc publish c1 --alias new-image

  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info new-image | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete new-image
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the container
  lxc delete c1

  # Delete the imported image
  fingerprint=$(LXD_DIR="${LXD_ONE_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Disable the image replication
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.images_minimal_replica 1
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -q 'cluster.images_minimal_replica: "1"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -q 'cluster.images_minimal_replica: "1"'
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -q 'cluster.images_minimal_replica: "1"'

  # Import the test image on node2
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage

  # The image is visible through all three nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc image list | grep -q testimage
  LXD_DIR="${LXD_TWO_DIR}" lxc image list | grep -q testimage
  LXD_DIR="${LXD_THREE_DIR}" lxc image list | grep -q testimage

  # The image tarball is only available on node2
  fingerprint=$(LXD_DIR="${LXD_TWO_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete testimage
  [ ! -f "${LXD_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${LXD_THREE_DIR}/images/${fingerprint}" ] || false

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  # Because we do not want tests to only run on Ubuntu (due to cluster's fan network dependency)
  # instead we will just spawn forkdns directly and check DNS resolution.

  # shellcheck disable=SC2031
  lxdDir="${LXD_DIR}"
  prefix="lxd$$"
  ipRand=$(shuf -i 0-9 -n 1)

  # Create first dummy interface for forkdns
  ip link add "${prefix}1" type dummy
  ip link set "${prefix}1" up
  ip a add 127.0.1.1"${ipRand}"/32 dev "${prefix}1"

  # Create forkdns config directory
  mkdir "${lxdDir}"/networks/lxdtest1/forkdns.servers -p

  # Launch forkdns (we expect syslog error about missing servers.conf file)
  lxd forkdns 127.0.1.1"${ipRand}":1053 lxd lxdtest1 &
  forkdns_pid1=$!

  # Create first dummy interface for forkdns
  ip link add "${prefix}2" type dummy
  ip link set "${prefix}2" up
  ip a add 127.0.1.2"${ipRand}"/32 dev "${prefix}2"

  # Create forkdns config directory
  mkdir "${lxdDir}"/networks/lxdtest2/forkdns.servers -p

  # Launch forkdns (we expect syslog error about missing servers.conf file)
  lxd forkdns 127.0.1.2"${ipRand}":1053 lxd lxdtest2 &
  forkdns_pid2=$!

  # Let the processes come up
  sleep 1

  # Create servers list file for forkdns1 pointing at forkdns2 (should be live reloaded)
  echo "127.0.1.2${ipRand}" > "${lxdDir}"/networks/lxdtest1/forkdns.servers/servers.conf.tmp
  mv "${lxdDir}"/networks/lxdtest1/forkdns.servers/servers.conf.tmp "${lxdDir}"/networks/lxdtest1/forkdns.servers/servers.conf

  # Create fake DHCP lease file on forkdns2 network
  echo "$(date +%s) 00:16:3e:98:05:40 10.140.78.145 test1 ff:2b:a8:0a:df:00:02:00:00:ab:11:36:ea:11:e5:37:e0:85:45" > "${lxdDir}"/networks/lxdtest2/dnsmasq.leases

  # Test querying forkdns1 for A record that is on forkdns2 network
  if ! dig @127.0.1.1"${ipRand}" -p1053 test1.lxd | grep "10.140.78.145" ; then
    echo "test1.lxd A DNS resolution failed"
    false
  fi

  # Test querying forkdns1 for AAAA record when equivalent A record is on forkdns2 network
  if ! dig @127.0.1.1"${ipRand}" -p1053 AAAA test1.lxd | grep "status: NOERROR" ; then
    echo "test1.lxd empty AAAAA DNS resolution failed"
    false
  fi

  # Test querying forkdns1 for PTR record that is on forkdns2 network
  if ! dig @127.0.1.1"${ipRand}" -p1053 -x 10.140.78.145 | grep "test1.lxd" ; then
    echo "10.140.78.145 PTR DNS resolution failed"
    false
  fi

  # Test querying forkdns1 for A record that is on forkdns2 network with recursion disabled to
  # ensure request isn't relayed
  if ! dig @127.0.1.1"${ipRand}" -p1053 +norecurse test1.lxd | grep "NXDOMAIN" ; then
    echo "test1.lxd A norecurse didnt return NXDOMAIN"
    false
  fi

  # Test querying forkdns1 for PTR record that is on forkdns2 network with recursion disabled to
  # ensure request isn't relayed
  if ! dig @127.0.1.1"${ipRand}" -p1053 +norecurse -x 10.140.78.145 | grep "NXDOMAIN" ; then
    echo "10.140.78.145 PTR norecurse didnt return NXDOMAIN"
    false
  fi

  # Cleanup
  kill -9 "${forkdns_pid1}"
  kill -9 "${forkdns_pid2}"
  ip link delete "${prefix}1"
  ip link delete "${prefix}2"
}

test_clustering_recover() {
  # shellcheck disable=2039,2034,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check the current database nodes
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -q "10.1.1.101:8443"
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -q "10.1.1.102:8443"
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -q "10.1.1.103:8443"

  # Create a test project, just to insert something in the database.
  LXD_DIR="${LXD_ONE_DIR}" lxc project create p1

  # Trying to recover a running daemon results in an error.
  ! LXD_DIR="${LXD_ONE_DIR}" lxd cluster recover-from-quorum-loss || false

  # Shutdown all nodes.
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5

  # Now recover the first node and restart it.
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster recover-from-quorum-loss -q
  respawn_lxd_cluster_member "${ns1}" "${LXD_ONE_DIR}"

  # The project we had created is still there
  LXD_DIR="${LXD_ONE_DIR}" lxc project list | grep -q p1

  # The database nodes have been updated
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -q "10.1.1.101:8443"
  ! LXD_DIR="${LXD_ONE_DIR}" lxd cluster list-database | grep -q "10.1.1.102:8443" || false

  # Cleanup the dead node.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node2 --force --yes
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --force --yes

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

# When a voter cluster member is shutdown, its role gets transferred to a spare
# node.
test_clustering_handover() {
  # shellcheck disable=2039,2034,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  echo "Launched member 1"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  echo "Launched member 2"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  echo "Launched member 3"

  # Spawn a fourth node, this will be a non-voter, stand-by node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  echo "Launched member 4"

  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -Fc "database-standby" | grep -Fx 1

  # Shutdown the first node.
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  echo "Stopped member 1"

  # The fourth node has been promoted, while the first one demoted.
  LXD_DIR="${LXD_THREE_DIR}" lxd sql local 'select * from raft_nodes'
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster ls
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node4
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node1
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node4 | grep -q "\- database$"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node1 | grep -q "database: false"

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
  sleep 0.5
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
  # shellcheck disable=2039,2034,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Spawn a fourth node
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check there is one database-standby member.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -Fc "database-standby" | grep -Fx 1

  # Kill the second node.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11
  kill -9 "$(cat "${LXD_TWO_DIR}/lxd.pid")"

  # Wait for the second node to be considered offline and be replaced by the
  # fourth node.
  sleep 15

  # The second node is offline and has been demoted.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "status: Offline"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "database: false"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -q "status: Online"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -q "\- database$"

  # Respawn the second node. It won't be able to disrupt the current leader,
  # since dqlite uses pre-vote.
  respawn_lxd_cluster_member "${ns2}" "${LXD_TWO_DIR}"
  sleep 12

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "status: Online"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "database: true"

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  sleep 0.5
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

# Recover a cluster where a raft node was removed from the nodes table but not
# from the raft configuration.
test_clustering_remove_raft_node() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -q 'cluster.offline_threshold: "11"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -q 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(cat "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(cat "${TEST_DIR}/ns/${ns2}/PID")"
  nsenter -m -n -t "${ns1_pid}" -- ip link show "${bridge}" > /dev/null
  nsenter -m -n -t "${ns2_pid}" -- ip link show "${bridge}" > /dev/null

  # Create a pending network and pool, to show that they are not
  # considered when checking if the joining node has all the required
  # networks and pools.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc network create net1 --target node2

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}"

  # Spawn a fourth node, this will be a database-standby node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list

  # Kill the second node, to prevent it from transferring its database role at shutdown.
  kill -9 "$(cat "${LXD_TWO_DIR}/lxd.pid")"

  # Remove the second node from the database but not from the raft configuration.
  retries=10
  while [ "${retries}" != "0" ]; do
    LXD_DIR="${LXD_ONE_DIR}" lxd sql global "DELETE FROM nodes WHERE address = '10.1.1.102:8443'" && break
    sleep 0.5
    retries=$((retries-1))
  done

  if [ "${retries}" -eq 0 ]; then
      echo "Failed to remove node from database"
      return 1
  fi

  # Let the heartbeats catch up.
  sleep 12

  # The node does not appear anymore in the cluster list.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q "node2" || false

  # There are only 2 database nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "\- database-leader$"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node3 | grep -q "\- database$"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -q "\- database$"

  # The second node is still in the raft_nodes table.
  LXD_DIR="${LXD_ONE_DIR}" lxd sql local "SELECT * FROM raft_nodes" | grep -q "10.1.1.102"

  # Force removing the raft node.
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster remove-raft-node -q "10.1.1.102"

  # Wait for a heartbeat to propagate and a rebalance to be performed.
  sleep 12

  # We're back to 3 database nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "\- database-leader$"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node3 | grep -q "\- database$"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -q "\- database$"

  # The second node is gone from the raft_nodes_table.
  ! LXD_DIR="${LXD_ONE_DIR}" lxd sql local "SELECT * FROM raft_nodes" | grep -q "10.1.1.102" || false

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 4 "${LXD_FIVE_DIR}"

  # Spawn a sixth node, using non-database node4 as join target.
  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_SIX_DIR}"
  ns6="${prefix}6"
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 4 "${LXD_SIX_DIR}"

  # Default failure domain
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "failure_domain: default"

  # Set failure domains

  # shellcheck disable=SC2039
  printf "roles: [\"database\"]\nfailure_domain: \"az1\"\ngroups: [\"default\"]" | LXD_DIR="${LXD_THREE_DIR}" lxc cluster edit node1
  # shellcheck disable=SC2039
  printf "roles: [\"database\"]\nfailure_domain: \"az2\"\ngroups: [\"default\"]" | LXD_DIR="${LXD_THREE_DIR}" lxc cluster edit node2
  # shellcheck disable=SC2039
  printf "roles: [\"database\"]\nfailure_domain: \"az3\"\ngroups: [\"default\"]" | LXD_DIR="${LXD_THREE_DIR}" lxc cluster edit node3
  # shellcheck disable=SC2039
  printf "roles: []\nfailure_domain: \"az1\"\ngroups: [\"default\"]" | LXD_DIR="${LXD_THREE_DIR}" lxc cluster edit node4
  # shellcheck disable=SC2039
  printf "roles: []\nfailure_domain: \"az2\"\ngroups: [\"default\"]" | LXD_DIR="${LXD_THREE_DIR}" lxc cluster edit node5
  # shellcheck disable=SC2039
  printf "roles: []\nfailure_domain: \"az3\"\ngroups: [\"default\"]" | LXD_DIR="${LXD_THREE_DIR}" lxc cluster edit node6

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "failure_domain: az2"

  # Shutdown a node in az2, its replacement is picked from az2.
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 3

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "database: false"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node5 | grep -q "database: true"

  LXD_DIR="${LXD_SIX_DIR}" lxd shutdown
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  # Spawn first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.images_minimal_replica 1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set images.auto_update_interval 1

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${poolDriver}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${poolDriver}"

  # Spawn public node which has a public testimage
  setup_clustering_netns 4
  LXD_REMOTE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_REMOTE_DIR}"
  ns4="${prefix}4"

  LXD_NETNS="${ns4}" spawn_lxd "${LXD_REMOTE_DIR}" false
  dir_configure "${LXD_REMOTE_DIR}"
  LXD_DIR="${LXD_REMOTE_DIR}" deps/import-busybox --alias testimage --public

  LXD_DIR="${LXD_REMOTE_DIR}" lxc config set core.https_address "10.1.1.104:8443"

  # Add remotes
  lxc remote add public "https://10.1.1.104:8443" --accept-certificate --password foo --public
  lxc remote add cluster "https://10.1.1.101:8443" --accept-certificate --password sekret

  LXD_DIR="${LXD_REMOTE_DIR}" lxc init testimage c1

  # Create additional projects
  LXD_DIR="${LXD_ONE_DIR}" lxc project create foo
  LXD_DIR="${LXD_ONE_DIR}" lxc project create bar

  # Copy default profile to all projects (this includes the root disk)
  LXD_DIR="${LXD_ONE_DIR}" lxc profile show default | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default --project foo
  LXD_DIR="${LXD_ONE_DIR}" lxc profile show default | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default --project bar

  for project in default foo bar; do
    # Copy the public image to each project
    LXD_DIR="${LXD_ONE_DIR}" lxc image copy public:testimage local: --alias testimage --project "${project}"

    # Diable autoupdate for testimage in project foo
    if [ "${project}" = "foo" ]; then
      auto_update=false
    else
      auto_update=true
    fi

    LXD_DIR="${LXD_ONE_DIR}" lxc image show testimage --project "${project}" | sed -r "s/auto_update: .*/auto_update: ${auto_update}/g" | LXD_DIR="${LXD_ONE_DIR}" lxc image edit testimage --project "${project}"

    # Create a container in each project
    LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 --project "${project}"
  done

  # Modify public testimage
  old_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image ls testimage -c f --format csv)"
  dd if=/dev/urandom count=32 | LXD_DIR="${LXD_REMOTE_DIR}" lxc file push - c1/foo
  LXD_DIR="${LXD_REMOTE_DIR}" lxc publish c1 --alias testimage --reuse --public
  new_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image ls testimage -c f --format csv)"

  pids=""

  if [ "${poolDriver}" != "dir" ]; then
    # Check image storage volume records exist.
    lxd sql global 'select name from storage_volumes'
    if [ "${poolDriver}" = "ceph" ]; then
      lxd sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 1
    else
      lxd sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 3
    fi
  fi

  # Trigger image refresh on all nodes
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    LXD_DIR="${lxd_dir}" lxc query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    wait "${pid}"
  done

  if [ "${poolDriver}" != "dir" ]; then
    lxd sql global 'select name from storage_volumes'
    # Check image storage volume records actually removed from relevant members and replaced with new fingerprint.
    if [ "${poolDriver}" = "ceph" ]; then
      lxd sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 0
      lxd sql global 'select name from storage_volumes' | grep -Fc "${new_fingerprint}" | grep -Fx 1
    else
      lxd sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 1
      lxd sql global 'select name from storage_volumes' | grep -Fc "${new_fingerprint}" | grep -Fx 2
    fi
  fi

  # The projects default and bar should have received the new image
  # while project foo should still have the old image.
  # Also, it should only show 1 entry for the old image and 2 entries
  # for the new one.
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="foo"' | grep "${old_fingerprint}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images' | grep -c "${old_fingerprint}")" -eq 1 ] || false

  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="default"' | grep "${new_fingerprint}"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="bar"' | grep "${new_fingerprint}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images' | grep -c "${new_fingerprint}")" -eq 2 ] || false

  pids=""

  # Trigger image refresh on all nodes. This shouldn't do anything as the image
  # is already up-to-date.
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    LXD_DIR="${lxd_dir}" lxc query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    wait "${pid}"
  done

  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="foo"' | grep "${old_fingerprint}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images' | grep -c "${old_fingerprint}")" -eq 1 ] || false

  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="default"' | grep "${new_fingerprint}"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="bar"' | grep "${new_fingerprint}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images' | grep -c "${new_fingerprint}")" -eq 2 ] || false

  # Modify public testimage
  dd if=/dev/urandom count=32 | LXD_DIR="${LXD_REMOTE_DIR}" lxc file push - c1/foo
  LXD_DIR="${LXD_REMOTE_DIR}" lxc publish c1 --alias testimage --reuse --public
  new_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image ls testimage -c f --format csv)"

  pids=""

  # Trigger image refresh on all nodes
  for lxd_dir in "${LXD_ONE_DIR}" "${LXD_TWO_DIR}" "${LXD_THREE_DIR}"; do
    LXD_DIR="${lxd_dir}" lxc query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    wait "${pid}"
  done

  pids=""

  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="foo"' | grep "${old_fingerprint}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images' | grep -c "${old_fingerprint}")" -eq 1 ] || false

  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="default"' | grep "${new_fingerprint}"
  LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="bar"' | grep "${new_fingerprint}"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global 'select images.fingerprint from images' | grep -c "${new_fingerprint}")" -eq 2 ] || false

  # Clean up everything
  for project in default foo bar; do
    # shellcheck disable=SC2046
    LXD_DIR="${LXD_ONE_DIR}" lxc image rm --project "${project}" $(LXD_DIR="${LXD_ONE_DIR}" lxc image ls --format csv --project "${project}" | cut -d, -f2)
    # shellcheck disable=SC2046
    LXD_DIR="${LXD_ONE_DIR}" lxc rm --project "${project}" $(LXD_DIR="${LXD_ONE_DIR}" lxc ls --format csv --project "${project}" | cut -d, -f1)
  done

  # shellcheck disable=SC2046
  LXD_DIR="${LXD_REMOTE_DIR}" lxc image rm $(LXD_DIR="${LXD_REMOTE_DIR}" lxc image ls --format csv | cut -d, -f2)
  # shellcheck disable=SC2046
  LXD_DIR="${LXD_REMOTE_DIR}" lxc rm $(LXD_DIR="${LXD_REMOTE_DIR}" lxc ls --format csv | cut -d, -f1)

  LXD_DIR="${LXD_ONE_DIR}" lxc project delete foo
  LXD_DIR="${LXD_ONE_DIR}" lxc project delete bar
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_REMOTE_DIR}" lxd shutdown
  sleep 0.5
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  # Spawn first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${poolDriver}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${poolDriver}"

  # Create local pool
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir

  # Create local storage volume
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create pool1 vol1

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c1 boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c2 --target=node1 -c cluster.evacuate=auto -s pool1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c2 boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c3 --target=node1 -c cluster.evacuate=stop
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c3 boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c4 --target=node1 -c cluster.evacuate=migrate -s pool1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c4 boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c5 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c5 boot.host_shutdown_timeout=1

  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c6 --target=node2
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c6 boot.host_shutdown_timeout=1

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list

  # Evacuate first node
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster evacuate node1 --force

  # Ensure the node is evacuated
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -q "status: Evacuated"

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list

  # Check instance status
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -q "Status: RUNNING"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -q "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -q "Status: RUNNING"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -q "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -q "Status: STOPPED"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -q "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -q "Status: RUNNING"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -q "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -q "Status: STOPPED"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -q "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -q "Location: node2"

  # Ensure instances cannot be created on the evacuated node
  ! LXD_DIR="${LXD_TWO_DIR}" lxc launch testimage c7 --target=node1 || false

  # Restore first node
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster restore node1 --force

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list

  # Ensure the instances were moved back to the origin
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -q "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -q "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -q "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -q "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -q "Status: STOPPED"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -q "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -q "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -q "Location: node2"

  # Clean up
  LXD_DIR="${LXD_TWO_DIR}" lxc rm -f c1
  LXD_DIR="${LXD_TWO_DIR}" lxc rm -f c2
  LXD_DIR="${LXD_TWO_DIR}" lxc rm -f c3
  LXD_DIR="${LXD_TWO_DIR}" lxc rm -f c4
  LXD_DIR="${LXD_TWO_DIR}" lxc rm -f c5
  LXD_DIR="${LXD_TWO_DIR}" lxc rm -f c6
  LXD_DIR="${LXD_TWO_DIR}" lxc image rm testimage

  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  # Shut down cluster
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 0.5
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

test_clustering_edit_configuration() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap the first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn 6 nodes in total for role coverage.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${LXD_FIVE_DIR}"

  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_SIX_DIR}"
  ns6="${prefix}6"
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 1 "${LXD_SIX_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11

  # Ensure successful communication
  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -q "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_SIX_DIR}" lxc info --target node1 | grep -q "server_name: node1"

  # Shut down all nodes, de-syncing the roles tables
  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"
  shutdown_lxd "${LXD_THREE_DIR}"
  shutdown_lxd "${LXD_FOUR_DIR}"

  # Force-kill the last two to prevent leadership loss.
  daemon_pid=$(cat "${LXD_FIVE_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(cat "${LXD_SIX_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true

  config=$(mktemp -p "${TEST_DIR}" XXX)
  # Update the cluster configuration with new port numbers
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster edit < "${config}"

  LXD_DIR="${LXD_TWO_DIR}" lxd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_TWO_DIR}" lxd cluster edit < "${config}"

  LXD_DIR="${LXD_THREE_DIR}" lxd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_THREE_DIR}" lxd cluster edit < "${config}"

  LXD_DIR="${LXD_FOUR_DIR}" lxd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_FOUR_DIR}" lxd cluster edit < "${config}"

  LXD_DIR="${LXD_FIVE_DIR}" lxd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_FIVE_DIR}" lxd cluster edit < "${config}"

  LXD_DIR="${LXD_SIX_DIR}" lxd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_SIX_DIR}" lxd cluster edit < "${config}"

  # Respawn the nodes
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" false
  LXD_NETNS="${ns2}" respawn_lxd "${LXD_TWO_DIR}" false
  LXD_NETNS="${ns3}" respawn_lxd "${LXD_THREE_DIR}" false
  LXD_NETNS="${ns4}" respawn_lxd "${LXD_FOUR_DIR}" false
  LXD_NETNS="${ns5}" respawn_lxd "${LXD_FIVE_DIR}" false
  # Only wait on the last node, because we don't know who the voters are
  LXD_NETNS="${ns6}" respawn_lxd "${LXD_SIX_DIR}" true

  # Let the heartbeats catch up
  sleep 12

  # Ensure successful communication
  LXD_DIR="${LXD_ONE_DIR}"   lxc info --target node2 | grep -q "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}"   lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_FOUR_DIR}"  lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_FIVE_DIR}"  lxc info --target node1 | grep -q "server_name: node1"
  LXD_DIR="${LXD_SIX_DIR}"   lxc info --target node1 | grep -q "server_name: node1"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster ls | grep -q "No heartbeat" || false

  # Clean up
  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"
  shutdown_lxd "${LXD_THREE_DIR}"
  shutdown_lxd "${LXD_FOUR_DIR}"

  # Force-kill the last two to prevent leadership loss.
  daemon_pid=$(cat "${LXD_FIVE_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(cat "${LXD_SIX_DIR}/lxd.pid")
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
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap the first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Ensure successful communication
  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -q "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -q "server_name: node1"

  # Remove the leader, via the stand-by node
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster rm node1

  # Ensure the remaining node is working
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -q "node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -q "node2"

  # Previous leader should no longer be clustered
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list || false

  # Spawn a third node, joining the cluster with node2
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}"

  # Ensure successful communication
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node3 | grep -q "server_name: node3"
  LXD_DIR="${LXD_THREE_DIR}" lxc info --target node2 | grep -q "server_name: node2"

  # Remove the third node, via itself
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster rm node3

  # Ensure only node2 is left in the cluster
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster ls | grep -q node2
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster ls -f csv | grep -qv node2 || false

  # Ensure clustering is disabled (no output) on node1 and node3
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster ls | grep -qv "." || false
  ! LXD_DIR="${LXD_THREE_DIR}" lxc cluster ls | grep -qv "." || false

  # Clean up
  daemon_pid1=$(cat "${LXD_ONE_DIR}/lxd.pid")
  shutdown_lxd "${LXD_ONE_DIR}"

  daemon_pid2=$(cat "${LXD_TWO_DIR}/lxd.pid")
  shutdown_lxd "${LXD_TWO_DIR}"

  daemon_pid3=$(cat "${LXD_THREE_DIR}/lxd.pid")
  shutdown_lxd "${LXD_THREE_DIR}"

  wait "${daemon_pid1}"
  wait "${daemon_pid2}"
  wait "${daemon_pid3}"

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}

test_clustering_autotarget() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

 # Use node1 for all cluster actions.
 LXD_DIR="${LXD_ONE_DIR}"

 # Spawn c1 on node2 from node1
 ensure_import_testimage
  lxc init --target node2 testimage c1
 lxc ls | grep c1 | grep -q node2

 # Set node1 config to disable autotarget
 lxc cluster set node1 scheduler.instance manual

 # Spawn another node, autotargeting node2 although it has more instances.
 lxc init testimage c2
 lxc ls | grep c2 | grep -q node2

  shutdown_lxd "${LXD_ONE_DIR}"
  shutdown_lxd "${LXD_TWO_DIR}"
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_groups() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  lxc remote add cluster --password sekret --accept-certificate "https://10.1.1.101:8443"

  # Initially, there is only the default group
  lxc cluster group show cluster:default
  [ "$(lxc query cluster:/1.0/cluster/groups | jq 'length')" -eq 1 ]

  # All nodes initially belong to the default group
  [ "$(lxc query cluster:/1.0/cluster/groups/default | jq '.members | length')" -eq 3 ]

  # Renaming the default group is not allowed
  ! lxc cluster group rename cluster:default foobar || false

  lxc cluster list cluster:
  # Nodes need to belong to at least one group, removing it from the default group should therefore fail
  ! lxc cluster group remove cluster:node1 default || false

  # Create new cluster group which should be empty
  lxc cluster group create cluster:foobar
  [ "$(lxc query cluster:/1.0/cluster/groups/foobar | jq '.members | length')" -eq 0 ]

  # Add second node to new group. Node2 will now belong to both groups.
  lxc cluster group assign cluster:node2 default,foobar
  [ "$(lxc query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "default"; .)')" = "true" ]
  [ "$(lxc query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "foobar"; .)')" = "true" ]

  # Deleting the "foobar" group should fail as it still has members
  ! lxc cluster group delete cluster:foobar || false

  # Since node2 now belongs to two groups, it can be removed from the default group
  lxc cluster group remove cluster:node2 default
  lxc query cluster:/1.0/cluster/members/node2

  [ "$(lxc query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "default"; .)')" = "false" ]
  [ "$(lxc query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "foobar"; .)')" = "true" ]

  # Rename group "foobar" to "blah"
  lxc cluster group rename cluster:foobar blah
  [ "$(lxc query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "blah"; .)')" = "true" ]

  lxc cluster group create cluster:foobar2
  lxc cluster group assign cluster:node3 default,foobar2

  # With these settings:
  # - node1 will receive instances unless a different node is directly targeted (not via group)
  # - node2 will receive instances if either targeted by group or directly
  # - node3 will only receive instances if targeted directly
  lxc cluster set cluster:node2 scheduler.instance=group
  lxc cluster set cluster:node3 scheduler.instance=manual

  ensure_import_testimage

  # Cluster group "foobar" doesn't exist and should therefore fail
  ! lxc init testimage cluster:c1 --target=@foobar || false

  # At this stage we have:
  # - node1 in group default accepting all instances
  # - node2 in group blah accepting group-only targeting
  # - node3 in group default accepting direct targeting only

  # c1 should go to node1
  lxc init testimage cluster:c1
  lxc info cluster:c1 | grep -q "Location: node1"

  # c2 should go to node2
  lxc init testimage cluster:c2 --target=@blah
  lxc info cluster:c2 | grep -q "Location: node2"

  # c3 should go to node2 again
  lxc init testimage cluster:c3 --target=@blah
  lxc info cluster:c3 | grep -q "Location: node2"

  # Direct targeting of node2 should work
  lxc init testimage cluster:c4 --target=node2
  lxc info cluster:c4 | grep -q "Location: node2"

  # Direct targeting of node3 should work
  lxc init testimage cluster:c5 --target=node3
  lxc info cluster:c5 | grep -q "Location: node3"

  # Clean up
  lxc rm -f c1 c2 c3 c4 c5

  # Restricted project tests
  lxc project create foo -c features.images=false -c restricted=true -c restricted.cluster.groups=blah
  lxc profile show default | lxc profile edit default --project foo

  # Check cannot create instance in restricted project that only allows blah group, when the only member that
  # exists in the blah group also has scheduler.instance=group set (so it must be targeted via group or directly).
  ! lxc init testimage cluster:c1 --project foo || false

  # Check cannot create instance in restricted project when targeting a member that isn't in the restricted
  # project's allowed cluster groups list.
  ! lxc init testimage cluster:c1 --project foo --target=node1 || false
  ! lxc init testimage cluster:c1 --project foo --target=@foobar2 || false

  # Check can create instance in restricted project when not targeting any specific member, but that it will only
  # be created on members within the project's allowed cluster groups list.
  lxc cluster unset cluster:node2 scheduler.instance
  lxc init testimage cluster:c1 --project foo
  lxc init testimage cluster:c2 --project foo
  lxc info cluster:c1 --project foo | grep -q "Location: node2"
  lxc info cluster:c2 --project foo | grep -q "Location: node2"
  lxc delete -f c1 c2 --project foo

  # Check can specify any member or group when restricted.cluster.groups is empty.
  lxc project unset foo restricted.cluster.groups
  lxc init testimage cluster:c1 --project foo --target=node1
  lxc info cluster:c1 --project foo | grep -q "Location: node1"

  lxc init testimage cluster:c2 --project foo --target=@blah
  lxc info cluster:c2 --project foo | grep -q "Location: node2"

  lxc delete -f c1 c2 --project foo

  lxc project delete foo

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"

  lxc remote rm cluster
}

test_clustering_events() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules...
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node.
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Spawn a third node.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}"

  # Spawn a fourth node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}"

  # Spawn a firth node.
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${LXD_FIVE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"

  ensure_import_testimage

  # c1 should go to node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc info c1 | grep -q "Location: node1"
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c2 --target=node2

  LXD_DIR="${LXD_ONE_DIR}" lxc monitor --type=lifecycle > "${TEST_DIR}/node1.log" &
  monitorNode1PID=$!
  LXD_DIR="${LXD_TWO_DIR}" lxc monitor --type=lifecycle > "${TEST_DIR}/node2.log" &
  monitorNode2PID=$!
  LXD_DIR="${LXD_THREE_DIR}" lxc monitor --type=lifecycle > "${TEST_DIR}/node3.log" &
  monitorNode3PID=$!

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 2
  done

  # Switch into event-hub mode.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hub
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster role add node2 event-hub
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -Fc event-hub | grep -Fx 2

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "cluster-member-updated" "${TEST_DIR}/node${i}.log" | grep -Fx 2
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

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 4
  done

  # Launch container on node3 to check image distribution events work during event-hub mode.
  LXD_DIR="${LXD_THREE_DIR}" lxc launch testimage c3 --target=node3

  for i in 1 2 3; do
    grep -Fc "instance-created" "${TEST_DIR}/node${i}.log" | grep -Fx 1
  done

  # Switch into full-mesh mode by removing one event-hub role so there is <2 in the cluster.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role remove node1 event-hub
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -Fc event-hub | grep -Fx 1

  sleep 1 # Wait for notification heartbeat to distribute new roles.
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "cluster-member-updated" "${TEST_DIR}/node${i}.log" | grep -Fx 3
  done

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 6
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

  sleep 12
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster ls

  # Confirm that local operations are not blocked by having no event hubs running, but that events are not being
  # distributed.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  sleep 1
  grep -Fc "instance-restarted" "${TEST_DIR}/node1.log"
  grep -Fc "instance-restarted" "${TEST_DIR}/node1.log" | grep -Fx 7
  for i in 2 3; do
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 6
  done

  # Kill monitors.
  kill -9 ${monitorNode1PID} || true
  kill -9 ${monitorNode2PID} || true
  kill -9 ${monitorNode3PID} || true

  # Cleanup.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1
  LXD_DIR="${LXD_TWO_DIR}" lxc delete -f c2
  LXD_DIR="${LXD_THREE_DIR}" lxc delete -f c3
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
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

test_clustering_uuid() {
  # shellcheck disable=2039,3043
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # create two cluster nodes
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  ensure_import_testimage

  # spawn an instance on the first LXD node
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1 --target=node1
  # get its volatile.uuid
  uuid_before_move=$(LXD_DIR="${LXD_ONE_DIR}" lxc config get c1 volatile.uuid)
  # stop the instance
  LXD_DIR="${LXD_ONE_DIR}" lxc stop -f c1
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
  LXD_DIR="${LXD_TWO_DIR}" lxc delete c1 -f
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}
