test_clustering_membership() {
  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/server.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # Configuration keys can be changed on any node.
  LXD_DIR="${LXD_TWO_DIR}" lxc config set images.auto_update_interval 10
  LXD_DIR="${LXD_ONE_DIR}" lxc info | grep -q 'images.auto_update_interval: "10"'
  LXD_DIR="${LXD_TWO_DIR}" lxc info | grep -q 'images.auto_update_interval: "10"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(cat "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(cat "${TEST_DIR}/ns/${ns2}/PID")"
  nsenter -n -t "${ns1_pid}" -- ip link show "${bridge}" > /dev/null
  nsenter -n -t "${ns2_pid}" -- ip link show "${bridge}" > /dev/null

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
  # checking which are database nodes and which are not.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep "node1" | grep -q "YES"
  LXD_DIR="${LXD_FOUR_DIR}" lxc cluster list | grep "node2" | grep -q "YES"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep "node3" | grep -q "YES"
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster list | grep "node4" | grep -q "NO"
  LXD_DIR="${LXD_FIVE_DIR}" lxc cluster list | grep "node5" | grep -q "NO"

  # Show a single node
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster show node5 | grep -q "node5"

  # Client certificate are shared across all nodes.
  LXD_DIR="${LXD_ONE_DIR}" lxc remote add cluster 10.1.1.101:8443 --accept-certificate --password=sekret
  LXD_DIR="${LXD_ONE_DIR}" lxc remote set-url cluster https://10.1.1.102:8443
  lxc network list cluster: | grep -q "${bridge}"

  # Shutdown a non-database node, and wait a few seconds so it will be
  # detected as down.
  LXD_DIR="${LXD_FOUR_DIR}" lxc config set cluster.offline_threshold 4
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  sleep 6
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep "node5" | grep -q "OFFLINE"

  # Trying to delete the preseeded network now fails, because a node is degraded.
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Force the removal of the degraded node.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster delete node5 --force

  # Now the preseeded network can be deleted, and all nodes are
  # notified.
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Rename a node using the pre-existing name.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster rename node4 node5

  # Trying to delete a container which is the only one with a copy of
  # an image results in an error
  LXD_DIR="${LXD_FOUR_DIR}" ensure_import_testimage
  ! LXD_DIR="${LXD_FOUR_DIR}" lxc cluster delete node5
  LXD_DIR="${LXD_TWO_DIR}" lxc image delete testimage

  # Remove a node gracefully.
  LXD_DIR="${LXD_FOUR_DIR}" lxc cluster delete node5
  LXD_DIR="${LXD_FOUR_DIR}" lxc cluster list | grep -q "https://0.0.0.0"

  LXD_DIR="${LXD_FOUR_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 2
  rm -f "${LXD_FIVE_DIR}/unix.socket"
  rm -f "${LXD_FOUR_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge
}

test_clustering_containers() {
  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/server.crt")

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

  # A Node: field indicates on which node the container is running
  LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q "Node: node2"

  # Start the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc start foo
  LXD_DIR="${LXD_TWO_DIR}" lxc info foo | grep -q "Status: Running"
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep foo | grep -q RUNNING

  # Trying to delete a node which has container results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster delete node2

  # Exec a command in the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc exec foo ls / | grep -q linuxrc

  # Pull, push and delete files from the container via node1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/non-existing-file "${TEST_DIR}/non-existing-file"
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
  ! LXD_DIR="${LXD_ONE_DIR}" lxc file pull foo/hello-world/text "${TEST_DIR}/hello-world-text"

  # Stop the container via node1
  LXD_DIR="${LXD_ONE_DIR}" lxc stop foo

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
  ! LXD_DIR="${LXD_ONE_DIR}" lxc info foo | grep -q foo-bak-2

  # Export from node1 the image that was imported on node2
  LXD_DIR="${LXD_ONE_DIR}" lxc image export testimage "${TEST_DIR}/testimage"
  rm "${TEST_DIR}/testimage.tar.xz"

  # Create a container on node1 using the image that was stored on
  # node2.
  LXD_DIR="${LXD_TWO_DIR}" lxc launch --target node1 testimage bar
  LXD_DIR="${LXD_TWO_DIR}" lxc stop bar
  LXD_DIR="${LXD_ONE_DIR}" lxc delete bar
  ! LXD_DIR="${LXD_TWO_DIR}" lxc list | grep -q bar

  # Delete the network now, since we're going to shutdown node2 and it
  # won't be possible afterwise.
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Shutdown node 2, wait for it to be considered offline, and list
  # containers.
  LXD_DIR="${LXD_THREE_DIR}" lxc config set cluster.offline_threshold 4
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 6
  LXD_DIR="${LXD_ONE_DIR}" lxc list | grep foo | grep -q ERROR

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 2
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge
}

test_clustering_storage() {
  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/server.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # The state of the preseeded storage pool is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Trying to pass config values other than 'source' results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir source=/foo size=123 --target node1

  # Define storage pools on the two nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node1
  LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -q node1
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -q node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep state: | grep -q PENDING

  # The source config key is not legal for the final pool creation
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 dir source=/foo

  # Create the storage pool
  LXD_DIR="${LXD_TWO_DIR}" lxc storage create pool1 dir
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep state: | grep -q CREATED

  # The 'source' config key is omitted when showing the cluster
  # configuration, and included when showing the node-specific one.
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep -q source
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 --target node1 | grep source | grep -q "$(basename "${LXD_ONE_DIR}")"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 --target node2 | grep source | grep -q "$(basename "${LXD_TWO_DIR}")"

  # Update the storage pool
  LXD_DIR="${LXD_ONE_DIR}" lxc storage set pool1 rsync.bwlimit 10
  LXD_DIR="${LXD_TWO_DIR}" lxc storage show pool1 | grep rsync.bwlimit | grep -q 10
  LXD_DIR="${LXD_TWO_DIR}" lxc storage unset pool1 rsync.bwlimit
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage show pool1 | grep -q rsync.bwlimit

  # Delete the storage pool
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete pool1
  ! LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep -q pool1

  # Create a volume on node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create data web
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume list data | grep -q node1
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume list data | grep -q node1

  # Since the volume name is unique to node1, it's possible to show, rename,
  # get the volume without specifying the --target parameter.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show data web | grep -q "node: node1"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume rename data web webbaz
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename data webbaz web
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume get data web size

  # Create another volume on node2 with the same name of the one on
  # node1.
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create --target node2 data web

  # Trying to show, rename or delete the web volume without --target
  # fails, because it's not unique.
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show data web
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename data web webbaz
  ! LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete data web

  # Specifying the --target parameter shows, renames and deletes the
  # proper volume.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show --target node1 data web | grep -q "node: node1"
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume show --target node2 data web | grep -q "node: node2"
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node1 data web webbaz
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename --target node2 data web webbaz
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete --target node2 data webbaz

  # Since now there's only one volume in the pool left named webbaz,
  # it's possible to delete it without specifying --target.
  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume delete data webbaz

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 2
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge
}

test_clustering_network() {
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
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/server.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}"

  # The state of the preseeded network is still CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network list| grep "${bridge}" | grep -q CREATED

  # Trying to pass config values other than
  # 'bridge.external_interfaces' results in an error
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create foo ipv4.address=auto --target node1

  net="${bridge}x"

  # Define networks on the two nodes
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node1
  LXD_DIR="${LXD_TWO_DIR}" lxc network show  "${net}" | grep -q node1
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network show "${net}" | grep -q node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep state: | grep -q PENDING

  # The bridge.external_interfaces config key is not legal for the final network creation
  ! LXD_DIR="${LXD_ONE_DIR}" lxc network create "${net}" bridge.external_interfaces=foo

  # Create the network
  LXD_DIR="${LXD_TWO_DIR}" lxc network create "${net}"
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" | grep state: | grep -q CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc network show "${net}" --target node2 | grep state: | grep -q CREATED

  # FIXME: rename the network is not supported with clustering
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network rename "${net}" "${net}-foo"

  # Delete the networks
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${net}"
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 2
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge
}

test_clustering_upgrade() {
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
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/server.crt")

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
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "message: fully operational"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "message: waiting for other nodes to be upgraded"

  # Respawn the first node, so it matches the version the second node
  # believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The second daemon has now unblocked
  LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=30

  # The cluster is again operational
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q "OFFLINE"

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
  ! LXD_DIR="${LXD_TWO_DIR}" lxd waitready --timeout=5

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "message: fully operational"
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node2 | grep -q "message: waiting for other nodes to be upgraded"
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster show node3 | grep -q "message: fully operational"

  # Respawn the first node and third node, so they match the version
  # the second node believes to have.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" false
  shutdown_lxd "${LXD_THREE_DIR}"
  LXD_NETNS="${ns3}" respawn_lxd "${LXD_THREE_DIR}" true

  # The cluster is again operational
  ! LXD_DIR="${LXD_ONE_DIR}" lxc cluster list | grep -q "OFFLINE"

  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 2
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge
}
