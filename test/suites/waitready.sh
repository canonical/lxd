test_waitready() {
  local LXD_STORAGE_DIR

  # Spawn a new daemon.
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false
  LXD_DIR="${LXD_STORAGE_DIR}"

  # Create storage pool.
  local lxd_backend storage_pool
  lxd_backend=$(storage_backend "$LXD_DIR")
  storage_pool="lxdtest-$(basename "${LXD_DIR}")-pool"
  br_name="lxdt$$"

  lxc storage create "${storage_pool}" "${lxd_backend}"
  lxc network create "${br_name}"

  echo "==> Corrupt the network by setting an invalid MTU"
  lxd sql global 'INSERT INTO networks_config (network_id, node_id, key, value) VALUES (1, null, "bridge.mtu", "42")'

  echo "==> Corrupt the storage pool source to cause errors when LXD starts trying to start the storage pools"
  initial_source="$(lxc storage get "${storage_pool}" source)"
  lxd sql global 'UPDATE storage_pools_config SET value="/invalid/path" WHERE key="source"'

  # Restart the daemon.
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # Wait for storage and network.
  # When requesting both --network and --storage the request will already timeout on --network.
  echo "==> LXD started but fails to start all networks and storage pools"
  [ "$(lxd waitready --network --storage --timeout 1 2>&1)" = "Error: Networks not ready yet after 1s timeout" ]

  # Not setting a timeout should have the same effect and return instantly.
  [ "$(DEBUG="" lxc query "/internal/ready?network=1&storage=1" 2>&1)" = "Error: Networks not ready yet" ]

  # The standard waitready without extra flags should still return with success.
  lxd waitready

  echo "==> Restore the network MTU"
  lxd sql global 'DELETE FROM networks_config WHERE key="bridge.mtu"'

  # LXD retries starting the networks every 60s.
  echo "==> Networks will appear ready after the 60s retry window"
  [ "$(lxd waitready --network --storage --timeout 60 2>&1)" = "Error: Storage pools not ready yet after 60s timeout" ]

  # Not setting a timeout should have the same effect and return instantly.
  [ "$(DEBUG="" lxc query "/internal/ready?network=1&storage=1" 2>&1)" = "Error: Storage pools not ready yet" ]

  # The standard waitready without extra flags should still return with success.
  lxd waitready

  echo "==> Restore the storage pool source"
  lxd sql global "UPDATE storage_pools_config SET value=\"${initial_source}\" WHERE key=\"source\""

  # LXD retries starting the storage pools every 60s.
  # The internal TryMount function retries 20 times over a course of 10s so we should account for this too.
  echo "==> All resources will appear ready after the 60s (+10s) retry window"
  lxd waitready --network --storage --timeout 70

  # The standard waitready without extra flags should still return with success.
  lxd waitready

  # Cleanup.
  lxc storage delete "${storage_pool}"
  lxc network delete "${br_name}"

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}
