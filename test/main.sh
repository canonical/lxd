#!/bin/sh -eu
[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

# Don't translate lxc output for parsing in it in tests.
export LC_ALL="C"

# Force UTC for consistency
export TZ="UTC"

if [ -n "${LXD_VERBOSE:-}" ] || [ -n "${LXD_DEBUG:-}" ]; then
  set -x
fi

export DEBUG=""
if [ -n "${LXD_VERBOSE:-}" ]; then
  DEBUG="--verbose"
fi

if [ -n "${LXD_DEBUG:-}" ]; then
  DEBUG="--debug"
fi

if [ -z "${LXD_BACKEND:-}" ]; then
    LXD_BACKEND="dir"
fi

# shellcheck disable=SC2034
LXD_NETNS=""

import_subdir_files() {
    test "$1"
    # shellcheck disable=SC2039
    local file
    for file in "$1"/*.sh; do
        # shellcheck disable=SC1090
        . "$file"
    done
}

import_subdir_files includes

echo "==> Checking for dependencies"
check_dependencies lxd lxc curl dnsmasq jq git xgettext sqlite3 msgmerge msgfmt shuf setfacl uuidgen

if [ "${USER:-'root'}" != "root" ]; then
  echo "The testsuite must be run as root." >&2
  exit 1
fi

if [ -n "${LXD_LOGS:-}" ] && [ ! -d "${LXD_LOGS}" ]; then
  echo "Your LXD_LOGS path doesn't exist: ${LXD_LOGS}"
  exit 1
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

import_storage_backends

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
    printf "To poke around, use:\\n LXD_DIR=%s LXD_CONF=%s sudo -E %s/bin/lxc COMMAND\\n" "${LXD_DIR}" "${LXD_CONF}" ${GOPATH:-}
    echo "Tests Completed (${TEST_RESULT}): hit enter to continue"

    # shellcheck disable=SC2034
    read -r nothing
  fi

  echo "==> Cleaning up"

  kill_external_auth_daemon "$TEST_DIR"
  cleanup_lxds "$TEST_DIR"

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION}"
  fi
  echo "==> Test result: ${TEST_RESULT}"
}

# Must be set before cleanup()
TEST_CURRENT=setup
# shellcheck disable=SC2034
TEST_RESULT=failure

trap cleanup EXIT HUP INT TERM

# Import all the testsuites
import_subdir_files suites

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

start_external_auth_daemon "${LXD_DIR}"

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
  # shellcheck disable=SC2034
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
run_test test_resources "resources"
run_test test_kernel_limits "kernel limits"
run_test test_macaroon_auth "macaroon authentication"
run_test test_console "console"
run_test test_proxy_device "proxy device"
run_test test_clustering_membership "clustering membership"
run_test test_clustering_containers "clustering containers"

# shellcheck disable=SC2034
TEST_RESULT=success
