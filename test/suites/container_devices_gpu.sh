gpu_get_first_card() {
  gpuCardDev="$(find /dev/dri -maxdepth 1 -name 'card*' 2>/dev/null | sort | head -n1)"
  [ -n "${gpuCardDev}" ] || return 1

  gpuCardName="$(basename "${gpuCardDev}")"
  gpuCardIndex="${gpuCardName#card}"
  if ! echo "${gpuCardIndex}" | grep -Eq '^[0-9]+$'; then
    return 1
  fi

  echo "${gpuCardName}"
}

gpu_run_basic_validation() {
  echo "==> Running basic GPU device validation tests"
  local ctName="$1"

  lxc init --empty "${ctName}"
  ! lxc config device add "${ctName}" gpu-basic gpu id=foo || false
  ! lxc config device add "${ctName}" gpu-basic gpu id=foo.com/gpu=0 || false

  lxc config device add "${ctName}" gpu-basic gpu id=nvidia.com/gpu=0
  lxc config device remove "${ctName}" gpu-basic

  lxc config device add "${ctName}" gpu-basic gpu id=amd.com/gpu=0
  lxc config device remove "${ctName}" gpu-basic

  lxc delete "${ctName}"
}

gpu_run_generic_tests() {
  echo "==> Running generic GPU device tests"
  local ctName="$1"
  local gpuCardName="$2"
  local gpuCardIndex="$3"

  # Check adding a card creates the correct device mounts and cleans up on removal.
  startMountCount=$(lxc exec "${ctName}" -- mount | wc -l)
  startDevCount=$(find "${LXD_DIR}"/devices/"${ctName}" -type c | wc -l)
  lxc config device add "${ctName}" gpu-basic gpu mode=0600 id="${gpuCardIndex}"
  lxc exec "${ctName}" -- mount | grep -wF "/dev/dri/${gpuCardName}"
  [ "$(lxc exec "${ctName}" -- stat -c '%a' /dev/dri/"${gpuCardName}")" = "600" ]
  [ "$(stat -c '%a' "${LXD_DIR}"/devices/"${ctName}"/unix.gpu--basic.dev-dri-"${gpuCardName}")" = "600" ]
  lxc config device remove "${ctName}" gpu-basic
  endMountCount=$(lxc exec "${ctName}" -- mount | wc -l)
  endDevCount=$(find "${LXD_DIR}"/devices/"${ctName}" -type c | wc -l)

  if [ "$startMountCount" != "$endMountCount" ]; then
    echo "leftover container mounts detected"
    false
  fi

  if [ "$startDevCount" != "$endDevCount" ]; then
    echo "leftover host devices detected"
    false
  fi

  # Check adding non-existent card fails.
  ! lxc config device add "${ctName}" gpu-missing gpu id=9999 || false

  # Check default create mode is 0660.
  lxc config device add "${ctName}" gpu-default gpu
  [ "$(lxc exec "${ctName}" -- stat -c '%a' /dev/dri/"${gpuCardName}")" = "660" ]
  lxc config device remove "${ctName}" gpu-default
}

gpu_run_nvidia_legacy_tests() {
  echo "==> Running NVIDIA legacy GPU device tests"
    # Check if nvidia GPU exists.
  if [ ! -c /dev/nvidia0  ]; then
    echo "==> SKIP: /dev/nvidia0 does not exist, skipping nvidia legacy tests"
    return
  fi

  # Check support for nvidia runtime
  lxc stop -f "${ctName}"
  lxc config set "${ctName}" nvidia.runtime true
  lxc start "${ctName}"
  # Instead of relying on an exact mount count (which can vary across
  # environments/drivers), verify that the important NVIDIA-related mount
  # points are present in the container's mount table.
  nvidiaMounts="$(lxc exec "${ctName}" -- mount | grep -F nvidia || true)"

  if [ -z "${nvidiaMounts}" ]; then
    echo "nvidia runtime mounts invalid: no nvidia mounts found"
    false
  fi

  missing=0
  for req in "/dev/nvidia-uvm" "/dev/nvidia-uvm-tools" "/dev/nvidiactl"; do
    if ! echo "${nvidiaMounts}" | grep -qF "${req}"; then
      echo "nvidia runtime mount missing: ${req}"
      missing=$((missing+1))
    fi
  done

  if [ "${missing}" -ne 0 ]; then
    echo "nvidia runtime mounts invalid (missing ${missing} required entries):"
    echo "${nvidiaMounts}"
    false
  fi

  lxc stop -f "${ctName}"
  lxc config set "${ctName}" nvidia.runtime false
  lxc start "${ctName}"
}

gpu_verify_nvidia_mounts() {
  local ctName="$1"

  lxc exec "${ctName}" -- mount | grep -E '/dev/dri/card[0-9]+'
  lxc exec "${ctName}" -- mount | grep -wF /dev/nvidia0
  lxc exec "${ctName}" -- mount | grep -wF /dev/nvidia-modeset
  lxc exec "${ctName}" -- mount | grep -wF /dev/nvidia-uvm
  lxc exec "${ctName}" -- mount | grep -wF /dev/nvidia-uvm-tools
  lxc exec "${ctName}" -- mount | grep -wF /dev/nvidiactl
}

gpu_run_nvidia_tests() {
  echo "==> Running NVIDIA GPU device tests"
  local ctName="$1"
  local gpuCardName="$2"

  # Check if nvidia GPU exists.
  if [ ! -c /dev/nvidia0  ]; then
    echo "==> SKIP: /dev/nvidia0 does not exist, skipping nvidia tests"
    return
  fi

  # Check nvidia-container-cli exists (requires libnvidia-container-tools be installed).
  if ! command -v nvidia-container-cli > /dev/null 2>&1; then
    echo "==> SKIP: nvidia-container-cli not available (please install libnvidia-container-tools)"
    return
  fi

  # Check the Nvidia specific devices are mounted correctly.
  lxc stop -f "${ctName}"
  lxc config device add "${ctName}" gpu-cdi gpu id=nvidia.com/gpu=0
  lxc start "${ctName}"

  gpu_verify_nvidia_mounts "${ctName}"

  lxc stop -f "${ctName}"
  lxc config device remove "${ctName}" gpu-cdi
  lxc start "${ctName}"

  lxc stop -f "${ctName}"
  lxc config device add "${ctName}" gpu-cdi gpu id=nvidia.com/gpu=all
  lxc start "${ctName}"

  gpu_verify_nvidia_mounts "${ctName}"

  lxc stop -f "${ctName}"
  lxc config device remove "${ctName}" gpu-cdi
  lxc start "${ctName}"
}

gpu_run_amd_tests() {
  echo "==> Running AMD GPU device tests"
  local ctName="$1"
  local amdDeviceName="$2"

  if ! command -v amd-ctk > /dev/null 2>&1; then
    echo "==> SKIP: amd-ctk not available (please install amd-container-toolkit)"
    return
  fi

  # Check if AMD GPU exists.
  if [ ! -c /dev/kfd ]; then
    echo "==> SKIP: /dev/kfd does not exist, skipping AMD tests"
    return
  fi

  lxc stop -f "${ctName}"
  lxc config device add "${ctName}" gpu-amd gpu id=amd.com/gpu=0
  lxc start "${ctName}"

  lxc exec "${ctName}" -- mount | grep -wF /dev/kfd
  lxc exec "${ctName}" -- mount | grep -wF "${amdDeviceName}"

  hostKfdUid="$(stat -c '%u' /dev/kfd)"
  hostKfdGid="$(stat -c '%g' /dev/kfd)"

  [ "$(lxc exec "${ctName}" -- stat -c '%u' /dev/kfd)" = "${hostKfdUid}" ]
  [ "$(lxc exec "${ctName}" -- stat -c '%g' /dev/kfd)" = "${hostKfdGid}" ]

  hostCardUid="$(stat -c '%u' /dev/dri/"${amdDeviceName}")"
  hostCardGid="$(stat -c '%g' /dev/dri/"${amdDeviceName}")"

  [ "$(lxc exec "${ctName}" -- stat -c '%u' /dev/dri/"${amdDeviceName}")" = "${hostCardUid}" ]
  [ "$(lxc exec "${ctName}" -- stat -c '%g' /dev/dri/"${amdDeviceName}")" = "${hostCardGid}" ]

  lxc restart "${ctName}"

  lxc stop -f "${ctName}"
  lxc config device remove "${ctName}" gpu-amd
  lxc start "${ctName}"

  lxc stop -f "${ctName}"
  lxc config device add "${ctName}" gpu-amd gpu id=amd.com/gpu=all
  lxc start "${ctName}"

  lxc exec "${ctName}" -- mount | grep -wF /dev/kfd
  lxc exec "${ctName}" -- mount | grep -wF "${amdDeviceName}"

  lxc stop -f "${ctName}"
  lxc config device remove "${ctName}" gpu-amd
  lxc start "${ctName}"
}

test_container_devices_gpu() {
  ctName="ct$$"

  gpu_run_basic_validation "${ctName}"

  gpuCardName="$(gpu_get_first_card)" || {
    echo "==> SKIP: No /dev/dri/card* device found"
    return
  }
  gpuCardIndex="${gpuCardName#card}"

  ensure_import_testimage
  lxc launch testimage "${ctName}"

  gpu_run_generic_tests "${ctName}" "${gpuCardName}" "${gpuCardIndex}"
  gpu_run_nvidia_tests "${ctName}" "${gpuCardName}"
  gpu_run_nvidia_legacy_tests "${ctName}"
  gpu_run_amd_tests "${ctName}" "${gpuCardName}"

  lxc delete -f "${ctName}"
}
