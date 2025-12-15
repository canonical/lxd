# lxd_websocket_operation simulates a websocket operation on an LXD instance through.
# The operation runs for a specified duration and then terminates
# shellcheck disable=SC2034
lxd_websocket_operation() {
  local instance_name="$1"
  local duration="$2"
  local project_name="${3:-default}"

  lxc query --wait -X POST -d '{"duration": "'"${duration}"'", "op_class": 2, "op_type": 20, "resources": {"instances": ["/1.0/instances/'"${instance_name}"'?project='"${project_name}"'"]}}' "/internal/testing/operation-wait?project=${project_name}"
}

# lxd_volume_operation simulates a custom volume operation.
# The operation runs for a specified duration and then terminates.
# shellcheck disable=SC2034
lxd_volume_operation() {
  local pool_name="$1"
  local volume_name="$2"
  local duration="$3"
  local project_name="${4:-default}"

  lxc query --wait -X POST -d '{"duration": "'"${duration}"'", "op_class": 1, "op_type": 48, "resources": {"storage_volumes": ["/1.0/storage-pools/'"${pool_name}"'/volumes/custom/'"${volume_name}"'?project='"${project_name}"'"]}}' "/internal/testing/operation-wait?project=${project_name}"
}

# check_registered_operations checks for registered operations.
# It waits for operations to be registered and then ensures that all the PIDs
# provided correspond to active PIDs.
check_registered_operations() {
  sleep 0.5
  local pid
  for pid in "$@"; do
    [ -d "/proc/${pid}" ] || return 1
  done
}

# terminate_leftovers terminates any leftover processes given their PIDs.
# Missing PIDs are ignored.
terminate_leftovers() {
  local pid
  for pid in "$@"; do
    kill -9 "${pid}" || true
  done
}
