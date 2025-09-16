test_clustering_waitready() {
  # shellcheck disable=SC2034
  local LXD_DIR
  local lxd_backend

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Get used storage backend.
  lxd_backend=$(storage_backend "${LXD_ONE_DIR}")

  # Add a newline at the end of each line. YAML has weird rules..
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

  # Setup a cluster wide network.
  LXD_DIR="${LXD_ONE_DIR}" lxc network create br1 --target "node1"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create br1 --target "node2"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create br1 --target "node3"
  LXD_DIR="${LXD_ONE_DIR}" lxc network create br1 ipv4.address=none ipv6.address=none

  # Set up node-specific storage pool keys for the selected backend.
  driver_config=""
  if [ "${lxd_backend}" = "btrfs" ] || [ "${lxd_backend}" = "lvm" ] || [ "${lxd_backend}" = "zfs" ]; then
      driver_config="size=1GiB"
  fi

  if [ "${lxd_backend}" = "ceph" ]; then
      driver_config="source=lxdtest-$(basename "${TEST_DIR}")-pool1"
  fi

  # Define storage pools on the two nodes.
  driver_config_node1="${driver_config}"
  driver_config_node2="${driver_config}"
  driver_config_node3="${driver_config}"

  if [ "${lxd_backend}" = "zfs" ]; then
      driver_config_node1="${driver_config_node1} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
      driver_config_node2="${driver_config_node2} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns2}"
      driver_config_node3="${driver_config_node3} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns3}"
  fi

  if [ "${lxd_backend}" = "lvm" ]; then
      driver_config_node1="${driver_config_node1} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
      driver_config_node2="${driver_config_node2} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns2}"
      driver_config_node3="${driver_config_node3} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns3}"
  fi

  # Setup a cluster wide storage pool.
  # shellcheck disable=SC2086
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${lxd_backend}" ${driver_config_node1} --target "node1"
  # shellcheck disable=SC2086
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${lxd_backend}" ${driver_config_node2} --target "node2"
  # shellcheck disable=SC2086
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${lxd_backend}" ${driver_config_node3} --target "node3"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create pool1 "${lxd_backend}"

  # Evacuate the first cluster member.
  # Afterwards we break both the cluster member's network and storage to see how the waitready command behaves.
  # We should be able to use the waitready command until all resources are successfully started again.
  # In the last step we can then safely run "lxc cluster restore" to bring the member back online.
  # If this member had any instance relying on those resources, they can now safely be moved back.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate "node1" --force

  echo "==> Corrupt the network by setting an invalid external interface"
  ns1_pid="$(< "${TEST_DIR}/ns/${ns1}/PID")"
  nsenter -m -n -t "${ns1_pid}" -- ip link add foo type dummy
  LXD_DIR="${LXD_ONE_DIR}" lxc network set br1 bridge.external_interfaces=foo --target "node1"
  # This will cause LXD's network startup to fail with "Failed starting: Only unconfigured network interfaces can be bridged"
  # Set the address after adding the interface to the bridge as LXD's validation would catch this.
  nsenter -m -n -t "${ns1_pid}" -- ip addr add dev foo 10.1.123.10/24

  # Stop the first cluster member.
  shutdown_lxd "${LXD_ONE_DIR}"

  echo "==> Corrupt the storage pool directory on the first cluster member to cause errors when LXD starts trying to start the pool"
  # Perform this after stopping the daemon to make sure all mounts of the storage pool directory are given up.
  rm -rf "${LXD_ONE_DIR}/storage-pools/pool1"
  touch "${LXD_ONE_DIR}/storage-pools/pool1"

  # Start the first cluster member.
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # Wait for storage and network.
  # When requesting both --network and --storage the request will already timeout on --network.
  echo "==> LXD started but fails to start all networks and storage pools"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd waitready --network --storage --timeout 1 2>&1)" = "Error: Networks not ready yet after 1s timeout" ]

  # Not setting a timeout should have the same effect and return instantly.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc query "/internal/ready?network=1&storage=1" 2>&1)" = "Error: Networks not ready yet" ]

  # The standard waitready without extra flags should still return with success.
  LXD_DIR="${LXD_ONE_DIR}" lxd waitready

  # The first cluster member cannot be restored as the network and storage pool aren't ready yet.
  # As the network is checked first, it is returned first.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force 2>&1)" = "Error: Failed to update cluster member state: Cannot restore \"node1\" because some networks aren't started yet" ]

  echo "==> Restore the network by unsetting the external interface"
  LXD_DIR="${LXD_ONE_DIR}" lxc network unset br1 bridge.external_interfaces --target "node1"

  # LXD retries starting the networks every 60s.
  # Wait for 80s to ensure the network is now ready but the storage pool isn't.
  echo "==> Networks will appear ready after the next retry"
  [ "$(LXD_DIR="${LXD_ONE_DIR}" lxd waitready --network --storage --timeout 80 2>&1)" = "Error: Storage pools not ready yet after 80s timeout" ]

  # Not setting a timeout should have the same effect and return instantly.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc query "/internal/ready?network=1&storage=1" 2>&1)" = "Error: Storage pools not ready yet" ]

  # The standard waitready without extra flags should still return with success.
  LXD_DIR="${LXD_ONE_DIR}" lxd waitready

  # The first cluster member cannot be restored as the storage pool isn't ready yet.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force 2>&1)" = "Error: Failed to update cluster member state: Cannot restore \"node1\" because some storage pools aren't started yet" ]

  echo "==> Restore the storage pool directory"
  rm "${LXD_ONE_DIR}/storage-pools/pool1"

  # LXD retries starting the storage pools every 60s.
  # The internal TryMount function retries 20 times over a course of 10s so we should account for this too.
  # Wait for 80s to ensure the storage pool is now ready too.
  echo "==> All resources will appear ready after the next retry window"
  LXD_DIR="${LXD_ONE_DIR}" lxd waitready --network --storage --timeout 80

  # The standard waitready without extra flags should still return with success.
  LXD_DIR="${LXD_ONE_DIR}" lxd waitready

  # The first cluster member can now be restored.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore node1 --force

  echo "==> Corrupt the network again by setting an invalid external interface"
  # Reset the IP again before to not get caught by LXD's validation.
  nsenter -m -n -t "${ns1_pid}" -- ip addr del dev foo 10.1.123.10/24
  LXD_DIR="${LXD_ONE_DIR}" lxc network set br1 bridge.external_interfaces=foo --target "node1"
  nsenter -m -n -t "${ns1_pid}" -- ip addr add dev foo 10.1.123.10/24

  echo "==> Corrupt the storage pool directory again on the first cluster member to cause errors when LXD starts trying to start the pool"
  # Perform this after stopping the daemon to make sure all mounts of the storage pool directory are given up.
  rm -rf "${LXD_ONE_DIR}/storage-pools/pool1"
  touch "${LXD_ONE_DIR}/storage-pools/pool1"

  # Restart the first cluster member.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The cluster member cannot be evacuated as long as it's networks and storage pools aren't started.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate "node1" --force 2>&1)" = "Error: Failed to update cluster member state: Cannot evacuate \"node1\" because some networks aren't started yet" ]

  echo "==> Restore the network by unsetting the external interface"
  LXD_DIR="${LXD_ONE_DIR}" lxc network unset br1 bridge.external_interfaces --target "node1"

  # Restart the first cluster member.
  # To speed up the test we don't wait for LXD until it tries starting the network again.
  # That was already tested above.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The cluster member cannot be evacuated as long as it's storage pools aren't started.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate "node1" --force 2>&1)" = "Error: Failed to update cluster member state: Cannot evacuate \"node1\" because some storage pools aren't started yet" ]

  echo "==> Restore the storage pool directory"
  rm "${LXD_ONE_DIR}/storage-pools/pool1"

  # Restart the first cluster member.
  shutdown_lxd "${LXD_ONE_DIR}"
  LXD_NETNS="${ns1}" respawn_lxd "${LXD_ONE_DIR}" true

  # The cluster member can now be evacuated.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate "node1" --force
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster restore "node1" --force

  # Cleanup.
  nsenter -m -n -t "${ns1_pid}" -- ip link del foo
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
