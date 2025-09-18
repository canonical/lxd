#!/bin/bash
set -eu
set -o pipefail
if [ -z "${GOPATH:-}" ] && command -v go >/dev/null; then
    GOPATH="$(go env GOPATH)"
fi

# Avoid accidental re-execution
if [ -n "${LXD_INSPECT_INPROGRESS:-}" ]; then
    echo "Refusing to run tests from inside a LXD_INSPECT session" >&2
    exit 1
fi

[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

# Don't translate lxc output for parsing in it in tests.
export LC_ALL="C"

# Force UTC for consistency
export TZ="UTC"

if [ -z "${NO_PROXY:-}" ]; then
  # Prevent proxy usage for some host names/IPs (comma-separated list)
  export NO_PROXY="127.0.0.1"
fi

export CLIENT_DEBUG=""
export SERVER_DEBUG=""
export SHELL_TRACING=""
if [ "${LXD_VERBOSE:-0}" != "0" ]; then
  if [ "${LXD_VERBOSE}" = "client" ]; then
      CLIENT_DEBUG="--verbose"
  elif [ "${LXD_VERBOSE}" = "server" ]; then
      SERVER_DEBUG="--verbose"
  else
      CLIENT_DEBUG="--verbose"
      SERVER_DEBUG="--verbose"
  fi

  SHELL_TRACING=1
fi

if [ "${LXD_DEBUG:-0}" != "0" ]; then
  if [ "${LXD_DEBUG}" = "client" ]; then
      CLIENT_DEBUG="--debug"
  elif [ "${LXD_DEBUG}" = "server" ]; then
      SERVER_DEBUG="--debug"
  else
      CLIENT_DEBUG="--debug"
      SERVER_DEBUG="--debug"
  fi

  SHELL_TRACING=1
fi

# shellcheck disable=SC2034
LXD_NETNS=""

import_subdir_files() {
    test "$1"
    local file
    for file in "$1"/*.sh; do
        # shellcheck disable=SC1090
        . "$file"
    done
}

# `main.sh` needs to be executed from inside the `test/` directory
if [ "${PWD}" != "$(dirname "${0}")" ]; then
    cd "$(dirname "${0}")"
fi
MAIN_DIR="${PWD}"
export MAIN_DIR
import_subdir_files includes

# Install needed storage driver tools
install_storage_driver_tools

# Install needed instance drivers
install_instance_drivers

echo "==> Checking for dependencies"
check_dependencies lxd lxc curl busybox dnsmasq iptables jq nc ping python3 yq git s3cmd sqlite3 rsync shuf setfacl setfattr socat swtpm dig tar2sqfs unsquashfs xz
if [ "${LXD_VM_TESTS:-0}" = "1" ]; then
  check_dependencies qemu-img "qemu-system-$(uname -m)" sgdisk
fi

echo "==> Checking test dependencies"
if ! check_dependencies devlxd-client fuidshift mini-oidc sysinfo; then
  ( cd .. && make test-binaries )
fi

# If no test image is specified, busybox-static will be needed by test/deps/import-busybox
if [ -z "${LXD_TEST_IMAGE:-}" ]; then
  BUSYBOX="$(command -v busybox)"
  if [ ! -e "${BUSYBOX}" ]; then
      echo "Please install busybox (busybox-static) or set LXD_TEST_IMAGE"
      exit 1
  fi

  if ldd "${BUSYBOX}" >/dev/null 2>&1; then
      echo "The testsuite requires ${BUSYBOX} to be a static binary"
      exit 1
  fi
fi

# find the path to lxc binary, not the shell wrapper function
_LXC="$(unset -f lxc; command -v lxc)"
readonly _LXC
export _LXC

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
  # Stop tracing everything
  { set +x; } 2>/dev/null
  if [ -z "${SHELL_TRACING:-}" ]; then
    echo "cleanup"
  fi

  # Avoid reentry by removing the traps
  trap - EXIT HUP INT TERM

  # Before setting +e, run the panic checker for any running LXD daemons.
  panic_checker "${TEST_DIR}"

  # Allow for failures
  set +e
  unset CLIENT_DEBUG
  unset SERVER_DEBUG
  unset SHELL_TRACING

  # Check if we failed and if so, provide debug info and possibly an inspection shell.
  if [ "${TEST_RESULT}" != "success" ]; then
    # Allow for inspection on failure
    if [ -n "${LXD_INSPECT:-}" ]; then
      # Re-execution prevention
      export LXD_INSPECT_INPROGRESS=true

      echo "==> FAILED TEST: ${TEST_CURRENT#test_} (${TEST_CURRENT_DESCRIPTION})"
      echo "==> Test result: ${TEST_RESULT}"
      # red
      PS1_PREFIX="\[\033[0;31m\]LXD-TEST\[\033[0m\]"

      echo -e "\033[0;33mDropping to a shell for inspection.\nOnce done, exit (Ctrl-D) to continue\033[0m"
      export PS1="${PS1_PREFIX} ${PS1:-\u@\h:\w\$ }"
      bash --norc
    fi

    echo ""
    echo "df -h output:"
    df -h

    if command -v ceph >/dev/null; then
      echo "::group::ceph status"
      ceph status --connect-timeout 5 || true
      echo "::endgroup::"
    fi

    # dmesg may contain oops, IO errors, crashes, etc
    # If there's a kernel stack trace, don't generate a collapsible group

    expandDmesg=no
    if journalctl --quiet --no-hostname --no-pager --boot=0 --lines=100 --dmesg --grep="Call Trace:" > /dev/null; then
      expandDmesg=yes
    fi

    if [ "${expandDmesg}" = "no" ]; then
      echo "::group::dmesg logs"
    else
      echo "dmesg logs"
    fi
    journalctl --quiet --no-hostname --no-pager --boot=0 --lines=100 --dmesg
    if [ "${expandDmesg}" = "no" ]; then
      echo "::endgroup::"
    fi
  fi

  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "==> Skipping cleanup (GitHub Action runner detected)"
  else
    echo "==> Cleaning up"

    [ -e "${LXD_TEST_IMAGE:-}" ] && rm "${LXD_TEST_IMAGE}"

    kill_oidc
    clear_ovn_nb_db
    mountpoint -q "${TEST_DIR}/dev" && umount -l "${TEST_DIR}/dev"
    cleanup_lxds "$TEST_DIR"

    mountpoint -q "${TEST_DIR}" && umount -l "${TEST_DIR}"
    rm -rf "${TEST_DIR}"
  fi

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "==> FAILED TEST: ${TEST_CURRENT#test_} (${TEST_CURRENT_DESCRIPTION})"
  fi
  echo "==> Test result: ${TEST_RESULT}"
}

# Must be set before cleanup()
TEST_CURRENT=setup
TEST_CURRENT_DESCRIPTION=setup
# shellcheck disable=SC2034
TEST_RESULT=failure

trap cleanup EXIT HUP INT TERM

# Import all the testsuites
import_subdir_files suites

if [ -n "${SHELL_TRACING:-}" ]; then
  set -x
fi

# Setup test directory
TEST_DIR="$(mktemp -d -t lxd-test.tmp.XXXX)"
chmod +x "${TEST_DIR}"

# Verify the dir chain is accessible for other users (other's execute bit has to be `x` or `t` (sticky))
# This is to catch if `sudo chmod +x ~` was not run and the TEST_DIR is under `~`
INACCESSIBLE_DIRS="$(namei -m "${TEST_DIR}" | awk '/^ d/ {if ($1 !~ "^d.*[xt]$") print $2}')"
if [ -n "${INACCESSIBLE_DIRS:-}" ]; then
    echo "Some directories are not accessible by other users" >&2
    namei -m "${TEST_DIR}"
    exit 1
fi

run_test() {
  TEST_CURRENT=${1}
  TEST_CURRENT_DESCRIPTION=${2:-${1#test_}}
  TEST_UNMET_REQUIREMENT=""
  cwd="${PWD}"

  echo "==> TEST BEGIN: ${TEST_CURRENT_DESCRIPTION}"
  START_TIME=$(date +%s)

  local skip=false

  # Skip test if requested.
  if [ -n "${LXD_SKIP_TESTS:-}" ]; then
    for testName in ${LXD_SKIP_TESTS}; do
      if [ "test_${testName}" = "${TEST_CURRENT}" ]; then
          echo "==> SKIP: ${TEST_CURRENT} as specified in LXD_SKIP_TESTS"
          skip=true
          break
      fi
    done
  fi

  if [ "${skip}" = false ]; then
    # Run test.
    ${TEST_CURRENT}

    # Check whether test was skipped due to unmet requirements, and if so check if the test is required and fail.
    if [ -n "${TEST_UNMET_REQUIREMENT}" ]; then
      if [ -n "${LXD_REQUIRED_TESTS:-}" ]; then
        for testName in ${LXD_REQUIRED_TESTS}; do
          if [ "test_${testName}" = "${TEST_CURRENT}" ]; then
              echo "==> REQUIRED: ${TEST_CURRENT} ${TEST_UNMET_REQUIREMENT}"
              false
              return
          fi
        done
      else
        # Skip test if its requirements are not met and is not specified in required tests.
        echo "==> SKIP: ${TEST_CURRENT} ${TEST_UNMET_REQUIREMENT}"
      fi
    fi
  fi

  END_TIME=$(date +%s)
  DURATION=$((END_TIME-START_TIME))
  cd "${cwd}"

  # output duration in blue
  echo -e "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION} (\033[0;34m${DURATION}s\033[0m)"

  if [ -n "${GITHUB_ACTIONS:-}" ]; then
      # strip the "test_" prefix to save the shorten test name along with its duration
      echo "${TEST_CURRENT#test_}|${DURATION}" >> "${GITHUB_STEP_SUMMARY}"
  fi
}

if [ -n "${GITHUB_ACTIONS:-}" ]; then
    # build a markdown table with the duration of each test
    echo "Test | Duration (s)" > "${GITHUB_STEP_SUMMARY}"
    echo ":--- | :---" >> "${GITHUB_STEP_SUMMARY}"
fi

# Spawn an interactive test shell when invoked as `./main.sh test-shell`.
# This is useful for quick interactions with LXD and its test suite.
if [ "${1:-"all"}" = "test-shell" ]; then
  # yellow
  export PS1="\[\033[0;33mLXD-TEST\033[0m ${PS1:-\u@\h:\w\$ }\]"

  # The `cleanup` handler must run when exiting a `test-shell` session but if the
  # last command returned non-0 (like `false`), we don't want to output the debug
  # information accompanying normal failures.
  #
  # If a test script runs into an error, the `cleanup` handler will already have
  # reported the relevant debug info so there is no need to repeat it when exiting
  # the `test-shell` environment.
  #
  # To do so, swallow any error code returned from the interactive \`test-shell\`.
  bash --rcfile test-shell.bashrc || true

  exit
fi

# Preflight check
if ldd "${_LXC}" | grep -F liblxc; then
    echo "lxc binary must not be linked with liblxc"
    exit 1
fi

if [ "${LXD_TMPFS:-0}" = "1" ]; then
  mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751 -o size=7G
fi

mkdir -p "${TEST_DIR}/dev"
mount -t tmpfs none "${TEST_DIR}"/dev
export LXD_DEVMONITOR_DIR="${TEST_DIR}/dev"

LXD_CONF=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_CONF

LXD_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_DIR
chmod +x "${LXD_DIR}"
spawn_lxd "${LXD_DIR}" true
LXD_ADDR=$(< "${LXD_DIR}/lxd.addr")
export LXD_ADDR

export LXD_SKIP_TESTS="${LXD_SKIP_TESTS:-}"

export LXD_REQUIRED_TESTS="${LXD_REQUIRED_TESTS:-}"

# This must be enough to accomodate the busybox testimage
export SMALL_ROOT_DISK="${SMALL_ROOT_DISK:-"root,size=32MiB"}"

# allow for running a specific set of tests
if [ "$#" -gt 0 ] && [ "$1" != "all" ] && [ "$1" != "cluster" ] && [ "$1" != "standalone" ]; then
  run_test "test_${1}"
  # shellcheck disable=SC2034
  TEST_RESULT=success
  exit
else
  # Since we are executing more than one test, cache the busybox testimage for reuse
  deps/import-busybox --save-image

  # Avoid `.tar.xz` extension that may conflict with some tests
  mv busybox.tar.xz busybox.tar.xz.cache
  export LXD_TEST_IMAGE="busybox.tar.xz.cache"
  echo "==> Saving testimage for reuse (${LXD_TEST_IMAGE})"
fi

if [ "${1:-"all"}" != "standalone" ]; then
    run_test test_clustering_enable "clustering enable"
    run_test test_clustering_edit_configuration "clustering config edit"
    run_test test_clustering_membership "clustering membership"
    run_test test_clustering_containers "clustering containers"
    run_test test_clustering_storage "clustering storage"
    run_test test_clustering_storage_single_node "clustering storage single node"
    run_test test_clustering_network "clustering network"
    run_test test_clustering_publish "clustering publish"
    run_test test_clustering_profiles "clustering profiles"
    run_test test_clustering_join_api "clustering join api"
    run_test test_clustering_shutdown_nodes "clustering shutdown"
    run_test test_clustering_projects "clustering projects"
    run_test test_clustering_metrics "clustering metrics"
    run_test test_clustering_update_cert "clustering update cert"
    run_test test_clustering_update_cert_reversion "clustering update cert reversion"
    run_test test_clustering_update_cert_token "clustering update cert token"
    run_test test_clustering_address "clustering address"
    run_test test_clustering_image_replication "clustering image replication"
    run_test test_clustering_dns "clustering DNS"
    run_test test_clustering_fan "clustering FAN"
    run_test test_clustering_recover "clustering recovery"
    run_test test_clustering_ha "clustering high availability"
    run_test test_clustering_handover "clustering handover"
    run_test test_clustering_rebalance "clustering rebalance"
    run_test test_clustering_remove_raft_node "clustering remove raft node"
    run_test test_clustering_failure_domains "clustering failure domains"
    run_test test_clustering_image_refresh "clustering image refresh"
    run_test test_clustering_evacuation "clustering evacuation"
    run_test test_clustering_move "clustering move"
    run_test test_clustering_remove_members "clustering config remove members"
    run_test test_clustering_autotarget "clustering autotarget member"
    run_test test_clustering_upgrade "clustering upgrade"
    run_test test_clustering_upgrade_large "clustering upgrade_large"
    run_test test_clustering_downgrade "clustering downgrade"
    run_test test_clustering_groups "clustering groups"
    run_test test_clustering_events "clustering events"
    run_test test_clustering_uuid "clustering uuid"
    run_test test_clustering_trust_add "clustering trust add"
    run_test test_clustering_waitready "clustering waitready"
fi

if [ "${1:-"all"}" != "cluster" ]; then
    run_test test_concurrent "concurrent startup"
    run_test test_concurrent_exec "concurrent exec"
    run_test test_database_restore "database restore"
    run_test test_database_no_disk_space "database out of disk space"
    run_test test_sql "lxd sql"
    run_test test_tls_restrictions "TLS restrictions"
    run_test test_tls_version "TLS version"
    run_test test_completions "CLI completions"
    run_test test_oidc "OpenID Connect"
    run_test test_authorization "Authorization"
    run_test test_certificate_edit "Certificate edit"
    run_test test_basic_usage "basic usage"
    run_test test_duplicate_detection "duplicate detection"
    run_test test_basic_version "basic version"
    run_test test_server_info "server info"
    run_test test_remote_url "remote url handling"
    run_test test_remote_url_with_token "remote token handling"
    run_test test_remote_admin "remote administration"
    run_test test_remote_usage "remote usage"
    run_test test_vm_empty "Empty VM"
    run_test test_projects_default "default project"
    run_test test_projects_copy "copy/move between projects"
    run_test test_projects_crud "projects CRUD operations"
    run_test test_projects_containers "containers inside projects"
    run_test test_projects_snapshots "snapshots inside projects"
    run_test test_projects_backups "backups inside projects"
    run_test test_projects_profiles "profiles inside projects"
    run_test test_projects_profiles_default "profiles from the global default project"
    run_test test_projects_images "images inside projects"
    run_test test_projects_images_default "images from the global default project"
    run_test test_projects_storage "projects and storage pools"
    run_test test_projects_network "projects and networks"
    run_test test_projects_limits "projects limits"
    run_test test_projects_usage "projects usage"
    run_test test_projects_yaml "projects with yaml initialization"
    run_test test_projects_before_init "project operations before init"
    run_test test_projects_restrictions "projects restrictions"
    run_test test_projects_images_volume "projects images volume"
    run_test test_projects_backups_volume "projects backups volume"
    run_test test_container_devices_disk "container devices - disk"
    run_test test_container_devices_disk_restricted "container devices - disk - restricted"
    run_test test_container_devices_nic_p2p "container devices - nic - p2p"
    run_test test_container_devices_nic_bridged "container devices - nic - bridged"
    run_test test_container_devices_nic_bridged_acl "container devices - nic - bridged - acl"
    run_test test_container_devices_nic_bridged_filtering "container devices - nic - bridged - filtering"
    run_test test_container_devices_nic_bridged_vlan "container devices - nic - bridged - vlan"
    run_test test_container_devices_nic_physical "container devices - nic - physical"
    run_test test_container_devices_nic_macvlan "container devices - nic - macvlan"
    run_test test_container_devices_nic_ipvlan "container devices - nic - ipvlan"
    run_test test_container_devices_nic_sriov "container devices - nic - sriov"
    run_test test_container_devices_nic_routed "container devices - nic - routed"
    run_test test_container_devices_none "container devices - none"
    run_test test_container_devices_infiniband_physical "container devices - infiniband - physical"
    run_test test_container_devices_infiniband_sriov "container devices - infiniband - sriov"
    run_test test_container_devices_proxy "container devices - proxy"
    run_test test_container_devices_gpu "container devices - gpu"
    run_test test_container_devices_unix "container devices - unix"
    run_test test_container_devices_tpm "container devices - tpm"
    run_test test_container_move "container server-side move"
    run_test test_container_syscall_interception "container syscall interception"
    run_test test_security "security features"
    run_test test_security_protection "container protection"
    run_test test_image_expiry "image expiry"
    run_test test_image_list_all_aliases "image list all aliases"
    run_test test_image_list_remotes "image list of simplestream remotes"
    run_test test_image_auto_update "image auto-update"
    run_test test_image_prefer_cached "image prefer cached"
    run_test test_image_import_dir "import image from directory"
    run_test test_image_import_existing_alias "import existing image from alias"
    run_test test_image_refresh "image refresh"
    run_test test_image_acl "image acl"
    run_test test_images_public "public images"
    run_test test_cloud_init "cloud-init"
    run_test test_exec "exec"
    run_test test_exec_exit_code "exec exit code"
    run_test test_lxd_benchmark_basic "lxd-benchmark basic init/start/stop/delete"
    run_test test_shutdown "lxd shutdown sequence"
    run_test test_snapshots "container snapshots"
    run_test test_snap_restore "snapshot restores"
    run_test test_snap_expiry "snapshot expiry"
    run_test test_snap_schedule "snapshot scheduling"
    run_test test_snap_volume_db_recovery "snapshot volume database record recovery"
    run_test test_snap_fail "snapshot creation failure"
    run_test test_config_profiles "profiles and configuration"
    run_test test_config_edit "container configuration edit"
    run_test test_property "container property"
    run_test test_config_edit_container_snapshot_pool_config "container and snapshot volume configuration edit"
    run_test test_container_metadata "manage container metadata and templates"
    run_test test_container_snapshot_config "container snapshot configuration"
    run_test test_server_config "server configuration"
    run_test test_filemanip "file manipulations"
    run_test test_filemanip_req_content_type "request content-type header verification during file push"
    run_test test_network "network management"
    run_test test_network_acl "network ACL management"
    run_test test_network_forward "network address forwards"
    run_test test_network_zone "network DNS zones"
    run_test test_network_ovn "OVN network management"
    run_test test_idmap "id mapping"
    run_test test_template "file templating"
    run_test test_pki "PKI mode"
    run_test test_devlxd "/dev/lxd"
    run_test test_devlxd_volume_management "devLXD volume management"
    run_test test_fuidshift "fuidshift"
    run_test test_migration "migration"
    run_test test_fdleak "fd leak"
    run_test test_storage "storage"
    run_test test_storage_volume_snapshots "storage volume snapshots"
    run_test test_init_auto "lxd init auto"
    run_test test_init_dump "lxd init dump"
    run_test test_init_interactive "lxd init interactive"
    run_test test_init_preseed "lxd init preseed"
    run_test test_storage_profiles "storage profiles"
    run_test test_container_recover "container recover"
    run_test test_bucket_recover "bucket recover"
    run_test test_get_operations "test_get_operations"
    run_test test_storage_volume_attach "attaching storage volumes"
    run_test test_storage_driver_btrfs "btrfs storage driver"
    run_test test_storage_driver_ceph "ceph storage driver"
    run_test test_storage_driver_cephfs "cephfs storage driver"
    run_test test_storage_driver_dir "dir storage driver"
    run_test test_storage_driver_zfs "zfs storage driver"
    run_test test_storage_driver_pure "pure storage driver"
    run_test test_storage_buckets "storage buckets"
    run_test test_storage_volume_import "storage volume import"
    run_test test_storage_volume_initial_config "storage volume initial configuration"
    run_test test_resources "resources"
    run_test test_resources_bcache "resources bcache"
    run_test test_kernel_limits "kernel limits"
    run_test test_console "console"
    run_test test_query "query"
    run_test test_storage_local_volume_handling "storage local volume handling"
    run_test test_backup_import "backup import"
    run_test test_backup_export "backup export"
    run_test test_backup_rename "backup rename"
    run_test test_backup_volume_export "backup volume export"
    run_test test_backup_export_import_instance_only "backup export and import instance only"
    run_test test_backup_metadata "backup metadata checks for containers and custom storage volumes"
    run_test test_backup_volume_rename_delete "backup volume rename and delete"
    run_test test_backup_instance_uuid "backup instance and check instance UUIDs"
    run_test test_backup_volume_expiry "backup volume expiry"
    run_test test_backup_export_import_recover "backup export, import, and recovery"
    run_test test_container_local_cross_pool_handling "container local cross pool handling"
    run_test test_incremental_copy "incremental container copy"
    run_test test_profiles_project_default "profiles in default project"
    run_test test_profiles_project_images_profiles "profiles in project with images and profiles enabled"
    run_test test_profiles_project_images "profiles in project with images enabled and profiles disabled"
    run_test test_profiles_project_profiles "profiles in project with images disabled and profiles enabled"
    run_test test_filtering "API filtering"
    run_test test_warnings "Warnings"
    run_test test_metrics "Metrics"
    run_test test_storage_volume_recover "Recover storage volumes"
    run_test test_storage_volume_recover_by_container "Recover storage volumes by container"
    run_test test_syslog_socket "Syslog socket"
    run_test test_lxd_user "lxd user"
    run_test test_waitready "waitready"
fi

# shellcheck disable=SC2034
TEST_RESULT=success
