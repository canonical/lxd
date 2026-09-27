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

  # Mirror the lookup order used by lxd/instance/drivers/libkrun/dynload.c.
  local has_libkrun=false
  if [ -n "${LIBKRUN_PATH:-}" ] && [ -f "${LIBKRUN_PATH}" ]; then
    has_libkrun=true
  elif ldconfig -p 2>/dev/null | grep -E '\slibkrun\.so(\.[0-2])? ' >/dev/null; then
    has_libkrun=true
  fi

  if [ "${has_libkrun}" = "false" ]; then
    export TEST_UNMET_REQUIREMENT="libkrun not found, install it or set LIBKRUN_PATH"
    return 1
  fi

  if [ "$(storage_backend "${LXD_DIR}")" != "dir" ]; then
    export TEST_UNMET_REQUIREMENT="MicroVM tests currently require the dir storage backend"
    return 1
  fi

  return 0
}

# Runs a command that is expected to fail and checks its output contains the expected text.
_microvm_assert_fails_with() {
  local expected="${1}"
  shift

  local output
  output="$(! "$@" 2>&1 || false)"
  if ! grep -F -- "${expected}" <<< "${output}" >/dev/null; then
    echo "ERROR: expected output to contain ${expected@Q}, got ${output@Q}"
    return 1
  fi
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

# Runs a command in the guest shell on the MicroVM console and optionally waits for the expected output.
# The console only relays output while a client is attached, so the guest shell is first probed
# until it answers, which also proves that the guest has booted.
_microvm_console_run() {
  local name="${1}"
  local cmd="${2}"
  local expected="${3:-}"

  python3 - "${LXD_DIR}/logs/${name}/libkrun.console" "${cmd}" "${expected}" <<'EOF'
import socket
import sys
import time

path, cmd, expected = sys.argv[1], sys.argv[2], sys.argv[3]

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.connect(path)
sock.settimeout(0.5)

output = ""


def read_until(needle, timeout, probe=None):
    """Read console output until needle shows up, resending probe every 2s."""
    global output
    deadline = time.monotonic() + timeout
    next_probe = 0.0
    while needle not in output:
        now = time.monotonic()
        if now > deadline:
            return False

        if probe is not None and now >= next_probe:
            sock.sendall(probe)
            next_probe = now + 2

        try:
            data = sock.recv(4096)
        except socket.timeout:
            continue

        if not data:
            return False

        output += data.decode(errors="replace")

    return True


# The arithmetic keeps the marker out of the echoed command line.
if not read_until("shell-42", 90, probe=b"\necho shell-$((40+2))\n"):
    sys.exit("ERROR: guest shell did not answer on the console, got: %r" % output[-500:])

output = ""
sock.sendall(cmd.encode() + b"\n")

if expected and not read_until(expected, 30):
    sys.exit("ERROR: expected console output %r, got: %r" % (expected, output[-500:]))

# Give the guest a moment to read the command before the console client detaches.
time.sleep(0.5)
EOF
}

test_microvm_feature_preview() {
  local LXD_DIR
  LXD_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"

  sub_test "Verify the client hides --microvm when the feature preview is disabled"
  ! LXD_FEATURES="" lxc init --help | grep -wF -- "--microvm" || false
  LXD_FEATURES="microvm" lxc init --help | grep -wF -- "--microvm" >/dev/null
  LXD_FEATURES="" _microvm_assert_fails_with "unknown flag: --microvm" lxc init testimage m-gated --microvm

  sub_test "Verify the daemon rejects MicroVMs when the feature preview is disabled"
  LXD_FEATURES="" spawn_lxd "${LXD_DIR}" true
  ensure_import_testimage
  LXD_FEATURES="microvm" _microvm_assert_fails_with 'Server does not support MicroVM instances: The server is missing the required "instance_microvm" API extension' lxc init testimage m-gated --microvm
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
  [ "$(lxc list -f csv -c t m1)" = "MICROVM" ]
  [ "$(lxc list -f csv -c s m1)" = "STOPPED" ]
  lxc query /1.0/instances/m1 | jq --exit-status '.type == "microvm"'
  [ -d "${LXD_DIR}/containers/m1" ]

  sub_test "Test config update while stopped"
  lxc config set m1 limits.memory=1GiB limits.cpu=2
  [ "$(lxc config get m1 limits.memory)" = "1GiB" ]
  [ "$(lxc config get m1 limits.cpu)" = "2" ]

  sub_test "Test rename while stopped"
  lxc rename m1 m2
  [ "$(lxc list -f csv -c t m2)" = "MICROVM" ]
  [ "$(lxc list -f csv -c s m2)" = "STOPPED" ]
  ! lxc info m1 || false

  sub_test "Test MicroVM storage volume reporting"
  local pool_name
  pool_name="$(lxc profile device get default root pool)"
  lxc storage volume list "${pool_name}" -f csv -c t,n | grep -xF "microvm,m2"
  lxc storage volume show "${pool_name}" microvm/m2 | grep -xF "type: microvm"

  sub_test "Test start protection"
  lxc config set m2 security.protection.start=true
  _microvm_assert_fails_with "Instance is protected from being started" lxc start m2
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
  curl --unix-socket "${LXD_DIR}/unix.socket" "http://localhost/1.0/instances/m2/logs/microvm.conf" | jq --exit-status '.version == 1 and .body.cpus == 2 and .body.memory_mib == 1024 and .body.kernel.format == "auto"'
  lxc query /1.0/instances/m2/logs | jq --exit-status 'any(.[]; endswith("/microvm.conf"))'
  lxc query /1.0/instances/m2/logs | jq --exit-status 'any(.[]; endswith("/microvm.log"))'
  lxc info m2 --show-log >/dev/null

  sub_test "Test operations rejected on a running MicroVM"
  _microvm_assert_fails_with "The instance is already running" lxc start m2
  _microvm_assert_fails_with 'Key "limits.memory" cannot be updated when VM is running' lxc config set m2 limits.memory=2GiB
  _microvm_assert_fails_with 'Key "limits.cpu" cannot be updated when VM is running' lxc config set m2 limits.cpu=1
  [ "$(lxc config get m2 limits.memory)" = "1GiB" ]
  ! lxc rename m2 m3 || false
  ! lxc delete m2 || false
  [ "$(lxc list -f csv -c s m2)" = "RUNNING" ]

  sub_test "Test graceful stop and restart require lxd-agent"
  # testimage has no lxd-agent, so the guest cannot be asked to power off.
  _microvm_assert_fails_with "Failed requesting guest power off" lxc stop m2 --timeout 10
  _microvm_assert_fails_with "Failed requesting guest power off" lxc restart m2 --timeout 10
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
  _microvm_assert_fails_with "The instance is already stopped" lxc stop -f m2
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
  _microvm_assert_fails_with "Kernel not found" lxc start m-nokernel
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
  local agent_mounts="${LXD_DIR}/containers/m-disk/config/agent-mounts.json"
  mkdir -p "${share}"
  echo "test data" > "${share}/hello.txt"

  sub_test "Test MicroVM starts with a host directory disk added while stopped"
  lxc init testimage m-disk --microvm
  lxc config device add m-disk data1 disk source="${share}" path=/mnt/data1
  lxc start m-disk
  [ "$(lxc list -f csv -c s m-disk)" = "RUNNING" ]

  sub_test "Test hot-adding a disk regenerates agent-mounts.json"
  lxc config device add m-disk data2 disk source="${share}" path=/mnt/data2 readonly=true
  [ -f "${agent_mounts}" ]
  jq --exit-status 'map(.target) | sort == ["/mnt/data1", "/mnt/data2"]' "${agent_mounts}"
  jq --exit-status '.[] | select(.target == "/mnt/data1") | .source == "lxd_data1" and .fstype == "virtiofs" and (.options // []) == []' "${agent_mounts}"
  jq --exit-status '.[] | select(.target == "/mnt/data2") | .source == "lxd_data2" and .fstype == "virtiofs" and .options == ["ro"]' "${agent_mounts}"

  sub_test "Test hot-removing a disk regenerates agent-mounts.json"
  lxc config device remove m-disk data2
  jq --exit-status 'map(.target) == ["/mnt/data1"]' "${agent_mounts}"

  lxc delete -f m-disk
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

  sub_test "Test the host side of the NIC is removed on stop"
  lxc stop -f m-nic
  [ ! -e "/sys/class/net/${host_name}" ]

  lxc delete m-nic
  lxc network delete "${br_name}"
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
  _microvm_assert_fails_with "Not supported" lxc pause m-neg
  [ "$(lxc list -f csv -c s m-neg)" = "RUNNING" ]
  _microvm_assert_fails_with "Not supported" lxc snapshot m-neg snap0
  _microvm_assert_fails_with "Stateful stop is not supported for MicroVM instances" lxc stop --stateful m-neg
  [ "$(lxc list -f csv -c s m-neg)" = "RUNNING" ]
  _microvm_assert_fails_with "Not supported" lxc file pull m-neg/etc/hostname -

  lxc stop -f m-neg

  sub_test "Unsupported operations return errors on stopped instance"
  _microvm_assert_fails_with "Not supported" lxc snapshot m-neg snap0
  lxc query /1.0/instances/m-neg/snapshots | jq --exit-status 'length == 0'
  _microvm_assert_fails_with "Not supported" lxc rebuild testimage m-neg
  _microvm_assert_fails_with "Create backup" lxc export m-neg "${TEST_DIR}/m-neg.tar.gz"
  lxc query /1.0/instances/m-neg/backups | jq --exit-status 'length == 0'
  rm -f "${TEST_DIR}/m-neg.tar.gz"

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
  _microvm_console_run m-guest 'echo booted-$((40+2))' "booted-42"

  sub_test "Test a reboot from inside the guest restarts the MicroVM"
  local pid_before pid_after
  pid_before="$(< "${LXD_DIR}/logs/m-guest/libkrun.pid")"
  _microvm_console_run m-guest "reboot -f"

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
  _microvm_console_run m-guest 'echo rebooted-$((40+2))' "rebooted-42"

  sub_test "Test a poweroff from inside the guest stops the MicroVM"
  _microvm_console_run m-guest "poweroff -f"
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
  [ "$(lxc list -f csv -c s m-crash)" = "STOPPED" ]

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

  local proxy_socket="${LXD_DIR}/libkrun-vsock-proxy.sock"

  # The agent proxy only exists when LXD serves its VM unix socket.
  local has_proxy=false
  if [ -S "${LXD_DIR}/vsock-unix.socket" ]; then
    has_proxy=true
  fi

  lxc init testimage m-old1 --microvm -c limits.memory=256MiB
  lxc init testimage m-old2 --microvm -c limits.memory=256MiB
  lxc start m-old1
  lxc start m-old2

  local pid_old1 pid_old2
  pid_old1="$(< "${LXD_DIR}/logs/m-old1/libkrun.pid")"
  pid_old2="$(< "${LXD_DIR}/logs/m-old2/libkrun.pid")"

  if [ "${has_proxy}" = "true" ]; then
    [ -S "${proxy_socket}" ]
  fi

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

  sub_test "Test the agent proxy is restored for MicroVMs started by the previous daemon"
  if [ "${has_proxy}" = "true" ]; then
    [ -S "${proxy_socket}" ]
  fi

  sub_test "Test stopping an older MicroVM keeps the agent proxy for the others"
  lxc init testimage m-new --microvm -c limits.memory=256MiB
  lxc start m-new
  lxc stop -f m-old1
  [ "$(lxc list -f csv -c s m-old1)" = "STOPPED" ]
  ! kill -0 "${pid_old1}" 2>/dev/null || false
  [ "$(lxc list -f csv -c s m-new)" = "RUNNING" ]
  if [ "${has_proxy}" = "true" ]; then
    [ -S "${proxy_socket}" ]
  fi

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
  if [ "${has_proxy}" = "true" ]; then
    [ -S "${proxy_socket}" ]
  fi

  sub_test "Test the agent proxy is closed with the last MicroVM"
  lxc stop -f m-new
  [ ! -e "${proxy_socket}" ]

  lxc delete m-old1 m-old2 m-new
}
