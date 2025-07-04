test_waitready() {
  # Create storage pool.
  local lxd_backend storage_pool
  lxd_backend=$(storage_backend "${LXD_DIR}")
  storage_pool="lxdtest-$(basename "${LXD_DIR}")-pool"
  br_name="lxdt$$"

  lxc storage create "${storage_pool}" "${lxd_backend}"
  lxc network create "${br_name}"

  echo "==> Corrupt the network by setting an invalid external interface"
  ip link add foo type bridge
  ip addr add dev foo 10.1.123.10/24
  # Inject the config setting manually to prevent validation by LXD.
  network_id="$(lxd sql global "select id from networks where name='${br_name}'" -f csv)"
  lxd sql global "INSERT INTO networks_config (network_id, node_id, key, value) VALUES (${network_id}, null, 'bridge.external_interfaces', 'foo')"

  # Stop the daemon.
  shutdown_lxd "${LXD_DIR}"

  echo "==> Corrupt the storage pool directory to cause errors when LXD starts trying to start the pool"
  # Perform this after stopping the daemon to make sure all mounts of the storage pool directory are given up.
  rm -rf "${LXD_DIR}/storage-pools/${storage_pool}"
  touch "${LXD_DIR}/storage-pools/${storage_pool}"

  # Start the daemon.
  respawn_lxd "${LXD_DIR}" true

  # Wait for storage and network.
  # When requesting both --network and --storage the request will already timeout on --network.
  echo "==> LXD started but fails to start all networks and storage pools"
  [ "$(lxd waitready --network --storage --timeout 1 2>&1)" = "Error: Networks not ready yet after 1s timeout" ]

  # Not setting a timeout should have the same effect and return instantly.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" lxc query "/internal/ready?network=1&storage=1" 2>&1)" = "Error: Networks not ready yet" ]

  # The standard waitready without extra flags should still return with success.
  lxd waitready

  # The CLI reports the network and storage pool to be unavailable.
  lxc network show "${br_name}" | grep -xF "status: Unavailable"
  lxc storage show "${storage_pool}" | grep -xF "status: Unavailable"

  echo "==> Restore the network by unsetting the external interface"
  lxd sql global 'DELETE FROM networks_config WHERE key="bridge.external_interfaces"'
  ip link del foo

  # LXD retries starting the networks every 60s.
  # Wait for 80s to ensure the network is now ready but the storage pool isn't.
  echo "==> Networks will appear ready after the next retry"
  [ "$(lxd waitready --network --storage --timeout 80 2>&1)" = "Error: Storage pools not ready yet after 80s timeout" ]

  # Not setting a timeout should have the same effect and return instantly.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" lxc query "/internal/ready?network=1&storage=1" 2>&1)" = "Error: Storage pools not ready yet" ]

  # The standard waitready without extra flags should still return with success.
  lxd waitready

  # The CLI reports only the storage pool to be unavailable.
  lxc network show "${br_name}" | grep -xF "status: Created"
  lxc storage show "${storage_pool}" | grep -xF "status: Unavailable"

  echo "==> Restore the storage pool directory"
  rm "${LXD_DIR}/storage-pools/${storage_pool}"

  # LXD retries starting the storage pools every 60s.
  # The internal TryMount function retries 20 times over a course of 10s so we should account for this too.
  # Wait for 80s to ensure the storage pool is now ready too.
  echo "==> All resources will appear ready after the next retry window"
  lxd waitready --network --storage --timeout 80

  # The standard waitready without extra flags should still return with success.
  lxd waitready

  # The CLI reports both network and storage pool to be created.
  lxc network show "${br_name}" | grep -xF "status: Created"
  lxc storage show "${storage_pool}" | grep -xF "status: Created"

  # Cleanup.
  lxc storage delete "${storage_pool}"
  lxc network delete "${br_name}"
}
