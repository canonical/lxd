test_resources() {
  RES=$(lxc storage show --resources "lxdtest-$(basename "${LXD_DIR}")")
  echo "${RES}" | grep -q "^space:"

  RES=$(lxc info --resources)
  echo "${RES}" | grep -q "^CPU"
  echo "${RES}" | grep -q "Cores:"
  echo "${RES}" | grep -q "Threads:"
  echo "${RES}" | grep -q "Free:"
  echo "${RES}" | grep -q "Used:"
  echo "${RES}" | grep -q "Total:"
}

test_resources_bcache() {
  # Make sure bcache is loaded.
  modprobe bcache

  # Create two loop devices used for the bcache cache and backing device.
  configure_loop_device loop_file_1 loop_device_1
  configure_loop_device loop_file_2 loop_device_2

  # Create bcache device.
  # shellcheck disable=SC2154
  make-bcache -C "${loop_device_1}" -B "${loop_device_2}"

  # Register bcache device.
  # shellcheck disable=SC2154
  echo "${loop_device_1}" > /sys/fs/bcache/register
  # shellcheck disable=SC2154
  echo "${loop_device_2}" > /sys/fs/bcache/register

  # Print for debugging purposes.
  lxc query /1.0/resources | jq '.storage.disks'

  # Check the bcache device is returned by LXD.
  [ "$(lxc query /1.0/resources | jq -r '.storage.disks[] | select(.id == "bcache0")')" != "" ]

  # Get the bcache cache and backing devices.
  cache_device_base="$(basename "${loop_device_1}")"
  backing_device_base="$(basename "${loop_device_2}")"
  cache_device="$(< "/sys/block/${cache_device_base}/dev")"
  backing_device="$(< "/sys/block/${backing_device_base}/dev")"

  # Check the devices are actually used for the bcache device as cache and backing.
  [ "$(< "/sys/block/bcache0/slaves/${cache_device_base}/dev")" = "${cache_device}" ]
  [ "$(< "/sys/block/bcache0/slaves/${backing_device_base}/dev")" = "${backing_device}" ]

  # Check the devices are marked in use by bcache.
  # The actual bcache device should report an unset 'used_by' field.
  [ "$(lxc query /1.0/resources | jq -r '.storage.disks[] | select(.id == "bcache0") | .used_by')" = "null" ]
  [ "$(lxc query /1.0/resources | jq -r '.storage.disks[] | select(.device == "'"${cache_device}"'") | .used_by')" = "bcache" ]
  [ "$(lxc query /1.0/resources | jq -r '.storage.disks[] | select(.device == "'"${backing_device}"'") | .used_by')" = "bcache" ]

  # Cleanup
  echo 1 > /sys/block/bcache0/bcache/stop
  for i in /sys/fs/bcache/*; do echo 1 > "$i/stop" 2>/dev/null || true; done
  # shellcheck disable=SC2154
  deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
  # shellcheck disable=SC2154
  deconfigure_loop_device "${loop_file_2}" "${loop_device_2}"
}

