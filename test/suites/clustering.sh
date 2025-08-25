test_clustering_enable() {
  local LXD_DIR

  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  # Test specified core.https_address with no cluster.https_address
  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}

    lxc config show | grep -xE "^\s+core\.https_address: 127\.0\.0\.1:[0-9]{4,5}"
    # Launch a container.
    ensure_import_testimage
    lxc storage create default dir
    lxc profile device add default root disk path="/" pool="default"
    lxc launch testimage c1

    # Enable clustering.
    lxc cluster enable node1

    # Test the non-recursive mode to list cluster members.
    [ "$(lxc query /1.0/cluster/members | jq -r '.[0]')" = "/1.0/cluster/members/node1" ]

    # Test the recursive mode to list cluster members.
    # The command implicitly sets the recursive=1 query paramter.
    lxc cluster list | grep -wF node1

    # The container is still there and now shows up as
    # running on node 1.
    [ "$(lxc list -f csv -c nL c1)" = "c1,node1" ]

    # Clustering can't be enabled on an already clustered instance.
    ! lxc cluster enable node2 || false

    # Delete the container
    lxc delete --force c1
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test wildcard core.https_address with no cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
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
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address 127.0.0.1
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -F 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test wildcard core.https_address with valid cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address ::
    lxc config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -F 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test empty core.https_address with no cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
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
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config unset core.https_address
    lxc config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -F 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test empty core.https_address with default port cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config unset core.https_address
    lxc config set cluster.https_address 127.0.0.1
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -F 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test covered cluster.https_address
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set core.https_address 127.0.0.1:8443
    lxc config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -F 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"

  # Test cluster listener after reload
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR=${LXD_INIT_DIR}
    lxc config set cluster.https_address 127.0.0.1:8443
    kill -9 "$(< "${LXD_DIR}/lxd.pid")"
    respawn_lxd "${LXD_DIR}" true
    # Enable clustering.
    lxc cluster enable node1
    lxc cluster list | grep -F 127.0.0.1:8443
  )

  kill_lxd "${LXD_INIT_DIR}"
}

test_clustering_membership() {
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Neither server certificate can be deleted
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config trust remove "$(cert_fingerprint "${LXD_ONE_DIR}/server.crt")" || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc config trust remove "$(cert_fingerprint "${LXD_ONE_DIR}/server.crt")" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc config trust remove "$(cert_fingerprint "${LXD_TWO_DIR}/server.crt")" || false
  ! LXD_DIR="${LXD_TWO_DIR}" lxc config trust remove "$(cert_fingerprint "${LXD_TWO_DIR}/server.crt")" || false

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F 'cluster.offline_threshold: "11"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(< "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(< "${TEST_DIR}/ns/${ns2}/PID")"
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
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 4 "${LXD_FIVE_DIR}" "${LXD_ONE_DIR}"

  # List all nodes, using clients points to different nodes and
  # checking which are database nodes and which are database-standby nodes.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -xF -- '- database-leader'
  [ "$(LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep -Fc "database-standby")" = "2" ]
  [ "$(LXD_DIR="${LXD_FIVE_DIR}" lxc cluster list | grep -Fc "database ")" = "3" ]

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
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 12
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node3 | grep -xF "status: Offline"

  # Gracefully remove a node and check trust certificate is removed.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep node4
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM identities WHERE type = 3 and name = "node4"')" = 1 ]
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster remove node4
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep node4 || false
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv 'SELECT COUNT(*) FROM identities WHERE type = 3 and name = "node4"')" = 0 ]

  # The node isn't clustered anymore.
  ! LXD_DIR="${LXD_FOUR_DIR}" lxc cluster list || false

  # Generate a join token for the sixth node.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node6)"

  # Check token is associated to correct name.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node6 | grep "${token}"

  # Spawn a sixth node, using join token.
  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns6="${prefix}6"

  # shellcheck disable=SC2034
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 2 "${LXD_SIX_DIR}" "${token}"

  # Check token has been deleted after join.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens
  ! LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node6 || false

  # Generate a join token for a seventh node
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node7)"

  # Check token is associated to correct name
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list-tokens | grep node7 | grep "${token}"

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
  setup_clustering_netns 8
  LXD_EIGHT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns8="${prefix}8"

  # shellcheck disable=SC2034
  spawn_lxd_and_join_cluster "${ns8}" "${bridge}" "${cert}" 8 2 "${LXD_EIGHT_DIR}" "${token_valid}"

  # This will cause the token to expire
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.join_token_expiry=2S
  token_expired="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster add --quiet node9)"
  sleep 2

  # Spawn a ninth node, using join token.
  setup_clustering_netns 9
  LXD_NINE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns9="${prefix}9"

  # shellcheck disable=SC2034
  ! spawn_lxd_and_join_cluster "${ns9}" "${bridge}" "${cert}" 9 2 "${LXD_NINE_DIR}" "${token_expired}" || false

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Init a container on node2, using a client connected to node1
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage foo

  # The container is visible through both nodes
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c nsL)" = "foo,STOPPED,node2" ]
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c nsL)" = "foo,STOPPED,node2" ]

  # A Location: field indicates on which node the container is running
  LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -xF "Location: node2"

  # Start the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc start foo
  LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_ONE_DIR}" lxc list --fast | grep -wF foo | grep -wF RUNNING

  # Trying to delete a node which has container results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node2 || false

  # Exec a command in the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc exec foo -- ls / | grep -xF proc

  # Pull, push and delete files from the container via node1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/non-existing-file "${TEST_DIR}/non-existing-file" || false
  mkdir "${TEST_DIR}/hello-world"
  echo "hello world" > "${TEST_DIR}/hello-world/text"
  LXD_DIR="${LXD_ONE_DIR}" lxc file push "${TEST_DIR}/hello-world/text" foo/hello-world-text
  LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world-text "${TEST_DIR}/hello-world-text"
  [ "$(< "${TEST_DIR}/hello-world-text")" = "hello world" ]
  rm "${TEST_DIR}/hello-world-text"
  LXD_DIR="${LXD_ONE_DIR}" lxc file push --recursive "${TEST_DIR}/hello-world" foo/
  rm -r "${TEST_DIR}/hello-world"
  LXD_DIR="${LXD_ONE_DIR}" lxc file pull --recursive foo/hello-world "${TEST_DIR}"
  [ "$(< "${TEST_DIR}/hello-world/text")" = "hello world" ]
  rm -r "${TEST_DIR}/hello-world"
  LXD_DIR="${LXD_ONE_DIR}" lxc file delete foo/hello-world/text
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world/text "${TEST_DIR}/hello-world-text" || false

  # Stop the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force

  # Rename the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo foo2
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc list -f csv -c n)" = "foo2" ]
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo2 foo

  # Show lxc.log via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc info --show-log foo | grep -xF 'Log:'

  # Create, rename and delete a snapshot of the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo foo-bak
  LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -wF foo-bak
  LXD_DIR="${LXD_ONE_DIR}" lxc rename foo/foo-bak foo/foo-bak-2
  LXD_DIR="${LXD_ONE_DIR}" lxc delete foo/foo-bak-2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -wF foo-bak-2 || false

  # Export from node1 the image that was imported on node2
  LXD_DIR="${LXD_ONE_DIR}" lxc image export testimage "${TEST_DIR}/testimage"
  rm "${TEST_DIR}/testimage.tar.xz"

  # Create a container on node1 using the image that was stored on
  # node2.
  LXD_DIR="${LXD_TWO_DIR}" lxc launch --target node1 testimage bar
  LXD_DIR="${LXD_TWO_DIR}" lxc stop bar --force
  LXD_DIR="${LXD_ONE_DIR}" lxc delete bar
  ! LXD_DIR="${LXD_TWO_DIR}" lxc list -c n | grep -wF bar || false

  # Create a container on node1 using a snapshot from node2.
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot foo foo-bak
  LXD_DIR="${LXD_TWO_DIR}" lxc copy foo/foo-bak bar --target node1
  LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -xF "Location: node1"
  LXD_DIR="${LXD_THREE_DIR}" lxc delete bar

  # Copy the container on node2 to node3, using a client connected to
  # node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc copy foo bar --target node3
  LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -xF "Location: node3"

  # Move the container on node3 to node1, using a client connected to
  # node2 and a different container name than the original one. The
  # volatile.apply_template config key is preserved.
  apply_template1=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get bar volatile.apply_template)

  LXD_DIR="${LXD_TWO_DIR}" lxc move bar egg --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc info egg | grep -xF "Location: node2"
  apply_template2=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)
  [ "${apply_template1}" =  "${apply_template2}" ]

  # Move back to node3 the container on node1, keeping the same name.
  apply_template1=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)
  LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node3
  LXD_DIR="${LXD_ONE_DIR}" lxc info egg | grep -xF "Location: node3"
  apply_template2=$(LXD_DIR="${LXD_TWO_DIR}" lxc config get egg volatile.apply_template)
  [ "${apply_template1}" =  "${apply_template2}" ]

  # Live migration is not supported for containers.
  LXD_DIR="${LXD_TWO_DIR}" lxc start egg
  ! LXD_DIR="${LXD_TWO_DIR}" lxc move egg --target node1 || false
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c sL egg)" = "RUNNING,node3" ]
  LXD_DIR="${LXD_TWO_DIR}" lxc stop -f egg

  # Create backup and attempt to move container. Move should fail and container should remain on node1.
  LXD_DIR="${LXD_THREE_DIR}" lxc query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/instances/egg/backups
  ! LXD_DIR="${LXD_THREE_DIR}" lxc move egg --target node2 || false
  LXD_DIR="${LXD_THREE_DIR}" lxc info egg | grep -xF "Location: node3"

  LXD_DIR="${LXD_THREE_DIR}" lxc delete egg

  # Delete the network now, since we're going to shutdown node2 and it
  # won't be possible afterwise.
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Shutdown node 2, wait for it to be considered offline, and list
  # containers.
  LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 12
  LXD_DIR="${LXD_ONE_DIR}" lxc list --fast | grep -wF foo | grep -wF ERROR

  # For an instance on an offline member, we can get its config but not use recursion nor get instance state.
  LXD_DIR="${LXD_ONE_DIR}" lxc config show foo
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/instances/foo" | jq -r '.status')" = "Error" ]
  ! LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/instances/foo?recursion=1" || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/instances/foo/state" || false

  # Init a container without specifying any target. It will be placed
  # on node1 since node2 is offline and both node1 and node3 have zero
  # containers, but node1 has a lower node ID.
  LXD_DIR="${LXD_THREE_DIR}" lxc init --empty bar
  LXD_DIR="${LXD_THREE_DIR}" lxc info bar | grep -xF "Location: node1"

  # Init a container without specifying any target. It will be placed
  # on node3 since node2 is offline and node1 already has a container.
  LXD_DIR="${LXD_THREE_DIR}" lxc init --empty egg
  LXD_DIR="${LXD_THREE_DIR}" lxc info egg | grep -xF "Location: node3"

  LXD_DIR="${LXD_ONE_DIR}" lxc delete egg
  LXD_DIR="${LXD_ONE_DIR}" lxc delete bar

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  # Check both nodes show preseeded storage pool created.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node1'")" = "node1,1" ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node2'")" = "node2,1" ]

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
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -F status: | grep -wF Created

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
    LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -xF "Location: node1"
    LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -wF "snap-test"

    # Start and stop the container on its new node1 host
    LXD_DIR="${LXD_TWO_DIR}" lxc start foo
    LXD_DIR="${LXD_TWO_DIR}" lxc stop foo --force

    # Init a new container on node2 using the snapshot on node1
    LXD_DIR="${LXD_ONE_DIR}" lxc copy foo/snap-test egg --target node2
    LXD_DIR="${LXD_TWO_DIR}" lxc start egg
    LXD_DIR="${LXD_ONE_DIR}" lxc stop egg --force
    LXD_DIR="${LXD_ONE_DIR}" lxc delete egg
  fi

  # If the driver has the same per-node storage pool config (e.g. size), make sure it's included in the
  # member_config, and actually added to a joining node so we can validate it.
  if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "ceph" ] || [ "${poolDriver}" = "lvm" ]; then
    # Spawn a third node
    setup_clustering_netns 3
    LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    ns3="${prefix}3"
    LXD_NETNS="${ns3}" spawn_lxd "${LXD_THREE_DIR}" false

    key=$(echo "${driver_config}" | cut -d'=' -f1)
    value=$(echo "${driver_config}" | cut -d'=' -f2-)

    # Set member_config to match `spawn_lxd_and_join_cluster` for 'data' and `driver_config` for 'pool1'.
    member_config="{\"entity\": \"storage-pool\",\"name\":\"pool1\",\"key\":\"${key}\",\"value\":\"${value}\"}"
    if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "lvm" ] ; then
      member_config="{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"size\",\"value\":\"1GiB\"},${member_config}"
    fi

    # Manually send the join request.
    cert=$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_ONE_DIR}/cluster.crt")
    token="$(lxc cluster add node3 --quiet)"
    op=$(curl --unix-socket "${LXD_THREE_DIR}/unix.socket" --fail-with-body -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d "{\"server_name\":\"node3\",\"enabled\":true,\"member_config\":[${member_config}],\"server_address\":\"100.64.1.103:8443\",\"cluster_address\":\"100.64.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_token\":\"${token}\"}" | jq -r .operation)
    curl --unix-socket "${LXD_THREE_DIR}/unix.socket" --fail-with-body "lxd${op}/wait"

    # Ensure that node-specific config appears on all nodes,
    # regardless of the pool being created before or after the node joined.
    for n in node1 node2 node3 ; do
      LXD_DIR="${LXD_ONE_DIR}" lxc storage get pool1 "${key}" --target "${n}" | grep -F "${value}"
    done

    # Other storage backends will be finished with the third node, so we can remove it.
    if [ "${poolDriver}" != "ceph" ]; then
      LXD_DIR="${LXD_ONE_DIR}" lxc cluster remove node3 --yes
    fi
  fi

  if [ "${poolDriver}" = "ceph" ]; then
    # Move the container to node3, renaming it
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo bar --target node3
    LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -xF "Location: node3"
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -wF "snap-test"

    # Shutdown node 3, and wait for it to be considered offline.
    LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 11
    LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
    sleep 12

    # Move the container back to node2, even if node3 is offline
    LXD_DIR="${LXD_ONE_DIR}" lxc move bar --target node2
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -xF "Location: node2"
    LXD_DIR="${LXD_TWO_DIR}" lxc info bar | grep -wF "snap-test"

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
    LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -xF "Location: node2"

    # Stop the container and move it to node1
    LXD_DIR="${LXD_ONE_DIR}" lxc stop foo --force
    LXD_DIR="${LXD_TWO_DIR}" lxc move foo bar --target node1
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -xF "Location: node1"

    # Start and stop the migrated container on node1
    LXD_DIR="${LXD_TWO_DIR}" lxc start bar
    LXD_DIR="${LXD_ONE_DIR}" lxc stop bar --force

    # Rename the container locally on node1
    LXD_DIR="${LXD_TWO_DIR}" lxc rename bar foo
    LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -xF "Location: node1"

    # Copy the container without specifying a target, it will be placed on node2
    # since it's the one with the least number of containers (0 vs 1)
    sleep 6 # Wait for pending operations to be removed from the database
    LXD_DIR="${LXD_ONE_DIR}" lxc copy foo bar
    LXD_DIR="${LXD_ONE_DIR}" lxc info bar | grep -xF "Location: node2"

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
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF pool1 || false

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # The state of the preseeded network shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network list | grep -F "${bridge}" | grep -wF CREATED

  # Add a newline at the end of each line. YAML has weird rules.
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
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  ! LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 -n "${net}" testimage bar || false

  # The bridge.external_interfaces config key is not legal for the final network creation
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" bridge.external_interfaces=foo || false

  # Create the network
  LXD_DIR="${LXD_TWO_DIR}" lxc network create "${net}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Created
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" --target node2 | grep -F status: | grep -wF Created

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
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Errored # Check has errored status.
  nsenter -n -t "${LXD_PID1}" -- ip link delete "${net}" # Remove conflicting interface.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" ipv4.dhcp=false || false # Check supplying global config on re-create is blocked.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" # Check re-create succeeds.
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep -F status: | grep -wF Created # Check is created after fix.
  nsenter -n -t "${LXD_PID1}" -- ip -details link show "${net}" | grep bridge # Check bridge exists.
  nsenter -n -t "${LXD_PID2}" -- ip -details link show "${net}" | grep bridge # Check bridge exists.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" || false # Check re-create is blocked after success.

  # Check both nodes marked created.
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'")" = "node1,1" ]
[ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql global --format csv "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'")" = "node2,1" ]

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

  # Check networks local to a cluster member show up when targeting that member
  # and hidden when targeting other cluster members. Setup is in includes/clustering.sh
  LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node1 | grep localBridge1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node1 | grep localBridge2 || false
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node2 | grep localBridge1 || false
  LXD_DIR="${LXD_ONE_DIR}" lxc network list --target=node2 | grep localBridge2

  # Cleanup instances and image.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1 c2 c3
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage

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
  local LXD_DIR LXD_NETNS

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # First, test the upgrade with a 2-node cluster
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

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

# Perform a downgrade of a 2-member cluster, then a join a third member and perform one more downgrade.
test_clustering_downgrade() {
  local LXD_DIR LXD_NETNS

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # First, test the upgrade with a 2-node cluster.
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

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
  local LXD_DIR LXD_NETNS N

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  LXD_CLUSTER_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  N=8

  setup_clustering_netns 1
  LXD_ONE_DIR="${LXD_CLUSTER_DIR}/1"
  mkdir -p "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  for i in $(seq 2 "${N}"); do
    setup_clustering_netns "${i}"
    LXD_ITH_DIR="${LXD_CLUSTER_DIR}/${i}"
    mkdir -p "${LXD_ITH_DIR}"
    nsi="${prefix}${i}"
    spawn_lxd_and_join_cluster "${nsi}" "${bridge}" "${cert}" "${i}" 1 "${LXD_ITH_DIR}" "${LXD_ONE_DIR}"
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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Give LXD a couple of seconds to get event API connected properly
  sleep 2

  # Init a container on node2, using a client connected to node1
  LXD_DIR="${LXD_TWO_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node2 testimage foo

  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo --alias=foo-image
  LXD_DIR="${LXD_ONE_DIR}" lxc image show foo-image | grep -F "public: false"
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete foo-image

  LXD_DIR="${LXD_TWO_DIR}" lxc snapshot foo backup
  LXD_DIR="${LXD_ONE_DIR}" lxc publish foo/backup --alias=foo-backup-image
  LXD_DIR="${LXD_ONE_DIR}" lxc image show foo-backup-image | grep -F "public: false"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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

  LXD_DIR="${LXD_TWO_DIR}" lxc exec c1 -- ls /mnt | grep -xF hello
  LXD_DIR="${LXD_TWO_DIR}" lxc exec c2 -- ls /mnt | grep -xF hello

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
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
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Send update request
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster update-cert "${cert_path}" "${key_path}" -q

  cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}"
  cmp -s "${LXD_TWO_DIR}/cluster.crt" "${cert_path}"

  cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}"
  cmp -s "${LXD_TWO_DIR}/cluster.key" "${key_path}"

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -F "server_name: node1"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
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
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

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

  LXD_DIR="${LXD_ONE_DIR}" lxc info --target node2 | grep -F "server_name: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc info --target node1 | grep -F "server_name: node1"

  LXD_DIR="${LXD_ONE_DIR}" lxc warning list | grep -F "Unable to update cluster certificate"

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

test_clustering_update_cert_token() {
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
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
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # quick check
  ! cmp -s "${LXD_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${LXD_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  # shellcheck disable=SC2034
  local LXD_DIR LXD_NETNS

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  LXD_NETNS="${ns2}" spawn_lxd "${LXD_TWO_DIR}" false

  # Check a join token cannot be created for the reserved name 'none'
  ! lxc cluster add none --quiet || false

  # Check a server with the name 'valid' cannot be joined when modifying the token.
  # Therefore replace the valid name in the token with 'none'.
  malicious_token="$(lxc cluster add valid --quiet | base64 -d | jq '.server_name |= "none"' | base64 --wrap=0)"
  op=$(curl --unix-socket "${LXD_TWO_DIR}/unix.socket" --fail-with-body -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d "{\"server_name\":\"valid\",\"enabled\":true,\"member_config\":[{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"source\",\"value\":\"\"}],\"server_address\":\"100.64.1.102:8443\",\"cluster_address\":\"100.64.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_token\":\"${malicious_token}\"}" | jq -r .operation)
  [ "$(curl --unix-socket "${LXD_TWO_DIR}/unix.socket" "lxd${op}/wait" | jq '.error_code')" = "403" ]

  # Check that the server cannot be joined using a valid token by changing it's name to 'none'.
  token="$(lxc cluster add valid2 --quiet)"
  [ "$(curl --unix-socket "${LXD_TWO_DIR}/unix.socket" -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d "{\"server_name\":\"none\",\"enabled\":true,\"member_config\":[{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"source\",\"value\":\"\"}],\"server_address\":\"100.64.1.102:8443\",\"cluster_address\":\"100.64.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_token\":\"${token}\"}" | jq -r '.error_code')" = "400" ]

  # Check the server can be joined.
  token="$(lxc cluster add node2 --quiet)"
  op=$(curl --unix-socket "${LXD_TWO_DIR}/unix.socket" --fail-with-body -H 'Content-Type: application/json' -X PUT "lxd/1.0/cluster" -d "{\"server_name\":\"node2\",\"enabled\":true,\"member_config\":[{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"source\",\"value\":\"\"}],\"server_address\":\"100.64.1.102:8443\",\"cluster_address\":\"100.64.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_token\":\"${token}\"}" | jq -r .operation)
  curl --unix-socket "${LXD_TWO_DIR}/unix.socket" --fail-with-body "lxd${op}/wait"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "message: Fully operational"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Init a container on node1, using a client connected to node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc launch --target node1 testimage foo

  # Get container PID
  instance_pid="$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c p foo)"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  LXD_DIR="${LXD_TWO_DIR}" lxd sql global 'delete from images_nodes where node_id = 1'

  # Check image import from node2 by creating container on node1 in other project.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc init --target node1 --empty c2 --project p1
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c2 --project p1

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

test_clustering_metrics() {
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{\"location\": \"node1\", \"type_code\": 0, \"message\": \"node1 is in a bad mood\"}' /internal/testing/warnings
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{\"location\": \"node1\", \"type_code\": 1, \"message\": \"node1 is bored\"}' /internal/testing/warnings
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{\"location\": \"node2\", \"type_code\": 0, \"message\": \"node2 is too cool for this\"}' /internal/testing/warnings

  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 2"
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 1"

  # Add a nodeless warning and check if count incremented only on the leader node.
  LXD_DIR="${LXD_ONE_DIR}" lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"nodeless warning\"}' /internal/testing/warnings

  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 3"
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 1"

  # Acknowledge/resolve a warning and check if the count decremented on the node relative to the resolved warning.
  uuid=$(LXD_DIR="${LXD_ONE_DIR}" lxc warning list --format json | jq -r '.[] | select(.last_message=="node1 is bored") | .uuid')
  LXD_DIR="${LXD_ONE_DIR}" lxc warning ack "${uuid}"

  LXD_DIR="${LXD_ONE_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 2"
  LXD_DIR="${LXD_TWO_DIR}" lxc query "/1.0/metrics" | grep -xF "lxd_warnings_total 1"

  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1 stopped c2
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"

  # Bootstrap the first node using a custom cluster port
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "dir" "8444"

  # The bootstrap node appears in the list with its cluster-specific port
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -F :8444
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF "database: true"

  # Add a remote using the core.https_address of the bootstrap node, and check
  # that the REST API is exposed.
  url="https://100.64.1.101:8443"
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  lxc remote add cluster --token "${token}" "${url}"
  lxc storage list cluster: | grep -F data

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node using a custom cluster port
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}" "dir" "8444"

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
  LXD_DIR="${LXD_ONE_DIR}" deps/import-busybox --alias testimage
  lxc init --target node2 testimage cluster:c1
  lxc list cluster: | grep -wF c1

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

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
  lxc launch testimage c1

  # Modify the container's rootfs and create a new image from the container
  lxc exec c1 -- touch /a
  lxc stop c1 --force
  lxc publish c1 --alias new-image

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
  lxc delete c1

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
  local lxdDir

  # Because we do not want tests to only run on Ubuntu (due to cluster's fan network dependency)
  # instead we will just spawn forkdns directly and check DNS resolution.

  # XXX: make a copy of the global LXD_DIR
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
  kill -9 "${forkdns_pid1}"
  kill -9 "${forkdns_pid2}"
  ip link delete "${prefix}1"
  ip link delete "${prefix}2"
}

test_clustering_fan() {
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Import the test image on node1
  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  fanbridge="${prefix}f"

  echo "Create a fan bridge"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create --target node1 "${fanbridge}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create --target node2 "${fanbridge}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${fanbridge}" bridge.mode=fan
  LXD_DIR="${LXD_ONE_DIR}" lxc network set "${fanbridge}" dns.domain=fantastic
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
  lxc stop -f c1
  LXD_DIR="${LXD_ONE_DIR}" lxc move c1 --target node2
  lxc start c1
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
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_recover() {
  # shellcheck disable=SC2034
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

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
  sleep 0.5

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

# Putting HAproxy in front of a cluster allows to use a single address to access
# the cluster, filter out some bogus/spam/malicious requests without terminating
# TLS and while preserving the original client IP addresses.
test_clustering_ha() {
  local LXD_DIR
  local successes
  local failures
  local FOUND_RADOSGW

  # Workaround radosgw binding port 80
  FOUND_RADOSGW="false"
  if command -v microceph >/dev/null && ss --no-header -nltp 'sport inet:80' | grep -wF radosgw >/dev/null; then
    FOUND_RADOSGW="true"
    microceph disable rgw
  fi

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  sleep 0.5
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
  # shellcheck disable=SC2034
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  echo "Launched member 1"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  echo "Launched member 2"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  echo "Launched member 3"

  # Spawn a fourth node, this will be a non-voter, stand-by node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  echo "Launched member 4"

  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -Fc "database-standby")" = "1" ]

  # Shutdown the first node.
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown

  echo "Stopped member 1"

  # The fourth node has been promoted, while the first one demoted.
  LXD_DIR="${LXD_THREE_DIR}" lxd sql local 'select * from raft_nodes'
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster ls
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node4
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node1
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node4 | grep -xF -- "- database"
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
  # shellcheck disable=SC2034
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fourth node
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check there is one database-standby member.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep -Fc "database-standby")" = "1" ]

  # Kill the second node.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.offline_threshold 11
  kill -9 "$(< "${LXD_TWO_DIR}/lxd.pid")"

  # Wait for the second node to be considered offline and be replaced by the
  # fourth node.
  sleep 15

  # The second node is offline and has been demoted.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "status: Offline"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -xF "database: false"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF "status: Online"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -F -- "- database"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set cluster.offline_threshold 11
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F 'cluster.offline_threshold: "11"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(< "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(< "${TEST_DIR}/ns/${ns2}/PID")"
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
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fourth node, this will be a database-standby node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list

  # Kill the second node, to prevent it from transferring its database role at shutdown.
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
  sleep 12

  # The node does not appear anymore in the cluster list.
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node2" || false

  # There are only 2 database nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- database-leader"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node3 | grep -xF -- "- database"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF -- "- database"

  # The second node is still in the raft_nodes table.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes WHERE address = '100.64.1.102:8443'")" = 1 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes")" = 4 ]

  # Force removing the raft node.
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster remove-raft-node -q "100.64.1.102"

  # Wait for a heartbeat to propagate and a rebalance to be performed.
  sleep 12

  # We're back to 3 database nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -xF -- "- database-leader"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node3 | grep -xF -- "- database"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node4 | grep -xF -- "- database"

  # The second node is gone from the raft_nodes_table.
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes WHERE address = '100.64.1.102:8443'")" = 0 ]
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd sql local --format csv "SELECT COUNT(*) FROM raft_nodes")" = 3 ]
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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 4 "${LXD_FIVE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a sixth node, using non-database node4 as join target.
  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns6="${prefix}6"
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 4 "${LXD_SIX_DIR}" "${LXD_ONE_DIR}"

  # Default failure domain
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "failure_domain: default"

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

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -F "failure_domain: az2"

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
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" lxc config set cluster.images_minimal_replica 1
  LXD_DIR="${LXD_ONE_DIR}" lxc config set images.auto_update_interval 1

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}" "${poolDriver}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}" "${poolDriver}"

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
  dd if=/dev/urandom count=32 | LXD_DIR="${LXD_REMOTE_DIR}" lxc file push - c1/foo
  LXD_DIR="${LXD_REMOTE_DIR}" lxc publish c1 --alias testimage --reuse --public
  new_fingerprint="$(LXD_DIR="${LXD_REMOTE_DIR}" lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  pids=""

  if [ "${poolDriver}" != "dir" ]; then
    # Check image storage volume records exist.
    if [ "${poolDriver}" = "ceph" ]; then
      [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 1 ]
    else
      [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 3 ]
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
      [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 0 ]
      [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${new_fingerprint}'")" = 1 ]
    else
      [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${old_fingerprint}'")" = 1 ]
      [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM storage_volumes WHERE name = '${new_fingerprint}'")" = 2 ]
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
  dd if=/dev/urandom count=32 | LXD_DIR="${LXD_REMOTE_DIR}" lxc file push - c1/foo
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
    LXD_DIR="${LXD_ONE_DIR}" lxc image rm --project "${project}" $(LXD_DIR="${LXD_ONE_DIR}" lxc image ls --format csv --project "${project}" | cut -d, -f2)
    # shellcheck disable=SC2046
    LXD_DIR="${LXD_ONE_DIR}" lxc rm --project "${project}" $(LXD_DIR="${LXD_ONE_DIR}" lxc list --format csv --columns n --project "${project}")
  done

  # shellcheck disable=SC2046
  LXD_DIR="${LXD_REMOTE_DIR}" lxc image rm $(LXD_DIR="${LXD_REMOTE_DIR}" lxc image ls --format csv | cut -d, -f2)
  # shellcheck disable=SC2046
  LXD_DIR="${LXD_REMOTE_DIR}" lxc rm $(LXD_DIR="${LXD_REMOTE_DIR}" lxc list --format csv --columns n)

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
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -wF data | grep -wF CREATED

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}" "${poolDriver}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}" "${poolDriver}"

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
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -F "status: Evacuated"

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list

  # Check instance status
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -xF "Status: RUNNING"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -xF "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -xF "Status: RUNNING"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -xF "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -xF "Status: STOPPED"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -xF "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -xF "Status: RUNNING"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -xF "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -xF "Status: STOPPED"
  ! LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -xF "Location: node1" || false
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -xF "Location: node2"

  c1_location=$(LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | awk '/Location:/ {print $2}')
  c2_location=$(LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | awk '/Location:/ {print $2}')
  c4_location=$(LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | awk '/Location:/ {print $2}')
  c5_location=$(LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | awk '/Location:/ {print $2}')
  c6_location=$(LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | awk '/Location:/ {print $2}')

  # Restore first node with "skip" mode.
  # "skip" mode restores cluster member status without starting instances or migrating back evacuated instances.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster restore node1 --action=skip --force

  # Ensure the node is restored
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -xF "status: Online"

  # Verify that instances remain in their evacuated state/location
  # c1 should stay on the node it was migrated to
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -xF "Location: ${c1_location}"
  # c2 should stay on the node it was migrated to
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -xF "Location: ${c2_location}"
  # c3 should remain stopped on node1
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -xF "Status: STOPPED"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -xF "Location: node1"
  # c4 should stay on the node it was migrated to
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -xF "Location: ${c4_location}"
  # c5 should remain stopped on the node it was migrated to
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -xF "Status: STOPPED"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -xF "Location: ${c5_location}"
  # c6 should stay on the node it was already on
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -xF "Location: ${c6_location}"

  # Now test a full restore for comparison
  # Evacuate node1 again
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster evacuate node1 --force

  # Ensure instances cannot be created on the evacuated node
  ! LXD_DIR="${LXD_TWO_DIR}" lxc launch testimage c7 --target=node1 || false

  # Ensure the node is evacuated
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node1 | grep -xF "status: Evacuated"

  # Restore first node (without "skip" mode)
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster restore node1 --force

  # For debugging
  LXD_DIR="${LXD_TWO_DIR}" lxc list

  # Ensure the instances were moved back to the origin
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c1 | grep -xF "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c2 | grep -xF "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c3 | grep -xF "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c4 | grep -xF "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -xF "Status: STOPPED"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c5 | grep -xF "Location: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -xF "Status: RUNNING"
  LXD_DIR="${LXD_TWO_DIR}" lxc info c6 | grep -xF "Location: node2"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap the first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn 6 nodes in total for role coverage.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${LXD_FIVE_DIR}" "${LXD_ONE_DIR}"

  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns6="${prefix}6"
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 1 "${LXD_SIX_DIR}" "${LXD_ONE_DIR}"

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
  daemon_pid=$(< "${LXD_FIVE_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(< "${LXD_SIX_DIR}/lxd.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true

  config=$(mktemp -p "${TEST_DIR}" XXX)
  # Update the cluster configuration with new port numbers
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster show > "${config}"

  # lxd cluster edit generates ${LXD_DIR}/database/lxd_recovery_db.tar.gz
  sed -e "s/:8443/:9393/" -i "${config}"
  LXD_DIR="${LXD_ONE_DIR}" lxd cluster edit < "${config}"

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
  LXD_NETNS="${ns5}" respawn_lxd "${LXD_FIVE_DIR}" false
  # Only wait on the last node, because we don't know who the voters are
  LXD_NETNS="${ns6}" respawn_lxd "${LXD_SIX_DIR}" true

  # Let the heartbeats catch up
  sleep 12

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # Bootstrap the first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fourth node
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fifth node
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${LXD_FIVE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a sixth node
  setup_clustering_netns 6
  LXD_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns6="${prefix}6"
  spawn_lxd_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 1 "${LXD_SIX_DIR}" "${LXD_ONE_DIR}"

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

  # make sure node6 is a spare ndoe
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -wF "node6"
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node6 | grep -E "\- database(|-standy|-leader)$" || false

  # waite for leader update table raft_node of local database by heartbeat
  sleep 10s

  # Remove the leader, via the spare node
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster rm node1

  # Ensure the remaining node is working and node1 had successful remove
  ! LXD_DIR="${LXD_SIX_DIR}" lxc cluster list | grep -wF "node1" || false
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster list | grep -wF "node6"

  # Check whether node6 is changed from a spare node to a leader node.
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster show node6 | grep -xF -- "- database-leader"
  LXD_DIR="${LXD_SIX_DIR}" lxc cluster show node6 | grep -xF -- "- database"

  # Spawn a sixth node
  setup_clustering_netns 7
  LXD_SEVEN_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns7="${prefix}7"
  spawn_lxd_and_join_cluster "${ns7}" "${bridge}" "${cert}" 7 6 "${LXD_SEVEN_DIR}" "${LXD_SIX_DIR}"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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
  sleep 0.5
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}

test_clustering_groups() {
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  lxc remote add cluster --token "${token}" "https://100.64.1.101:8443"

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

  # Check duplicates cannot be created
  lxc cluster group create cluster:foo
  [ "$(! "${_LXC}" cluster group create cluster:foo 2>&1 1>/dev/null)" = 'Error: Cluster group "foo" already exists' ]
  lxc cluster group create cluster:bar
  [ "$(! "${_LXC}" cluster group rename cluster:bar foo 2>&1 1>/dev/null)" = 'Error: Name "foo" already in use' ]
  lxc cluster group delete cluster:foo
  lxc cluster group delete cluster:bar

  # Create new cluster group which should be empty
  lxc cluster group create cluster:foobar
  [ "$(lxc query cluster:/1.0/cluster/groups/foobar | jq '.members | length')" -eq 0 ]

  # Copy both description and members from default group
  lxc cluster group show cluster:default | lxc cluster group edit cluster:foobar
  [ "$(lxc query cluster:/1.0/cluster/groups/foobar | jq '.description == "Default cluster group"')" = "true" ]
  [ "$(lxc query cluster:/1.0/cluster/groups/foobar | jq '.members | length')" -eq 3 ]

  # Delete all members from new group
  lxc cluster group remove cluster:node1 foobar
  lxc cluster group remove cluster:node2 foobar
  lxc cluster group remove cluster:node3 foobar

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

  # Remove node2 from "foobar" group should fail as node2 is not in any other group
  ! lxc cluster group remove cluster:node2 foobar || false

  # Rename group "foobar" to "blah"
  lxc cluster group rename cluster:foobar blah
  [ "$(lxc query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "blah"; .)')" = "true" ]

  lxc cluster group create cluster:foobar2
  lxc cluster group assign cluster:node3 default,foobar2

  # Create a new group "newgroup"
  lxc cluster group create cluster:newgroup
  [ "$(lxc query cluster:/1.0/cluster/groups/newgroup | jq '.members | length')" -eq 0 ]

  # Add node1 to the "newgroup" group
  lxc cluster group add cluster:node1 newgroup
  [ "$(lxc query cluster:/1.0/cluster/members/node1 | jq 'any(.groups[] == "newgroup"; .)')" = "true" ]

  # remove node1 from "newgroup"
  lxc cluster group remove cluster:node1 newgroup

  # delete cluster group "newgroup"
  lxc cluster group delete cluster:newgroup

  # Try to create a cluster group using yaml
  lxc cluster group create cluster:yamlgroup <<EOF
description: foo
EOF

  [ "$(lxc query cluster:/1.0/cluster/groups/yamlgroup | jq -r '.description')" = "foo" ]
  # Delete the cluster group "yamlgroup"
  lxc cluster group delete cluster:yamlgroup

  # Try to initialize a cluster group with multiple nodes
  lxc query cluster:/1.0/cluster/groups -X POST -d '{\"name\":\"multi-node-group\",\"description\":\"\",\"members\":[\"node1\",\"node2\",\"node3\"]}'

  # Ensure cluster group created with requested members
  [ "$(lxc query cluster:/1.0/cluster/groups/multi-node-group | jq '.members | length')" -eq 3 ]

  # Remove nodes and delete cluster group
  lxc cluster group remove cluster:node1 multi-node-group
  lxc cluster group remove cluster:node2 multi-node-group
  lxc cluster group remove cluster:node3 multi-node-group

  lxc cluster group delete cluster:multi-node-group

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
  lxc info cluster:c1 | grep -xF "Location: node1"

  # c2 should go to node2. Additionally it should be possible to specify the network.
  lxc init testimage cluster:c2 --target=@blah --network "${bridge}"
  lxc info cluster:c2 | grep -xF "Location: node2"

  # c3 should go to node2 again. Additionally it should be possible to specify the storage pool.
  lxc init testimage cluster:c3 --target=@blah --storage data
  lxc info cluster:c3 | grep -xF "Location: node2"

  # Direct targeting of node2 should work
  lxc init testimage cluster:c4 --target=node2
  lxc info cluster:c4 | grep -xF "Location: node2"

  # Direct targeting of node3 should work
  lxc init testimage cluster:c5 --target=node3
  lxc info cluster:c5 | grep -xF "Location: node3"

  # Clean up
  lxc rm -f c1 c2 c3 c4 c5

  ## Restricted project tests

  # Create an empty cluster group and reference it from project config
  lxc cluster group create cluster:fizz
  lxc project create cluster:buzz -c restricted=true -c restricted.cluster.groups=fizz

  # Cannot launch an instance because fizz has no members
  ! lxc init testimage cluster:c1 --project buzz || false

  # Group fizz has no members, but it cannot be deleted because it is referenced by project buzz.
  [ "$(lxc_remote query cluster:/1.0/cluster/groups/fizz | jq -r '.used_by | @csv')" = '"/1.0/projects/buzz"' ]
  ! lxc cluster group delete cluster:fizz || false

  # Restricted certificate does not see project fizz in cluster group used by URLs
  token1="$(lxc config trust add cluster: --name cg-cert1 --quiet --restricted --projects default)"
  LXD_CONF1=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF1}" gen_cert_and_key "client"
  LXD_CONF="${LXD_CONF1}" lxc remote add cluster_remote "${token1}"
  [ "$(LXD_CONF="${LXD_CONF1}" lxc_remote query cluster_remote:/1.0/cluster/groups/fizz | jq -r '.used_by | length')" = 0 ]

  # Fine-grained TLS identity does not see project fizz in cluster group used by URLs unless any groups it is a member of
  # have can_view on the project.
  lxc auth group create cluster:test-group
  token2="$(lxc auth identity create cluster:tls/gc-cert2 --group test-group --quiet)"
  LXD_CONF2=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF2}" gen_cert_and_key "client"
  LXD_CONF="${LXD_CONF2}" lxc remote add cluster_remote "${token2}"
  [ "$(LXD_CONF="${LXD_CONF2}" lxc_remote query cluster_remote:/1.0/cluster/groups/fizz | jq -r '.used_by | length')" = 0 ]
  lxc auth group permission add cluster:test-group project buzz can_view
  [ "$(LXD_CONF="${LXD_CONF2}" lxc_remote query cluster_remote:/1.0/cluster/groups/fizz | jq -r '.used_by | @csv')" = '"/1.0/projects/buzz"' ]

  # Clean up.
  lxc config trust remove "cluster:$(cert_fingerprint "${LXD_CONF1}/client.crt")"
  lxc auth identity delete cluster:tls/gc-cert2
  lxc auth group delete cluster:test-group
  rm -rf "${LXD_CONF1}" "${LXD_CONF2}"
  lxc project delete cluster:buzz
  lxc cluster group delete cluster:fizz

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
  lxc info cluster:c1 --project foo | grep -xF "Location: node2"
  lxc info cluster:c2 --project foo | grep -xF "Location: node2"
  lxc delete -f c1 c2 --project foo

  # Check can specify any member or group when restricted.cluster.groups is empty.
  lxc project unset foo restricted.cluster.groups
  lxc init testimage cluster:c1 --project foo --target=node1
  lxc info cluster:c1 --project foo | grep -xF "Location: node1"

  lxc init testimage cluster:c2 --project foo --target=@blah
  lxc info cluster:c2 --project foo | grep -xF "Location: node2"

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules...
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node.
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node.
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fourth node.
  setup_clustering_netns 4
  LXD_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns4="${prefix}4"
  spawn_lxd_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${LXD_FOUR_DIR}" "${LXD_ONE_DIR}"

  # Spawn a fifth node.
  setup_clustering_netns 5
  LXD_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns5="${prefix}5"
  spawn_lxd_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${LXD_FIVE_DIR}" "${LXD_ONE_DIR}"

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"

  ensure_import_testimage

  # c1 should go to node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc launch testimage c1 --target=node1
  LXD_DIR="${LXD_ONE_DIR}" lxc info c1 | grep -xF "Location: node1"
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
  sleep 2

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "2" ]
  done

  # Switch into event-hub mode.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role add node1 event-hub
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster role add node2 event-hub
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -Fc event-hub)" = "2" ]

  # Check events were distributed.
  for i in 1 2 3; do
    [ "$(grep -Fc "cluster-member-updated" "${TEST_DIR}/node${i}.log")" = "2" ]
  done

  sleep 2 # Wait for notification heartbeat to distribute new roles.
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: hub-server"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: hub-server"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: hub-client"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: hub-client"

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2
  sleep 2

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "4" ]
  done

  # Launch container on node3 to check image distribution events work during event-hub mode.
  LXD_DIR="${LXD_THREE_DIR}" lxc launch testimage c3 --target=node3

  for i in 1 2 3; do
    [ "$(grep -Fc "instance-created" "${TEST_DIR}/node${i}.log")" = "1" ]
  done

  # Switch into full-mesh mode by removing one event-hub role so there is <2 in the cluster.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster role remove node1 event-hub
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -Fc event-hub)" = "1" ]

  sleep 1 # Wait for notification heartbeat to distribute new roles.
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_THREE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FOUR_DIR}" lxc info | grep -F "server_event_mode: full-mesh"
  LXD_DIR="${LXD_FIVE_DIR}" lxc info | grep -F "server_event_mode: full-mesh"

  # Check events were distributed.
  for i in 1 2 3; do
    [ "$(grep -Fc "cluster-member-updated" "${TEST_DIR}/node${i}.log")" = "3" ]
  done

  # Restart instance generating restart lifecycle event.
  LXD_DIR="${LXD_ONE_DIR}" lxc restart -f c1
  LXD_DIR="${LXD_THREE_DIR}" lxc restart -f c2
  sleep 2

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "6" ]
  done

  # Switch back into event-hub mode by giving the role to node4 and node5.
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster role remove node2 event-hub
  LXD_DIR="${LXD_FOUR_DIR}" lxc cluster role add node4 event-hub
  LXD_DIR="${LXD_FIVE_DIR}" lxc cluster role add node5 event-hub

  sleep 2 # Wait for notification heartbeat to distribute new roles.
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
  sleep 2

  [ "$(grep -Fc "instance-restarted" "${TEST_DIR}/node1.log")" = "7" ]
  for i in 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    [ "$(grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log")" = "6" ]
  done

  # Kill monitors.
  kill -9 "${monitorNode1PID}" || true
  kill -9 "${monitorNode2PID}" || true
  kill -9 "${monitorNode3PID}" || true

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
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # create two cluster nodes
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

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

test_clustering_trust_add() {
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # create two cluster nodes
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Check using token that is expired

  # Set token expiry to 1 seconds
  lxc config set core.remote_token_expiry 1S

  # Get a certificate add token from LXD_ONE. The operation will run on LXD_ONE locally.
  lxd_one_token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"
  sleep 2

  # Expect one running token operation.
  operation_uuid="$(LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "TOKEN,Executing operation,RUNNING" | cut -d, -f1 )"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Executing operation,RUNNING"
  is_uuid_v4 "${operation_uuid}"

  # Get the address of LXD_TWO.
  lxd_two_address="https://$(LXD_DIR="${LXD_TWO_DIR}" lxc config get core.https_address)"

  # Test adding the remote using the address of LXD_TWO with the token operation running on LXD_ONE.
  # LXD_TWO does not have the operation running locally, so it should find the UUID of the operation in the database
  # and query LXD_ONE for it. LXD_TWO should cancel the operation by sending a DELETE /1.0/operations/{uuid} to LXD_ONE
  # and needs to parse the metadata of the operation into the correct type to complete the trust process.
  # The expiry time should be parsed and found to be expired so the add action should fail.
  ! lxc remote add lxd_two "${lxd_two_address}" --token "${lxd_one_token}" || false

  # Expect the operation to be cancelled.
  LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Executing operation,CANCELLED"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Executing operation,CANCELLED"

  # Set token expiry to 1 hour
  lxc config set core.remote_token_expiry 1H

  # Check using token that isn't expired

  # Get a certificate add token from LXD_ONE. The operation will run on LXD_ONE locally.
  lxd_one_token="$(LXD_DIR="${LXD_ONE_DIR}" lxc config trust add --name foo --quiet)"

  # Expect one running token operation.
  operation_uuid="$(LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "TOKEN,Executing operation,RUNNING" | cut -d, -f1 )"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Executing operation,RUNNING"
  is_uuid_v4 "${operation_uuid}"

  # Get the address of LXD_TWO.
  lxd_two_address="https://$(LXD_DIR="${LXD_TWO_DIR}" lxc config get core.https_address)"

  # Test adding the remote using the address of LXD_TWO with the token operation running on LXD_ONE.
  # LXD_TWO does not have the operation running locally, so it should find the UUID of the operation in the database
  # and query LXD_ONE for it. LXD_TWO should cancel the operation by sending a DELETE /1.0/operations/{uuid} to LXD_ONE
  # and needs to parse the metadata of the operation into the correct type to complete the trust process.
  lxc remote add lxd_two "${lxd_two_address}" --token "${lxd_one_token}"

  # Expect the operation to be cancelled.
  LXD_DIR="${LXD_ONE_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Executing operation,CANCELLED"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format csv | grep -F "${operation_uuid},TOKEN,Executing operation,CANCELLED"

  # Clean up
  lxc remote rm lxd_two

  # Unset token expiry
  lxc config unset core.remote_token_expiry

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
