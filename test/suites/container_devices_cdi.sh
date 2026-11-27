test_container_devices_cdi_amd() {
  if ! command -v amd-ctk >/dev/null 2>&1; then
    # shellcheck disable=SC2034 # Read by the test harness after this function returns.
    TEST_UNMET_REQUIREMENT="amd-ctk is not available"
    return
  fi

  cdiCtName="cdi-amd-ct${$}"
  cdiFixtureDir="$(mktemp -d -p "${TEST_DIR}" lxd-cdi-amd-XXX)"
  cdiSpecDir="/var/run/cdi"
  cdiSpecFile="${cdiSpecDir}/lxd-${cdiCtName}.json"
  cdiDeviceName="lxd-test-amd-${$}"

  container_devices_cdi_cleanup() {
    lxc delete -f "${cdiCtName}" >/dev/null 2>&1 || true
    rm -f "${cdiSpecFile}" "${cdiSpecFile}.tmp" 2>/dev/null || true
    rm -rf -- "${cdiFixtureDir}" 2>/dev/null || true
  }

  local orig_exit_trap
  orig_exit_trap=$(trap -p EXIT | sed -E "s/^trap -- '(.*)' EXIT$/\1/")
  # shellcheck disable=SC2064 # Expand the captured harness trap now, not when signalled.
  trap "container_devices_cdi_cleanup; ${orig_exit_trap}" EXIT

  sub_test "Generate a CPU-only AMD CDI spec"
  mkdir -p "${cdiSpecDir}"
  mknod -m 0666 "${cdiFixtureDir}/card1" c 1 3
  mknod -m 0666 "${cdiFixtureDir}/renderD128" c 1 3
  mknod -m 0666 "${cdiFixtureDir}/kfd" c 1 3
  # shellcheck disable=SC2016 # The output path is expanded by the nested shell.
  unshare --mount bash -c '
    set -eu
    mount -t tmpfs tmpfs /sys
    mkdir -p \
      /sys/module/amdgpu/drivers/pci:amdgpu/0000:01:00.0/drm/card1 \
      /sys/module/amdgpu/drivers/pci:amdgpu/0000:01:00.0/drm/renderD128 \
      /sys/class/kfd/kfd/topology/nodes/1
    printf "unique_id 1234\ndrm_render_minor 128\n" \
      > /sys/class/kfd/kfd/topology/nodes/1/properties
    amd-ctk cdi generate --output="$1"
  ' -- "${cdiSpecFile}"

  jq --exit-status '.kind == "amd.com/gpu"' "${cdiSpecFile}" >/dev/null
  jq --exit-status '.devices[] | select(.name == "0") | (.containerEdits.deviceNodes | length) == 3' "${cdiSpecFile}" >/dev/null

  # amd-ctk v1.3.0 has no synthetic-device or CSV mode. Keep its generated
  # container paths and map them to safe temporary host nodes for the test.
  jq --exit-status '
    .devices |= [
      .[] |
      select(.name == "0") |
      .name = "'"${cdiDeviceName}"'" |
      .containerEdits.deviceNodes[] |= (
        (if .path == "/dev/dri/card1" then . + {"hostPath": "'"${cdiFixtureDir}/card1"'"}
        elif .path == "/dev/dri/renderD128" then . + {"hostPath": "'"${cdiFixtureDir}/renderD128"'"}
        elif .path == "/dev/kfd" then . + {"hostPath": "'"${cdiFixtureDir}/kfd"'"}
        else .
        end) | del(.major, .minor)
      )
    ]
  ' "${cdiSpecFile}" > "${cdiSpecFile}.tmp"
  mv "${cdiSpecFile}.tmp" "${cdiSpecFile}"

  sub_test "Boot a container with the generated AMD CDI spec"
  ensure_import_testimage
  lxc init testimage "${cdiCtName}"
  lxc config device add "${cdiCtName}" gpu-cdi gpu gputype=physical id="amd.com/gpu=${cdiDeviceName}"
  lxc start "${cdiCtName}"

  sub_test "Verify AMD CDI devices inside the container"
  for device in /dev/dri/card1 /dev/dri/renderD128 /dev/kfd; do
    lxc exec "${cdiCtName}" -- test -c "${device}"
    [ "$(lxc exec "${cdiCtName}" -- stat -c '%t:%T' "${device}")" = "1:3" ]
  done

  container_devices_cdi_cleanup
  unset -f container_devices_cdi_cleanup
  # shellcheck disable=SC2064 # Restore the captured harness trap.
  trap "${orig_exit_trap}" EXIT
}

test_container_devices_cdi_nvidia() {
  if ! command -v nvidia-ctk >/dev/null 2>&1; then
    # shellcheck disable=SC2034 # Read by the test harness after this function returns.
    TEST_UNMET_REQUIREMENT="nvidia-ctk is not available"
    return
  fi

  cdiCtName="cdi-nvidia-ct${$}"
  cdiFixtureDir="$(mktemp -d -p "${TEST_DIR}" lxd-cdi-nvidia-XXX)"
  cdiSpecDir="/var/run/cdi"
  cdiSpecFile="${cdiSpecDir}/lxd-${cdiCtName}.json"
  cdiDeviceName="lxd-test-nvidia-${$}"

  container_devices_cdi_cleanup() {
    lxc delete -f "${cdiCtName}" >/dev/null 2>&1 || true
    rm -f "${cdiSpecFile}" "${cdiSpecFile}.tmp" 2>/dev/null || true
    rm -rf -- "${cdiFixtureDir}" 2>/dev/null || true
  }

  local orig_exit_trap
  orig_exit_trap=$(trap -p EXIT | sed -E "s/^trap -- '(.*)' EXIT$/\1/")
  # shellcheck disable=SC2064 # Expand the captured harness trap now, not when signalled.
  trap "container_devices_cdi_cleanup; ${orig_exit_trap}" EXIT

  sub_test "Generate a CPU-only NVIDIA CDI spec"
  mkdir -p "${cdiSpecDir}" "${cdiFixtureDir}/mount-dir"
  printf 'file-mount-from-cdi\n' > "${cdiFixtureDir}/mount-file"
  printf 'directory-mount-from-cdi\n' > "${cdiFixtureDir}/mount-dir/value"
  printf 'symlink-target-from-cdi\n' > "${cdiFixtureDir}/mount-target"
  ln -s mount-target "${cdiFixtureDir}/mount-link"
  mknod -m 0666 "${cdiFixtureDir}/test-device" c 1 3

  cat > "${cdiFixtureDir}/mounts.csv" <<EOF
dev, /dev/null
dev, ${cdiFixtureDir}/test-device
lib, ${cdiFixtureDir}/mount-file
dir, ${cdiFixtureDir}/mount-dir
sym, ${cdiFixtureDir}/mount-link
EOF

  nvidia-ctk cdi generate \
    --mode=csv \
    --csv.file="${cdiFixtureDir}/mounts.csv" \
    --device-name-strategy=index \
    --class=gpu \
    --format=json \
    --output="${cdiSpecFile}"

  jq --exit-status '.kind == "nvidia.com/gpu"' "${cdiSpecFile}" >/dev/null
  jq --exit-status '.devices[] | select(.name == "0") | (.containerEdits.deviceNodes | length) == 2' "${cdiSpecFile}" >/dev/null
  jq --exit-status '.containerEdits.env | index("NVIDIA_VISIBLE_DEVICES=void") != null' "${cdiSpecFile}" >/dev/null
  jq --exit-status '.containerEdits.mounts | any(.hostPath == "/dev/null" and .containerPath == "/var/run/nvidia-container-devices/void")' "${cdiSpecFile}" >/dev/null

  # CSV mode does not emit ordinary container environment edits. Add one to
  # exercise environment translation while retaining all generated edits.
  jq --exit-status '
    .devices |= [.[] | select(.name == "0") | .name = "'"${cdiDeviceName}"'"] |
    .containerEdits.env += ["LXD_CDI_TEST_ENV=generated-spec"]
  ' "${cdiSpecFile}" > "${cdiSpecFile}.tmp"
  mv "${cdiSpecFile}.tmp" "${cdiSpecFile}"
  jq --exit-status '.containerEdits.env | index("LXD_CDI_TEST_ENV=generated-spec") != null' "${cdiSpecFile}" >/dev/null

  sub_test "Boot a container with the generated CDI spec"
  ensure_import_testimage
  lxc init testimage "${cdiCtName}"

  # Match regular distribution images where /var/run resolves through /run.
  lxc start "${cdiCtName}"
  lxc exec "${cdiCtName}" -- sh -c 'mkdir -p /var /run && rm -rf /var/run && ln -s /run /var/run'
  [ "$(lxc exec "${cdiCtName}" -- readlink /var/run)" = "/run" ]
  lxc stop "${cdiCtName}"

  lxc config device add "${cdiCtName}" gpu-cdi gpu gputype=physical id="nvidia.com/gpu=${cdiDeviceName}"
  lxc start "${cdiCtName}"

  sub_test "Verify CDI edits inside the container"
  [ "$(lxc exec "${cdiCtName}" -- readlink /var/run)" = "/run" ]
  [ "$(lxc exec "${cdiCtName}" -- stat -c '%F %t:%T' "${cdiFixtureDir}/test-device")" = "character special file 1:3" ]
  [ "$(lxc exec "${cdiCtName}" -- cat "${cdiFixtureDir}/mount-file")" = "file-mount-from-cdi" ]
  [ "$(lxc exec "${cdiCtName}" -- cat "${cdiFixtureDir}/mount-dir/value")" = "directory-mount-from-cdi" ]
  [ "$(lxc exec "${cdiCtName}" -- readlink "${cdiFixtureDir}/mount-link")" = "mount-target" ]
  [ "$(lxc exec "${cdiCtName}" -- cat "${cdiFixtureDir}/mount-link")" = "symlink-target-from-cdi" ]
  lxc exec "${cdiCtName}" -- env | grep -xF "LXD_CDI_TEST_ENV=generated-spec"

  mountinfo="$(lxc exec "${cdiCtName}" -- cat /proc/self/mountinfo)"
  echo "${mountinfo}" | grep -wF "${cdiFixtureDir}/mount-file"
  echo "${mountinfo}" | grep -wF "${cdiFixtureDir}/mount-dir"

  if lxc exec "${cdiCtName}" -- test -e /run/nvidia-container-devices/void; then
    echo "NVIDIA runtime void mount was applied"
    false
  fi

  if lxc exec "${cdiCtName}" -- env | grep -wF "NVIDIA_VISIBLE_DEVICES=void"; then
    echo "NVIDIA runtime environment marker was applied"
    false
  fi

  container_devices_cdi_cleanup
  unset -f container_devices_cdi_cleanup
  # shellcheck disable=SC2064 # Restore the captured harness trap.
  trap "${orig_exit_trap}" EXIT
}
