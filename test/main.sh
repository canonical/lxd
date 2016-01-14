#!/bin/sh -eu
[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

if [ -n "${LXD_DEBUG:-}" ]; then
  set -x
  DEBUG="--debug"
fi

echo "==> Checking for dependencies"
for dep in lxd lxc curl jq git xgettext sqlite3 msgmerge msgfmt shuf setfacl uuidgen pyflakes3 pep8 shellcheck; do
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

# import all the backends
for backend in backends/*.sh; do
  . "${backend}"
done

if [ -z "${LXD_BACKEND:-}" ]; then
  LXD_BACKEND=dir
fi

spawn_lxd() {
  set +x
  # LXD_DIR is local here because since $(lxc) is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.
  local LXD_DIR

  lxddir=${1}
  shift

  # Copy pre generated Certs
  cp deps/server.crt "${lxddir}"
  cp deps/server.key "${lxddir}"

  # setup storage
  "$LXD_BACKEND"_setup "${lxddir}"

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
  if [ -n "${LXD_DEBUG:-}" ]; then
    set -x
  fi

  echo "==> Configuring storage backend"
  "$LXD_BACKEND"_configure "${lxddir}"
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
  if [ -n "${LXD_DEBUG:-}" ]; then
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
      ../scripts/lxd-images import busybox --alias testimage
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
  local LXD_DIR
  daemon_dir=${1}
  LXD_DIR=${daemon_dir}
  daemon_pid=$(cat "${daemon_dir}/lxd.pid")
  echo "==> Killing LXD at ${daemon_dir}"

  if [ -e "${daemon_dir}/unix.socket" ]; then
    # Delete all containers
    echo "==> Deleting all containers"
    for container in $(lxc list --force-local | tail -n+3 | grep "^| " | cut -d' ' -f2); do
      lxc delete "${container}" --force-local || true
    done

    # Delete all images
    echo "==> Deleting all images"
    for image in $(lxc image list --force-local | tail -n+3 | grep "^| " | cut -d'|' -f3 | sed "s/^ //g"); do
      lxc image delete "${image}" --force-local || true
    done

    echo "==> Checking for locked DB tables"
    for table in $(echo .tables | sqlite3 "${daemon_dir}/lxd.db"); do
      echo "SELECT * FROM ${table};" | sqlite3 "${daemon_dir}/lxd.db" >/dev/null
    done

    # Kill the daemon
    lxd shutdown || kill -9 "${daemon_pid}" 2>/dev/null || true

    # Cleanup shmounts (needed due to the forceful kill)
    find "${daemon_dir}" -name shmounts -exec "umount" "-l" "{}" \; >/dev/null 2>&1 || true
  fi

  if [ -n "${LXD_LOGS:-}" ]; then
    echo "==> Copying the logs"
    mkdir -p "${LXD_LOGS}/${daemon_pid}"
    cp -R "${daemon_dir}/logs/" "${LXD_LOGS}/${daemon_pid}/"
    cp "${daemon_dir}/lxd.log" "${LXD_LOGS}/${daemon_pid}/"
  fi

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

  # teardown storage
  "$LXD_BACKEND"_teardown "${daemon_dir}"

  # Wipe the daemon directory
  wipe "${daemon_dir}"

  # Remove the daemon from the list
  sed "\|^${daemon_dir}|d" -i "${TEST_DIR}/daemons"
}

cleanup() {
  set +e

  # Allow for inspection
  if [ -n "${LXD_INSPECT:-}" ]; then
    echo "==> Test result: ${TEST_RESULT}"
    if [ "${TEST_RESULT}" != "success" ]; then
      echo "failed test: ${TEST_CURRENT}"
    fi

    # shellcheck disable=SC2086
    printf "To poke around, use:\n LXD_DIR=%s LXD_CONF=%s sudo -E %s/bin/lxc COMMAND\n" "${LXD_DIR}" "${LXD_CONF}" ${GOPATH:-}
    echo "Tests Completed (${TEST_RESULT}): hit enter to continue"

    # shellcheck disable=SC2034
    read nothing
  fi

  echo "==> Cleaning up"

  # Kill all the LXD instances
  while read daemon_dir; do
    kill_lxd "${daemon_dir}"
  done < "${TEST_DIR}/daemons"

  # Wipe the test environment
  wipe "${TEST_DIR}"

  echo ""
  echo ""
  echo "==> Test result: ${TEST_RESULT}"
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "failed test: ${TEST_CURRENT}"
  fi
}

wipe() {
  if which btrfs >/dev/null 2>&1; then
    rm -Rf "${1}" 2>/dev/null || true
    if [ -d "${1}" ]; then
      find "${1}" | tac | xargs btrfs subvolume delete >/dev/null 2>&1 || true
    fi
  fi

  # shellcheck disable=SC2009
  ps aux | grep lxc-monitord | grep "${1}" | awk '{print $2}' | while read pid; do
    kill -9 "${pid}"
  done

  if [ -f "${TEST_DIR}/loops" ]; then
    while read line; do
      losetup -d "${line}" || true
    done < "${TEST_DIR}/loops"
  fi
  if mountpoint -q "${1}"; then
    umount "${1}"
  fi

  rm -Rf "${1}"
}

trap cleanup EXIT HUP INT TERM

# Import all the testsuites
for suite in suites/*.sh; do
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

# Setup the first LXD
LXD_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_DIR
chmod +x "${LXD_DIR}"
spawn_lxd "${LXD_DIR}"
LXD_ADDR=$(cat "${LXD_DIR}/lxd.addr")
export LXD_ADDR

# Setup the second LXD
LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
chmod +x "${LXD2_DIR}"
spawn_lxd "${LXD2_DIR}"
LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")
export LXD2_ADDR

TEST_CURRENT=setup
TEST_RESULT=failure

# allow for running a specific set of tests
if [ "$#" -gt 0 ]; then
  "test_${1}"
  TEST_RESULT=success
  exit
fi

echo "==> TEST: doing static analysis of commits"
TEST_CURRENT=test_static_analysis
test_static_analysis

echo "==> TEST: checking dependencies"
TEST_CURRENT=test_check_deps
test_check_deps

echo "==> TEST: Database schema update"
TEST_CURRENT=test_database_update
test_database_update

echo "==> TEST: lxc remote url"
TEST_CURRENT=test_remote_url
test_remote_url

echo "==> TEST: lxc remote administration"
TEST_CURRENT=test_remote_admin
test_remote_admin

echo "==> TEST: basic usage"
TEST_CURRENT=test_basic_usage
test_basic_usage

echo "==> TEST: images (and cached image expiry)"
TEST_CURRENT=test_image_expiry
test_image_expiry

if [ -n "${LXD_CONCURRENT:-}" ]; then
  echo "==> TEST: concurrent exec"
  TEST_CURRENT=test_concurrent_exec
  test_concurrent_exec

  echo "==> TEST: concurrent startup"
  TEST_CURRENT=test_concurrent
  test_concurrent
fi

echo "==> TEST: lxc remote usage"
TEST_CURRENT=test_remote_usage
test_remote_usage

echo "==> TEST: snapshots"
TEST_CURRENT=test_snapshots
test_snapshots

echo "==> TEST: snapshot restore"
TEST_CURRENT=test_snap_restore
test_snap_restore

echo "==> TEST: profiles, devices and configuration"
TEST_CURRENT=test_config_profiles
test_config_profiles

echo "==> TEST: server config"
TEST_CURRENT=test_server_config
test_server_config

echo "==> TEST: filemanip"
TEST_CURRENT=test_filemanip
test_filemanip

echo "==> TEST: devlxd"
TEST_CURRENT=test_devlxd
test_devlxd

if which fuidshift >/dev/null 2>&1; then
  echo "==> TEST: uidshift"
  TEST_CURRENT=test_fuidshift
  test_fuidshift
else
  echo "==> SKIP: fuidshift (binary missing)"
fi

echo "==> TEST: migration"
TEST_CURRENT=test_migration
test_migration

curversion=$(dpkg -s lxc | awk '/^Version/ { print $2 }')
if dpkg --compare-versions "${curversion}" gt 1.1.2-0ubuntu3; then
  echo "==> TEST: fdleak"
  TEST_CURRENT=test_fdleak
  test_fdleak
else
  # We temporarily skip the fdleak test because a bug in lxc is
  # known to make it # fail without lxc commit
  # 858377e: # logs: introduce a thread-local 'current' lxc_config (v2)
  echo "==> SKIPPING TEST: fdleak"
fi

echo "==> TEST: cpu profiling"
TEST_CURRENT=test_cpu_profiling
test_cpu_profiling

echo "==> TEST: memory profiling"
TEST_CURRENT=test_mem_profiling
test_mem_profiling

TEST_RESULT=success
