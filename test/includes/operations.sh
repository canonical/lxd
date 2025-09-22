# lxd_websocket_operation simulates a websocket operation on an LXD instance through.
# The operation runs for a specified duration and then terminates
# shellcheck disable=SC2034,SC2016
lxd_websocket_operation() {
  local instance_name="$1"
  local duration="$2"
  local project_name="${3:-default}"

  # Note that the `-d` flag argument is quoted with single quotes and contains bash arguments, but we ignored SC2016 above.
  # Why?? `lxc` isn't really `lxc`, it's a shell function which will split the given arguments and expand them anyway.
  lxc query --wait -X POST -d '{\"duration\": \"$duration\", \"op_class\": 2, \"op_type\": 20, \"resources\": {\"instances\": [\"/1.0/instances/${instance_name}?project=${project_name}\"]}}' "/internal/testing/operation-wait?project=${project_name}"
}

# lxd_volume_operation simulates a custom volume operation.
# The operation runs for a specified duration and then terminates.
# shellcheck disable=SC2034,SC2016
lxd_volume_operation() {
  local pool_name="$1"
  local volume_name="$2"
  local duration="$3"
  local project_name="${3:-default}"

  # Note that the `-d` flag argument is quoted with single quotes and contains bash arguments, but we ignored SC2016 above.
  # Why?? `lxc` isn't really `lxc`, it's a shell function which will split the given arguments and expand them anyway.
  lxc query --wait -X POST -d '{\"duration\": \"$duration\", \"op_class\": 1, \"op_type\": 48, \"resources\": {\"storage_volumes\": [\"/1.0/storage-pools/${pool_name}/volumes/custom/${volume_name}?project=${project_name}\"]}}' "/internal/testing/operation-wait?project=${project_name}"
}
