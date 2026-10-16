# MicroVM test helper to check pre-conditions.
# Sets TEST_UNMET_REQUIREMENT and returns 1 if the host cannot run MicroVMs.
_microvm_check_prerequisites() {
  if [ ! -e "/dev/kvm" ]; then
    export TEST_UNMET_REQUIREMENT="MicroVM tests require KVM support (/dev/kvm missing)"
    return 1
  fi

  if [ ! -f "${LXD_DIR}/microvm/vmlinuz" ] && [ ! -f "${LXD_DIR}/microvm/vmlinux" ]; then
    export TEST_UNMET_REQUIREMENT="MicroVM kernel not found, set LXD_MICROVM_KERNEL"
    return 1
  fi

  # libkrun is taken from LIBKRUN_PATH, then from ${LXD_DIR}/libkrun/, then from the system library path.
  local has_libkrun=false
  if [ -f "${LIBKRUN_PATH:-}" ] || [ -f "${LXD_DIR}/libkrun/libkrun.so" ]; then
    has_libkrun=true
  elif ldconfig -p 2>/dev/null | grep -E '\slibkrun\.so(\.[0-2])? ' >/dev/null; then
    has_libkrun=true
  fi

  if [ "${has_libkrun}" = "false" ]; then
    export TEST_UNMET_REQUIREMENT="libkrun not found, install it or set LIBKRUN_PATH"
    return 1
  fi

  # Storage drivers that list the MicroVM volume type.
  case "$(storage_backend "${LXD_DIR}")" in
    dir|btrfs|zfs|lvm|ceph) ;;
    *)
      export TEST_UNMET_REQUIREMENT="MicroVM tests require the dir, btrfs, zfs, lvm or ceph storage backend"
      return 1
      ;;
  esac

  return 0
}

# Waits for a MicroVM to reach the expected status.
_microvm_wait_status() {
  local name="${1}"
  local expected="${2}"
  local status=""

  for _ in $(seq 120); do
    status="$(lxc list -f csv -c s "${name}")"
    if [ "${status}" = "${expected}" ]; then
      return 0
    fi

    sleep 0.5
  done

  echo "ERROR: expected ${name} to be ${expected}, got ${status}"
  return 1
}

test_microvm_feature_preview() {
  local LXD_DIR
  LXD_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"

  sub_test "Verify the client hides --microvm when the feature preview is disabled"
  ! LXD_FEATURES="" lxc init --help | grep -wF -- "--microvm" || false
  LXD_FEATURES="microvm" lxc init --help | grep -wF -- "--microvm" >/dev/null
  [[ "$(! LXD_FEATURES="" "${_LXC}" init testimage m-gated --microvm 2>&1 1>/dev/null)" == *"unknown flag: --microvm"* ]]

  sub_test "Verify the daemon rejects MicroVMs when the feature preview is disabled"
  LXD_FEATURES="" spawn_lxd "${LXD_DIR}" true
  ensure_import_testimage
  [[ "$(! LXD_FEATURES="microvm" "${_LXC}" init testimage m-gated --microvm 2>&1 1>/dev/null)" == *'Server does not support MicroVM instances: The server is missing the required "instance_microvm" API extension'* ]]
  ! lxc info m-gated || false

  kill_lxd "${LXD_DIR}"
}

test_microvm_lifecycle() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  sub_test "Test MicroVM init and status reporting"
  lxc init testimage m1 --microvm -c limits.memory=512MiB
  [ "$(lxc list -f csv -c ts m1)" = "MICROVM,STOPPED" ]
  [ -d "${LXD_DIR}/containers/m1" ]

  sub_test "Test config update while stopped"
  lxc config set m1 limits.memory=1GiB limits.cpu=2
  [ "$(lxc config get m1 limits.memory)" = "1GiB" ]
  [ "$(lxc config get m1 limits.cpu)" = "2" ]

  sub_test "Test rename while stopped"
  lxc rename m1 m2
  [ "$(lxc list -f csv -c ts m2)" = "MICROVM,STOPPED" ]
  ! lxc info m1 || false

  sub_test "Test MicroVM storage volume reporting"
  local pool_name
  pool_name="$(lxc profile device get default root pool)"
  lxc storage volume list "${pool_name}" -f csv -c t,n | grep -xF "microvm,m2"
  lxc storage volume show "${pool_name}" microvm/m2 | grep -xF "type: microvm"

  sub_test "Test start protection"
  lxc config set m2 security.protection.start=true
  [[ "$(! "${_LXC}" start m2 2>&1 1>/dev/null)" == *"Instance is protected from being started"* ]]
  [ "$(lxc list -f csv -c s m2)" = "STOPPED" ]
  lxc config unset m2 security.protection.start

  sub_test "Test start and running state"
  lxc start m2
  [ "$(lxc list -f csv -c s m2)" = "RUNNING" ]
  lxc query /1.0/instances/m2/state | jq --exit-status '.pid > 0'

  sub_test "Test libkrun helper process and log file"
  local pid_before
  pid_before="$(< "${LXD_DIR}/logs/m2/libkrun.pid")"
  [ -n "${pid_before}" ]
  kill -0 "${pid_before}"
  grep -aF forklibkrun "/proc/${pid_before}/cmdline" >/dev/null
  [ -f "${LXD_DIR}/logs/m2/microvm.log" ]

  sub_test "Test microvm.conf is generated and exposed through the logs API"
  [ -f "${LXD_DIR}/logs/m2/microvm.conf" ]
  curl --unix-socket "${LXD_DIR}/unix.socket" "http://localhost/1.0/instances/m2/logs/microvm.conf" | jq --exit-status '.cpus == 2 and .memory_mib == 1024 and .kernel.format == "auto"'
  lxc query /1.0/instances/m2/logs | jq --exit-status 'any(.[]; endswith("/microvm.conf"))'
  lxc query /1.0/instances/m2/logs | jq --exit-status 'any(.[]; endswith("/microvm.log"))'
  lxc info m2 --show-log >/dev/null

  sub_test "Test operations rejected on a running MicroVM"
  [[ "$(! "${_LXC}" start m2 2>&1 1>/dev/null)" == *"The instance is already running"* ]]
  [[ "$(! "${_LXC}" config set m2 limits.memory=2GiB 2>&1 1>/dev/null)" == *'Key "limits.memory" cannot be updated when VM is running'* ]]
  [[ "$(! "${_LXC}" config set m2 limits.cpu=1 2>&1 1>/dev/null)" == *'Key "limits.cpu" cannot be updated when VM is running'* ]]
  [ "$(lxc config get m2 limits.memory)" = "1GiB" ]
  ! lxc rename m2 m3 || false
  ! lxc delete m2 || false
  [ "$(lxc list -f csv -c s m2)" = "RUNNING" ]

  sub_test "Test graceful stop and restart require lxd-agent"
  # testimage has no lxd-agent, so the guest cannot be asked to power off.
  [[ "$(! "${_LXC}" stop m2 --timeout 10 2>&1 1>/dev/null)" == *"Failed requesting guest power off"* ]]
  [[ "$(! "${_LXC}" restart m2 --timeout 10 2>&1 1>/dev/null)" == *"Failed requesting guest power off"* ]]
  [ "$(lxc list -f csv -c s m2)" = "RUNNING" ]
  [ "$(< "${LXD_DIR}/logs/m2/libkrun.pid")" = "${pid_before}" ]

  sub_test "Test restart spawns a new libkrun helper process"
  lxc restart -f m2
  [ "$(lxc list -f csv -c s m2)" = "RUNNING" ]
  local pid_after
  pid_after="$(< "${LXD_DIR}/logs/m2/libkrun.pid")"
  [ -n "${pid_after}" ]
  [ "${pid_before}" != "${pid_after}" ]
  ! kill -0 "${pid_before}" 2>/dev/null || false

  sub_test "Test force stop cleans up the libkrun helper process"
  lxc stop -f m2
  [ "$(lxc list -f csv -c s m2)" = "STOPPED" ]
  ! kill -0 "${pid_after}" 2>/dev/null || false
  [[ "$(! "${_LXC}" stop -f m2 2>&1 1>/dev/null)" == *"The instance is already stopped"* ]]
  [ -f "${LXD_DIR}/logs/m2/microvm.conf" ]

  sub_test "Test start and delete while running with --force"
  lxc start m2
  [ "$(lxc list -f csv -c s m2)" = "RUNNING" ]
  lxc delete -f m2
  ! lxc info m2 || false
  [ ! -d "${LXD_DIR}/logs/m2" ]
  [ ! -e "${LXD_DIR}/containers/m2" ]
}

test_microvm_missing_kernel() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  local kernel="${LXD_DIR}/microvm/vmlinuz"
  if [ ! -f "${kernel}" ] && [ -f "${LXD_DIR}/microvm/vmlinux" ]; then
    kernel="${LXD_DIR}/microvm/vmlinux"
  fi
  lxc init testimage m-nokernel --microvm

  sub_test "Test start fails cleanly when the MicroVM kernel is missing"
  mv "${kernel}" "${kernel}.bak"
  [[ "$(! "${_LXC}" start m-nokernel 2>&1 1>/dev/null)" == *"Kernel not found"* ]]
  [ "$(lxc list -f csv -c s m-nokernel)" = "STOPPED" ]
  mv "${kernel}.bak" "${kernel}"

  sub_test "Test start succeeds once the kernel is restored"
  lxc start m-nokernel
  [ "$(lxc list -f csv -c s m-nokernel)" = "RUNNING" ]
  lxc delete -f m-nokernel
}

test_microvm_devices_disk() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  local share="${TEST_DIR}/m-disk-share"
  mkdir -p "${share}"

  lxc init testimage m-disk --microvm

  sub_test "Test a disk other than the root disk is refused"
  [[ "$(! "${_LXC}" config device add m-disk data1 disk source="${share}" path=/mnt/data1 2>&1 1>/dev/null)" == *"Unsupported device type"* ]]
  [[ "$(! "${_LXC}" config device add m-disk cloud disk source=cloud-init:config 2>&1 1>/dev/null)" == *"Unsupported device type"* ]]

  sub_test "Test a profile disk other than the root disk is skipped"
  lxc profile create m-disk-profile
  lxc profile device add m-disk-profile data1 disk source="${share}" path=/mnt/data1
  lxc profile add m-disk m-disk-profile
  lxc start m-disk
  [ "$(lxc list -f csv -c s m-disk)" = "RUNNING" ]
  [ ! -e "${LXD_DIR}/containers/m-disk/config/agent-mounts.json" ]

  lxc delete -f m-disk
  lxc profile delete m-disk-profile
  rm -rf "${share}"
}

test_microvm_devices_nic() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  local br_name="lxdm$$"
  lxc network create "${br_name}" ipv4.address=none ipv6.address=none

  sub_test "Test MicroVM bridged NIC device attachment"
  lxc init testimage m-nic --microvm
  lxc config device add m-nic eth0 nic network="${br_name}" name=eth0

  lxc start m-nic
  [ "$(lxc list -f csv -c s m-nic)" = "RUNNING" ]

  sub_test "Test the host side of the NIC is attached to the bridge"
  local host_name
  host_name="$(lxc config get m-nic volatile.eth0.host_name)"
  [ -n "${host_name}" ]
  [ "$(< "/sys/class/net/${host_name}/master/ifindex")" = "$(< "/sys/class/net/${br_name}/ifindex")" ]
  [ -n "$(lxc config get m-nic volatile.eth0.hwaddr)" ]

  sub_test "Test devices cannot be added to or removed from a running MicroVM"
  lxc network create "${br_name}b" ipv4.address=none ipv6.address=none
  [[ "$(! "${_LXC}" config device add m-nic eth1 nic network="${br_name}b" name=eth1 2>&1 1>/dev/null)" == *"Devices cannot be added to a running MicroVM"* ]]
  [[ "$(! "${_LXC}" config device remove m-nic eth0 2>&1 1>/dev/null)" == *"Devices cannot be removed from a running MicroVM"* ]]
  [ "$(lxc list -f csv -c s m-nic)" = "RUNNING" ]
  [ -e "/sys/class/net/${host_name}" ]

  sub_test "Test the host side of the NIC is removed on stop"
  lxc stop -f m-nic
  [ ! -e "/sys/class/net/${host_name}" ]

  lxc delete m-nic
  lxc network delete "${br_name}"
  lxc network delete "${br_name}b"
}

test_microvm_negative() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  lxc init testimage m-neg --microvm
  lxc start m-neg
  [ "$(lxc list -f csv -c s m-neg)" = "RUNNING" ]

  sub_test "Unsupported operations return errors on running instance"
  [[ "$(! "${_LXC}" pause m-neg 2>&1 1>/dev/null)" == *"Not supported"* ]]
  [ "$(lxc list -f csv -c s m-neg)" = "RUNNING" ]
  [[ "$(! "${_LXC}" snapshot m-neg snap0 2>&1 1>/dev/null)" == *"Not supported"* ]]
  [[ "$(! "${_LXC}" stop --stateful m-neg 2>&1 1>/dev/null)" == *"Stateful stop is not supported for MicroVM instances"* ]]
  [ "$(lxc list -f csv -c s m-neg)" = "RUNNING" ]
  [[ "$(! "${_LXC}" file pull m-neg/etc/hostname - 2>&1 1>/dev/null)" == *"Not supported"* ]]

  lxc stop -f m-neg

  sub_test "Unsupported operations return errors on stopped instance"
  [[ "$(! "${_LXC}" snapshot m-neg snap0 2>&1 1>/dev/null)" == *"Not supported"* ]]
  lxc query /1.0/instances/m-neg/snapshots | jq --exit-status 'length == 0'
  [[ "$(! "${_LXC}" rebuild testimage m-neg 2>&1 1>/dev/null)" == *"Not supported"* ]]
  [[ "$(! "${_LXC}" export m-neg "${TEST_DIR}/m-neg.tar.gz" 2>&1 1>/dev/null)" == *"Create backup"* ]]
  lxc query /1.0/instances/m-neg/backups | jq --exit-status 'length == 0'
  rm -f "${TEST_DIR}/m-neg.tar.gz"

  sub_test "The root disk size cannot be changed after creation"
  [[ "$(! "${_LXC}" config device override m-neg root size=20GiB 2>&1 1>/dev/null)" == *"The root disk size of a MicroVM cannot be changed after creation"* ]]
  lxc query /1.0/instances/m-neg | jq --exit-status '.devices == {}'

  lxc delete m-neg
}

test_microvm_guest_console() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  lxc init testimage m-guest --microvm -c limits.memory=256MiB
  lxc start m-guest
  [ "$(lxc list -f csv -c s m-guest)" = "RUNNING" ]

  sub_test "Test the guest boots and answers on the console"
  # shellcheck disable=SC2016 # The arithmetic is expanded by the guest shell.
  microvm_console_socket_run m-guest 'echo booted-$((40+2))' "booted-42"

  sub_test "Test a reboot from inside the guest restarts the MicroVM"
  local pid_before pid_after
  pid_before="$(< "${LXD_DIR}/logs/m-guest/libkrun.pid")"
  microvm_console_socket_run m-guest "reboot -f"

  pid_after="${pid_before}"
  for _ in $(seq 120); do
    pid_after="$(cat "${LXD_DIR}/logs/m-guest/libkrun.pid" 2>/dev/null || true)"
    if [ -n "${pid_after}" ] && [ "${pid_after}" != "${pid_before}" ]; then
      break
    fi

    sleep 0.5
  done

  [ -n "${pid_after}" ]
  [ "${pid_after}" != "${pid_before}" ]
  ! kill -0 "${pid_before}" 2>/dev/null || false
  _microvm_wait_status m-guest RUNNING
  # shellcheck disable=SC2016 # The arithmetic is expanded by the guest shell.
  microvm_console_socket_run m-guest 'echo rebooted-$((40+2))' "rebooted-42"

  sub_test "Test a poweroff from inside the guest stops the MicroVM"
  microvm_console_socket_run m-guest "poweroff -f"
  _microvm_wait_status m-guest STOPPED
  ! kill -0 "${pid_after}" 2>/dev/null || false
  [ "$(lxc config get m-guest volatile.last_state.power)" = "STOPPED" ]
  [ ! -e "${LXD_DIR}/logs/m-guest/libkrun.pid" ]

  lxc delete m-guest
}

test_microvm_unexpected_exit() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  local br_name="lxdm$$"
  lxc network create "${br_name}" ipv4.address=none ipv6.address=none

  lxc init testimage m-crash --microvm -c limits.memory=256MiB
  lxc config device add m-crash eth0 nic network="${br_name}" name=eth0
  lxc start m-crash
  [ "$(lxc list -f csv -c s m-crash)" = "RUNNING" ]

  sub_test "Test an unexpected libkrun helper exit stops the instance and cleans up"
  local pid host_name
  pid="$(< "${LXD_DIR}/logs/m-crash/libkrun.pid")"
  host_name="$(lxc config get m-crash volatile.eth0.host_name)"
  [ -e "/sys/class/net/${host_name}" ]

  kill -9 "${pid}"
  _microvm_wait_status m-crash STOPPED

  # The cleanup runs in the background once the helper exit is noticed.
  for _ in $(seq 60); do
    if [ "$(lxc config get m-crash volatile.last_state.power)" = "STOPPED" ] && [ ! -e "/sys/class/net/${host_name}" ]; then
      break
    fi

    sleep 0.5
  done

  [ "$(lxc config get m-crash volatile.last_state.power)" = "STOPPED" ]
  [ ! -e "/sys/class/net/${host_name}" ]
  [ ! -e "${LXD_DIR}/logs/m-crash/libkrun.pid" ]
  [ ! -e "${LXD_DIR}/logs/m-crash/libkrun.console" ]
  # The stop operation, which reports RUNNING, ends after the storage is unmounted.
  _microvm_wait_status m-crash STOPPED

  sub_test "Test the instance starts again after an unexpected exit"
  lxc start m-crash
  [ "$(lxc list -f csv -c s m-crash)" = "RUNNING" ]
  [ "$(< "${LXD_DIR}/logs/m-crash/libkrun.pid")" != "${pid}" ]
  lxc delete -f m-crash

  sub_test "Test an ephemeral MicroVM is deleted when stopped"
  lxc init testimage m-eph --microvm --ephemeral -c limits.memory=256MiB
  lxc start m-eph
  [ "$(lxc list -f csv -c s m-eph)" = "RUNNING" ]
  lxc stop -f m-eph
  ! lxc info m-eph || false
  [ ! -e "${LXD_DIR}/containers/m-eph" ]

  lxc network delete "${br_name}"
}

test_microvm_daemon_restart() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  # The libkrun helpers connect agents to this socket, which the daemon owns.
  local vsock_socket="${LXD_DIR}/vsock-unix.socket"

  lxc init testimage m-old1 --microvm -c limits.memory=256MiB
  lxc init testimage m-old2 --microvm -c limits.memory=256MiB
  lxc start m-old1
  lxc start m-old2

  local pid_old1 pid_old2
  pid_old1="$(< "${LXD_DIR}/logs/m-old1/libkrun.pid")"
  pid_old2="$(< "${LXD_DIR}/logs/m-old2/libkrun.pid")"

  [ -S "${vsock_socket}" ]
  [ ! -e "${LXD_DIR}/libkrun-vsock-proxy.sock" ]

  sub_test "Test running MicroVMs survive a daemon restart"
  # SIGTERM stops the daemon without stopping the instances.
  local lxd_pid
  lxd_pid="$(< "${LXD_DIR}/lxd.pid")"
  kill -TERM "${lxd_pid}"
  for _ in $(seq 120); do
    if [ ! -d "/proc/${lxd_pid}" ]; then
      break
    fi

    sleep 0.5
  done

  [ ! -d "/proc/${lxd_pid}" ]
  kill -0 "${pid_old1}"
  kill -0 "${pid_old2}"

  respawn_lxd "${LXD_DIR}" true

  [ "$(lxc list -f csv -c s m-old1)" = "RUNNING" ]
  [ "$(lxc list -f csv -c s m-old2)" = "RUNNING" ]
  [ "$(< "${LXD_DIR}/logs/m-old1/libkrun.pid")" = "${pid_old1}" ]
  [ "$(< "${LXD_DIR}/logs/m-old2/libkrun.pid")" = "${pid_old2}" ]
  kill -0 "${pid_old1}"
  kill -0 "${pid_old2}"

  sub_test "Test the agent socket is back for MicroVMs started by the previous daemon"
  [ -S "${vsock_socket}" ]

  sub_test "Test stopping an older MicroVM keeps the others running"
  lxc init testimage m-new --microvm -c limits.memory=256MiB
  lxc start m-new
  lxc stop -f m-old1
  [ "$(lxc list -f csv -c s m-old1)" = "STOPPED" ]
  ! kill -0 "${pid_old1}" 2>/dev/null || false
  [ "$(lxc list -f csv -c s m-new)" = "RUNNING" ]
  [ -S "${vsock_socket}" ]

  sub_test "Test an unexpected exit is noticed for a MicroVM started by the previous daemon"
  kill -9 "${pid_old2}"
  _microvm_wait_status m-old2 STOPPED
  for _ in $(seq 60); do
    if [ "$(lxc config get m-old2 volatile.last_state.power)" = "STOPPED" ]; then
      break
    fi

    sleep 0.5
  done

  [ "$(lxc config get m-old2 volatile.last_state.power)" = "STOPPED" ]
  [ ! -e "${LXD_DIR}/logs/m-old2/libkrun.pid" ]

  sub_test "Test the agent socket stays after the last MicroVM stops"
  lxc stop -f m-new
  [ -S "${vsock_socket}" ]

  lxc delete m-old1 m-old2 m-new
}
