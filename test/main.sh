#!/bin/sh -eu
[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

# Don't translate lxc output for parsing in it in tests.
export "LC_ALL=C"

# Force UTC for consistency
export "TZ=UTC"

if [ -n "${LXD_VERBOSE:-}" ] || [ -n "${LXD_DEBUG:-}" ]; then
  set -x
fi

if [ -n "${LXD_VERBOSE:-}" ]; then
  DEBUG="--verbose"
fi

if [ -n "${LXD_DEBUG:-}" ]; then
  DEBUG="--debug"
fi

echo "==> Checking for dependencies"
deps="lxd lxc curl jq git xgettext sqlite3 msgmerge msgfmt shuf setfacl uuidgen"
for dep in $deps; do
  which "${dep}" >/dev/null 2>&1 || (echo "Missing dependency: ${dep}" >&2 && exit 1)
done

if [ "${USER:-'root'}" != "root" ]; then
  echo "The testsuite must be run as root." >&2
  exit 1
fi

if [ -n "${LXD_LOGS:-}" ] && [ ! -d "${LXD_LOGS}" ]; then
  echo "Your LXD_LOGS path doesn't exist: ${LXD_LOGS}"
  exit 1
fi

# Helper functions
for include in includes/*.sh; do
    # shellcheck disable=SC1090
    . "$include"
done

local_tcp_port() {
  while :; do
    port=$(shuf -i 10000-32768 -n 1)
    nc -l 127.0.0.1 "${port}" >/dev/null 2>&1 &
    pid=$!
    kill "${pid}" >/dev/null 2>&1 || continue
    wait "${pid}" || true
    echo "${port}"
    return
  done
}

# Set default backend to dir
if [ -z "${LXD_BACKEND:-}" ]; then
    LXD_BACKEND=dir
fi

echo "==> Available storage backends: $(available_storage_backends | sort)"
if [ "$LXD_BACKEND" != "random" ] && ! storage_backend_available "$LXD_BACKEND"; then
  echo "Storage backage \"$LXD_BACKEND\" is not available"
  exit 1
fi
echo "==> Using storage backend ${LXD_BACKEND}"

# import storage backends
for backend in $(available_storage_backends); do
  # shellcheck disable=SC1090
  . "backends/${backend}.sh"
done

spawn_lxd() {
  set +x
  # LXD_DIR is local here because since $(lxc) is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.

  # shellcheck disable=2039
  local LXD_DIR lxddir lxd_backend

  lxddir=${1}
  shift

  if [ "$LXD_BACKEND" = "random" ]; then
    lxd_backend="$(random_storage_backend)"
  else
    lxd_backend="$LXD_BACKEND"
  fi

  # Copy pre generated Certs
  cp deps/server.crt "${lxddir}"
  cp deps/server.key "${lxddir}"

  # setup storage
  "$lxd_backend"_setup "${lxddir}"
  echo "$lxd_backend" > "${lxddir}/lxd.backend"

  echo "==> Spawning lxd in ${lxddir}"
  # shellcheck disable=SC2086
  LXD_DIR="${lxddir}" lxd --logfile "${lxddir}/lxd.log" ${DEBUG-} "$@" 2>&1 &
  LXD_PID=$!
  echo "${LXD_PID}" > "${lxddir}/lxd.pid"
  echo "${lxddir}" >> "${TEST_DIR}/daemons"
  echo "==> Spawned LXD (PID is ${LXD_PID})"

  echo "==> Confirming lxd is responsive"
  LXD_DIR="${lxddir}" lxd waitready --timeout=300

  echo "==> Binding to network"
  # shellcheck disable=SC2034
  for i in $(seq 10); do
    addr="127.0.0.1:$(local_tcp_port)"
    LXD_DIR="${lxddir}" lxc config set core.https_address "${addr}" || continue
    echo "${addr}" > "${lxddir}/lxd.addr"
    echo "==> Bound to ${addr}"
    break
  done

  echo "==> Setting trust password"
  LXD_DIR="${lxddir}" lxc config set core.trust_password foo
  if [ -n "${DEBUG:-}" ]; then
    set -x
  fi

  echo "==> Configuring storage backend"
  "$lxd_backend"_configure "${lxddir}"
}

respawn_lxd() {
  set +x
  # LXD_DIR is local here because since $(lxc) is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.

  # shellcheck disable=2039
  local LXD_DIR

  lxddir=${1}
  shift

  echo "==> Spawning lxd in ${lxddir}"
  # shellcheck disable=SC2086
  LXD_DIR="${lxddir}" lxd --logfile "${lxddir}/lxd.log" ${DEBUG-} "$@" 2>&1 &
  LXD_PID=$!
  echo "${LXD_PID}" > "${lxddir}/lxd.pid"
  echo "==> Spawned LXD (PID is ${LXD_PID})"

  echo "==> Confirming lxd is responsive"
  LXD_DIR="${lxddir}" lxd waitready --timeout=300
}

lxc() {
  LXC_LOCAL=1
  lxc_remote "$@"
  RET=$?
  unset LXC_LOCAL
  return ${RET}
}

lxc_remote() {
  set +x
  injected=0
  cmd=$(which lxc)

  # shellcheck disable=SC2048,SC2068
  for arg in $@; do
    if [ "${arg}" = "--" ]; then
      injected=1
      cmd="${cmd} ${DEBUG:-}"
      [ -n "${LXC_LOCAL}" ] && cmd="${cmd} --force-local"
      cmd="${cmd} --"
    elif [ "${arg}" = "--force-local" ]; then
      continue
    else
      cmd="${cmd} \"${arg}\""
    fi
  done

  if [ "${injected}" = "0" ]; then
    cmd="${cmd} ${DEBUG-}"
  fi
  if [ -n "${DEBUG:-}" ]; then
    set -x
  fi
  eval "${cmd}"
}

my_curl() {
  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" "$@"
}

wait_for() {
  addr=${1}
  shift
  op=$("$@" | jq -r .operation)
  my_curl "https://${addr}${op}/wait"
}

ensure_has_localhost_remote() {
  addr=${1}
  if ! lxc remote list | grep -q "localhost"; then
    lxc remote add localhost "https://${addr}" --accept-certificate --password foo
  fi
}

ensure_import_testimage() {
  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
    if [ -e "${LXD_TEST_IMAGE:-}" ]; then
      lxc image import "${LXD_TEST_IMAGE}" --alias testimage
    else
      if [ ! -e "/bin/busybox" ]; then
        echo "Please install busybox (busybox-static) or set LXD_TEST_IMAGE"
        exit 1
      fi

      if ldd /bin/busybox >/dev/null 2>&1; then
        echo "The testsuite requires /bin/busybox to be a static binary"
        exit 1
      fi

      deps/import-busybox --alias testimage
    fi
  fi
}

check_empty() {
  if [ "$(find "${1}" 2> /dev/null | wc -l)" -gt "1" ]; then
    echo "${1} is not empty, content:"
    find "${1}"
    false
  fi
}

check_empty_table() {
  if [ -n "$(sqlite3 "${1}" "SELECT * FROM ${2};")" ]; then
    echo "DB table ${2} is not empty, content:"
    sqlite3 "${1}" "SELECT * FROM ${2};"
    false
  fi
}

kill_lxd() {
  # LXD_DIR is local here because since $(lxc) is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.

  # shellcheck disable=2039
  local LXD_DIR daemon_dir daemon_pid check_leftovers lxd_backend

  daemon_dir=${1}
  LXD_DIR=${daemon_dir}
  daemon_pid=$(cat "${daemon_dir}/lxd.pid")
  check_leftovers="false"
  lxd_backend=$(storage_backend "$daemon_dir")
  echo "==> Killing LXD at ${daemon_dir}"

  if [ -e "${daemon_dir}/unix.socket" ]; then
    # Delete all containers
    echo "==> Deleting all containers"
    for container in $(lxc list --force-local | tail -n+3 | grep "^| " | cut -d' ' -f2); do
      lxc delete "${container}" --force-local -f || true
    done

    # Delete all images
    echo "==> Deleting all images"
    for image in $(lxc image list --force-local | tail -n+3 | grep "^| " | cut -d'|' -f3 | sed "s/^ //g"); do
      lxc image delete "${image}" --force-local || true
    done

    # Delete all profiles
    echo "==> Deleting all profiles"
    for profile in $(lxc profile list --force-local); do
      lxc profile delete "${profile}" --force-local || true
    done

    echo "==> Checking for locked DB tables"
    for table in $(echo .tables | sqlite3 "${daemon_dir}/lxd.db"); do
      echo "SELECT * FROM ${table};" | sqlite3 "${daemon_dir}/lxd.db" >/dev/null
    done

    # Kill the daemon
    lxd shutdown || kill -9 "${daemon_pid}" 2>/dev/null || true

    # Cleanup shmounts (needed due to the forceful kill)
    find "${daemon_dir}" -name shmounts -exec "umount" "-l" "{}" \; >/dev/null 2>&1 || true
    find "${daemon_dir}" -name devlxd -exec "umount" "-l" "{}" \; >/dev/null 2>&1 || true

    check_leftovers="true"
  fi

  if [ -n "${LXD_LOGS:-}" ]; then
    echo "==> Copying the logs"
    mkdir -p "${LXD_LOGS}/${daemon_pid}"
    cp -R "${daemon_dir}/logs/" "${LXD_LOGS}/${daemon_pid}/"
    cp "${daemon_dir}/lxd.log" "${LXD_LOGS}/${daemon_pid}/"
  fi

  if [ "${check_leftovers}" = "true" ]; then
    echo "==> Checking for leftover files"
    rm -f "${daemon_dir}/containers/lxc-monitord.log"
    rm -f "${daemon_dir}/security/apparmor/cache/.features"
    check_empty "${daemon_dir}/containers/"
    check_empty "${daemon_dir}/devices/"
    check_empty "${daemon_dir}/images/"
    # FIXME: Once container logging rework is done, uncomment
    # check_empty "${daemon_dir}/logs/"
    check_empty "${daemon_dir}/security/apparmor/cache/"
    check_empty "${daemon_dir}/security/apparmor/profiles/"
    check_empty "${daemon_dir}/security/seccomp/"
    check_empty "${daemon_dir}/shmounts/"
    check_empty "${daemon_dir}/snapshots/"

    echo "==> Checking for leftover DB entries"
    check_empty_table "${daemon_dir}/lxd.db" "containers"
    check_empty_table "${daemon_dir}/lxd.db" "containers_config"
    check_empty_table "${daemon_dir}/lxd.db" "containers_devices"
    check_empty_table "${daemon_dir}/lxd.db" "containers_devices_config"
    check_empty_table "${daemon_dir}/lxd.db" "containers_profiles"
    check_empty_table "${daemon_dir}/lxd.db" "images"
    check_empty_table "${daemon_dir}/lxd.db" "images_aliases"
    check_empty_table "${daemon_dir}/lxd.db" "images_properties"
    check_empty_table "${daemon_dir}/lxd.db" "images_source"
    check_empty_table "${daemon_dir}/lxd.db" "profiles"
    check_empty_table "${daemon_dir}/lxd.db" "profiles_config"
    check_empty_table "${daemon_dir}/lxd.db" "profiles_devices"
    check_empty_table "${daemon_dir}/lxd.db" "profiles_devices_config"
  fi

  # teardown storage
  "$lxd_backend"_teardown "${daemon_dir}"

  # Wipe the daemon directory
  wipe "${daemon_dir}"

  # Remove the daemon from the list
  sed "\|^${daemon_dir}|d" -i "${TEST_DIR}/daemons"
}

shutdown_lxd() {
  # LXD_DIR is local here because since $(lxc) is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.

  # shellcheck disable=2039
  local LXD_DIR

  daemon_dir=${1}
  LXD_DIR=${daemon_dir}
  daemon_pid=$(cat "${daemon_dir}/lxd.pid")
  echo "==> Killing LXD at ${daemon_dir}"

  # Kill the daemon
  lxd shutdown || kill -9 "${daemon_pid}" 2>/dev/null || true
}

cleanup() {
  # Allow for failures and stop tracing everything
  set +ex
  DEBUG=

  # Allow for inspection
  if [ -n "${LXD_INSPECT:-}" ]; then
    if [ "${TEST_RESULT}" != "success" ]; then
      echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION}"
    fi
    echo "==> Test result: ${TEST_RESULT}"

    # shellcheck disable=SC2086
    printf "To poke around, use:\n LXD_DIR=%s LXD_CONF=%s sudo -E %s/bin/lxc COMMAND\n" "${LXD_DIR}" "${LXD_CONF}" ${GOPATH:-}
    echo "Tests Completed (${TEST_RESULT}): hit enter to continue"

    # shellcheck disable=SC2034
    read -r nothing
  fi

  echo "==> Cleaning up"

  # Kill all the LXD instances
  while read -r daemon_dir; do
    kill_lxd "${daemon_dir}"
  done < "${TEST_DIR}/daemons"

  # Wipe the test environment
  wipe "${TEST_DIR}"

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION}"
  fi
  echo "==> Test result: ${TEST_RESULT}"
}

wipe() {
  if which btrfs >/dev/null 2>&1; then
    rm -Rf "${1}" 2>/dev/null || true
    if [ -d "${1}" ]; then
      find "${1}" | tac | xargs btrfs subvolume delete >/dev/null 2>&1 || true
    fi
  fi

  # shellcheck disable=SC2009
  ps aux | grep lxc-monitord | grep "${1}" | awk '{print $2}' | while read -r pid; do
    kill -9 "${pid}" || true
  done

  if [ -f "${TEST_DIR}/loops" ]; then
    while read -r line; do
      losetup -d "${line}" || true
    done < "${TEST_DIR}/loops"
  fi
  if mountpoint -q "${1}"; then
    umount "${1}"
  fi

  rm -Rf "${1}"
}

# Must be set before cleanup()
TEST_CURRENT=setup
TEST_RESULT=failure

trap cleanup EXIT HUP INT TERM

# Import all the testsuites
for suite in suites/*.sh; do
  # shellcheck disable=SC1090
 . "${suite}"
done

# Setup test directory
TEST_DIR=$(mktemp -d -p "$(pwd)" tmp.XXX)
chmod +x "${TEST_DIR}"

if [ -n "${LXD_TMPFS:-}" ]; then
  mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751
fi

LXD_CONF=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_CONF

LXD_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_DIR
chmod +x "${LXD_DIR}"
spawn_lxd "${LXD_DIR}"
LXD_ADDR=$(cat "${LXD_DIR}/lxd.addr")
export LXD_ADDR

run_test() {
  TEST_CURRENT=${1}
  TEST_CURRENT_DESCRIPTION=${2:-${1}}

  echo "==> TEST BEGIN: ${TEST_CURRENT_DESCRIPTION}"
  START_TIME=$(date +%s)
  ${TEST_CURRENT}
  END_TIME=$(date +%s)

  echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION} ($((END_TIME-START_TIME))s)"
}

# allow for running a specific set of tests
if [ "$#" -gt 0 ]; then
  run_test "test_${1}"
  TEST_RESULT=success
  exit
fi

run_test test_check_deps "checking dependencies"
run_test test_static_analysis "static analysis"
run_test test_database_update "database schema updates"
run_test test_remote_url "remote url handling"
run_test test_remote_admin "remote administration"
run_test test_remote_usage "remote usage"
run_test test_basic_usage "basic usage"
run_test test_security "security features"
run_test test_image_expiry "image expiry"
run_test test_image_auto_update "image auto-update"
run_test test_concurrent_exec "concurrent exec"
run_test test_concurrent "concurrent startup"
run_test test_snapshots "container snapshots"
run_test test_snap_restore "snapshot restores"
run_test test_config_profiles "profiles and configuration"
run_test test_server_config "server configuration"
run_test test_filemanip "file manipulations"
run_test test_idmap "id mapping"
run_test test_template "file templating"
run_test test_devlxd "/dev/lxd"
run_test test_fuidshift "fuidshift"
run_test test_migration "migration"
run_test test_fdleak "fd leak"
run_test test_cpu_profiling "CPU profiling"
run_test test_mem_profiling "memory profiling"
run_test test_init_auto "lxd init auto"
run_test test_init_interactive "lxd init interactive"

TEST_RESULT=success
