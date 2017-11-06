test_clustering() {
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
  ip netns exec "${ns1}" ip link show "${bridge}" > /dev/null
  ip netns exec "${ns2}" ip link show "${bridge}" > /dev/null

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

  # List all nodes
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep -q "ONLINE"

  # Shutdown a non-database node, and wait a few seconds so it will be
  # detected as down.
  LXD_DIR="${LXD_FIVE_DIR}" lxd shutdown
  sleep 22
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster list | grep "node5" | grep -q "OFFLINE"

  # Trying to delete the preseeded network now fails, because a node is degraded.
  ! LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Force the removal of the degraded node.
  LXD_DIR="${LXD_THREE_DIR}" lxc cluster remove node5 --force

  # Now the preseeded network can be deleted, and all nodes are
  # notified.
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  # Remove a node gracefully.
  LXD_DIR="${LXD_FOUR_DIR}" lxc cluster remove node4

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
}
