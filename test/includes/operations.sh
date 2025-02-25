# lxd_websocket_operation simulates a websocket operation on an LXD instance through.
# The operation runs for a specified duration and then terminates
# shellcheck disable=all
lxd_websocket_operation() {
  local instance_name="$1"
  local duration="$2"

  lxc query --wait -X POST -d '{\"duration\": \"$duration\", \"op_class\": \"websocket\", \"op_type\": \"CommandExec\", \"resources\": {\"instances\": [\"/1.0/instances/${instance_name}?project=default\"]}}' /internal/testing/operation-wait
}
# shellcheck enable=all

# lxd_volume_operation simulates a custom volume operation.
# The operation runs for a specified duration and then terminates.
# shellcheck disable=all
lxd_volume_operation() {
  local pool_name="$1"
  local volume_name="$2"
  local duration="$3"

  lxc query --wait -X POST -d '{\"duration\": \"$duration\", \"op_class\": \"task\", \"op_type\": \"CustomVolumeBackupCreate\", \"resources\": {\"storage_volumes\": [\"/1.0/storage-pools/${pool_name}/volumes/custom/${volume_name}?project=default\"]}}' /internal/testing/operation-wait
}
# shellcheck enable=all