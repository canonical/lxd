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

if [ -z "${LXD_BACKEND:-}" ]; then
  LXD_BACKEND=dir
fi

echo "==> Available storage backends: $(available_storage_backends | sort)"
if [ "$LXD_BACKEND" != "random" ] && ! storage_backend_available "$LXD_BACKEND"; then
  if [ "${LXD_BACKEND}" = "ceph" ] && [ -z "${LXD_CEPH_CLUSTER:-}" ]; then
    echo "Ceph storage backend requires that \"LXD_CEPH_CLUSTER\" be set."
    exit 1
  fi
  echo "Storage backend \"$LXD_BACKEND\" is not available"
  exit 1
fi
echo "==> Using storage backend ${LXD_BACKEND}"

# import storage backends
for backend in $(available_storage_backends); do
  # shellcheck disable=SC1090
  . "backends/${backend}.sh"
done

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

  # Cleanup leftover networks
  # shellcheck disable=SC2009
  ps aux | grep "interface=lxdt$$ " | grep -v grep | awk '{print $2}' | while read -r line; do
    kill -9 "${line}"
  done
  if [ -e "/sys/class/net/lxdt$$" ]; then
    ip link del lxdt$$
  fi

  # Wipe the test environment
  wipe "${TEST_DIR}"

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION}"
  fi
  echo "==> Test result: ${TEST_RESULT}"
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
spawn_lxd "${LXD_DIR}" true
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
run_test test_image_list_all_aliases "image list all aliases"
run_test test_image_auto_update "image auto-update"
run_test test_image_prefer_cached "image prefer cached"
run_test test_image_import_dir "import image from directory"
run_test test_concurrent_exec "concurrent exec"
run_test test_concurrent "concurrent startup"
run_test test_snapshots "container snapshots"
run_test test_snap_restore "snapshot restores"
run_test test_config_profiles "profiles and configuration"
run_test test_config_edit "container configuration edit"
run_test test_config_edit_container_snapshot_pool_config "container and snapshot volume configuration edit"
run_test test_container_metadata "manage container metadata and templates"
run_test test_server_config "server configuration"
run_test test_filemanip "file manipulations"
run_test test_network "network management"
run_test test_idmap "id mapping"
run_test test_template "file templating"
run_test test_pki "PKI mode"
run_test test_devlxd "/dev/lxd"
run_test test_fuidshift "fuidshift"
run_test test_migration "migration"
run_test test_fdleak "fd leak"
run_test test_cpu_profiling "CPU profiling"
run_test test_mem_profiling "memory profiling"
run_test test_storage "storage"
run_test test_init_auto "lxd init auto"
run_test test_init_interactive "lxd init interactive"
run_test test_init_preseed "lxd init preseed"
run_test test_storage_profiles "storage profiles"
run_test test_container_import "container import"
run_test test_storage_volume_attach "attaching storage volumes"
run_test test_storage_driver_ceph "ceph storage driver"

TEST_RESULT=success
