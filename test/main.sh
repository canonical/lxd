#!/bin/bash
set -eu
set -o pipefail

# === pre-flight checks === #
# root is required
if [ "${USER:-'root'}" != "root" ]; then
  echo "The testsuite must be run as root." >&2
  exit 1
fi

# Avoid accidental re-execution
if [ -n "${LXD_INSPECT_INPROGRESS:-}" ]; then
    echo "Refusing to run tests from inside a LXD_INSPECT session" >&2
    exit 1
fi

# Create LXD_LOGS if needed
[ -n "${LXD_LOGS:-}" ] && mkdir -p "${LXD_LOGS}"

# Create GOCOVERDIR if needed
[ -n "${GOCOVERDIR:-}" ] && mkdir -p "${GOCOVERDIR}"

# === export needed environment variables with defaults === #
# OVN
export LXD_OVN_NB_CA_CRT_FILE="${LXD_OVN_NB_CA_CRT_FILE:-}"
export LXD_OVN_NB_CLIENT_CRT_FILE="${LXD_OVN_NB_CLIENT_CRT_FILE:-}"
export LXD_OVN_NB_CLIENT_KEY_FILE="${LXD_OVN_NB_CLIENT_KEY_FILE:-}"
if [ -d "/snap/microovn/current/commands" ]; then
    # Add microovn snap commands to PATH if not there already
    [[ "${PATH}" != *"/snap/microovn/current/commands"* ]] && PATH="${PATH}:/snap/microovn/current/commands"

    # Handle microovn certificates
    if [[ "${LXD_OVN_NB_CONNECTION:-}" =~ ^ssl: ]]; then
      [ -z "${LXD_OVN_NB_CLIENT_CRT_FILE}" ] && LXD_OVN_NB_CLIENT_CRT_FILE=/var/snap/microovn/common/data/pki/client-cert.pem
      [ -z "${LXD_OVN_NB_CLIENT_KEY_FILE}" ] && LXD_OVN_NB_CLIENT_KEY_FILE=/var/snap/microovn/common/data/pki/client-privkey.pem
      [ -z "${LXD_OVN_NB_CA_CRT_FILE}" ]     && LXD_OVN_NB_CA_CRT_FILE=/var/snap/microovn/common/data/pki/cacert.pem
    fi
fi

# Ceph
export LXD_CEPH_CLUSTER="${LXD_CEPH_CLUSTER:-"ceph"}"
export LXD_CEPH_CEPHFS="${LXD_CEPH_CEPHFS:-"cephfs"}"

export GOTOOLCHAIN=local # Avoid downloading toolchain
if [ -z "${GOPATH:-}" ] && command -v go >/dev/null; then
    GOPATH="$(go env GOPATH)"
fi

# Add GOPATH/bin to PATH if not there already
if [ -n "${GOPATH:-}" ] && [[ "${PATH}" != *"${GOPATH}/bin"* ]]; then
    PATH="${GOPATH}/bin:${PATH}"
fi
export PATH

# Don't translate lxc output for parsing in it in tests.
export LC_ALL="C"

# Force UTC for consistency
export TZ="UTC"

# Prevent proxy usage for some host names/IPs (comma-separated list)
export NO_PROXY="${NO_PROXY:-"127.0.0.1"}"

# Detect architecture name for later use
ARCH="$(dpkg --print-architecture || echo "amd64")"
export ARCH

export LXD_VM_TESTS="${LXD_VM_TESTS:-1}"
export CLIENT_DEBUG="" SERVER_DEBUG="" SHELL_TRACING=""
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

# Default sizes to be used with storage pools
export DEFAULT_VOLUME_SIZE="24MiB"
export DEFAULT_POOL_SIZE="3GiB"

export LXD_SKIP_TESTS="${LXD_SKIP_TESTS:-}"
export LXD_REQUIRED_TESTS="${LXD_REQUIRED_TESTS:-}"

# This must be enough to accommodate the busybox testimage
export SMALL_ROOT_DISK="${SMALL_ROOT_DISK:-"root,size=32MiB"}"

# This must be enough to accommodate the ubuntu-minimal-daily:24.04 image
export SMALLEST_VM_ROOT_DISK="3584MiB"
export SMALL_VM_ROOT_DISK="${SMALL_VM_ROOT_DISK:-"root,size=${SMALLEST_VM_ROOT_DISK}"}"

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

dependency_checks() {
  echo "==> Checking for dependencies"
  check_dependencies lxd lxc curl busybox dnsmasq expect iptables jq nc ping python3 yq git s3cmd sqlite3 rsync shuf setfacl setfattr socat swtpm dig tar2sqfs unsquashfs xz
  if [ "${LXD_VM_TESTS}" = "1" ]; then
    check_dependencies qemu-img "qemu-system-$(uname -m)" sgdisk
  fi
  if ! check_dependencies minio mc; then
    download_minio
  fi

  echo "==> Checking test dependencies"
  if ! check_dependencies devlxd-client lxd-client fuidshift mini-oidc sysinfo; then
    make -C "${MAIN_DIR}/.." test-binaries
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

    # Cache the busybox testimage for reuse
    deps/import-busybox --save-image

    # Avoid `.tar.xz` extension that may conflict with some tests
    mv busybox.tar.xz busybox.tar.xz.cache
    export LXD_TEST_IMAGE="busybox.tar.xz.cache"
    echo "==> Saving testimage for reuse (${LXD_TEST_IMAGE})"
  fi
}

# `main.sh` needs to be executed from inside the `test/` directory
if [ "${PWD}" != "$(dirname "${0}")" ]; then
    cd "$(dirname "${0}")"
fi
readonly MAIN_DIR="${PWD}"
export MAIN_DIR
export LXD_BACKEND="${LXD_BACKEND:-"dir"}"

# Support multiple backends selection
LXD_BACKENDS="${LXD_BACKENDS:-"${LXD_BACKEND}"}"
if [ "${LXD_BACKENDS}" = "all" ]; then
  LXD_BACKENDS="btrfs ceph dir lvm zfs random"
elif [ "${LXD_BACKENDS}" = "fasts" ]; then
  LXD_BACKENDS="btrfs dir"
elif [ "${LXD_BACKENDS}" = "fast" ]; then
  # Pick on of btrfs or dir
  LXD_BACKENDS="btrfs"
  if [ $(( "${GITHUB_RUN_ID:-"${RANDOM}"}" % 2 )) -eq 0 ]; then
    LXD_BACKENDS="dir"
  fi
  echo "::notice::fast backend=${LXD_BACKENDS}"
fi
readonly LXD_BACKENDS

import_subdir_files includes

# Install needed instance drivers
install_instance_drivers

dependency_checks

# find the path to lxc binary, not the shell wrapper function
_LXC="$(unset -f lxc; command -v lxc)"
readonly _LXC
export _LXC

# Set ulimit to ensure core dump is outputted.
ulimit -c unlimited
echo '|/bin/sh -c $@ -- eval exec gzip --fast > /var/crash/core-%e.%p.gz' > /proc/sys/kernel/core_pattern

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
  unset CLIENT_DEBUG SERVER_DEBUG SHELL_TRACING

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

    kill_oidc
    clear_ovn_nb_db
    mountpoint -q "${TEST_DIR}/dev" && umount -l "${TEST_DIR}/dev"
    cleanup_lxds "$TEST_DIR"

    mountpoint -q "${TEST_DIR}" && umount -l "${TEST_DIR}"
    rm -rf "${TEST_DIR}"
  fi

  # build a markdown table with the duration of each test
  (
    echo "Test (${LXD_BACKEND}) | Duration (s)"
    echo ":--- | :---"
    for t in "${!durations[@]}"; do
        echo "${t} | ${durations[$t]}"
    done | sort
  ) > "${GITHUB_STEP_SUMMARY:-"/dev/stdout"}"

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    echo "==> FAILED TEST: ${TEST_CURRENT#test_}"
  fi
  echo "==> Test result: ${TEST_RESULT}"
}

# Must be set before cleanup()
TEST_CURRENT=setup
# shellcheck disable=SC2034
TEST_RESULT=failure

# Record tests durations info
declare -A durations

trap cleanup EXIT HUP INT TERM

# Import all the testsuites
import_subdir_files suites

# Run all tests in a group
run_test_group() {
    local -n group_ref="test_group_${1}"
    local SHUF='cat'
    [ "${LXD_RANDOMIZE_TESTS:-0}" = "1" ] && SHUF='shuf'

    for t in $(printf '%s\n' "${group_ref[@]}" | "${SHUF}"); do
      run_test_n_times "${t}"
    done
}

# Run a test multiple times
run_test_n_times() {
  local name="${1}"
  local iterCount=1
  while [ "${iterCount}" -le "${LXD_REPEAT_TESTS:-1}" ]; do
    run_test "test_${name}"
    iterCount=$((iterCount + 1))
  done
}

# Run a single test
run_test() {
  TEST_CURRENT=${1}
  TEST_CURRENT_DESCRIPTION="${TEST_CURRENT#test_} on ${LXD_BACKEND}"
  TEST_UNMET_REQUIREMENT=""
  cwd="${PWD}"

  if [ "${RUN_COUNT:-0}" -ne 0 ] && [ "${LXD_REPEAT_TESTS:-1}" -ne 1 ]; then
    TEST_CURRENT_DESCRIPTION="${TEST_CURRENT_DESCRIPTION} (${RUN_COUNT}/${LXD_REPEAT_TESTS})"
  fi

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

    if [[ "${TEST_CURRENT}" =~ ^test_snap_.*$ ]]; then
      [ -e "/snap/lxd/current" ] || spawn_lxd_snap

      # For snap based tests, the lxc and lxc_remote functions MUST not be used
      unset -f lxc lxc_remote
    elif [ -e "/snap/lxd/current" ]; then
      kill_lxd_snap
    fi

    # If there is '_vm' in the test name, then VM tests are expected to be run.
    # If LXD_VM_TESTS=1, then VM tests can be run.
    if [[ "${TEST_CURRENT}" =~ ^test_.*_vm.*$ ]] && [ "${LXD_VM_TESTS}" = "0" ]; then
      export TEST_UNMET_REQUIREMENT="VM test currently disabled due to LXD_VM_TESTS=0"
    else
      # Check for any core dump before running the test
      if ! check_empty /var/crash/; then
        echo "==> CORE: coredumps found before running the test"
        false
      fi

      # Run test.
      ${TEST_CURRENT}

      # XXX: Ignore qemu core dumps
      if [ -n "$(ls /var/crash/core-qemu-system-x86.*.gz)" ]; then
        echo "==> IGNORE: Ignoring qemu core dumps"
        rm -f /var/crash/core-qemu-system-x86.*.gz
      fi

      # Check for any core dump after running the test
      if ! check_empty /var/crash/; then
        echo "==> CORE: coredumps found after running the test"
        false
      fi
    fi

    # Check whether test was skipped due to unmet requirements, and if so check if the test is required and fail.
    if [ -n "${TEST_UNMET_REQUIREMENT}" ]; then
      if [ -n "${LXD_REQUIRED_TESTS:-}" ]; then
        for testName in ${LXD_REQUIRED_TESTS}; do
          if [ "test_${testName}" = "${TEST_CURRENT}" ]; then
              echo "==> REQUIRED: ${TEST_CURRENT} ${TEST_UNMET_REQUIREMENT}"
              false
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
  durations["${TEST_CURRENT#test_}"]="${DURATION}"
  cd "${cwd}"

  # output duration in blue
  echo -e "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION} (\033[0;34m${DURATION}s\033[0m)"
}

# Preflight check
if ldd "${_LXC}" | grep -F liblxc; then
    echo "lxc binary must not be linked with liblxc"
    exit 1
fi

# Only spawn a new LXD if not done yet.
spawn_initial_lxd() {
    install_storage_driver_tools

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

    echo "==> Available storage backends: $(available_storage_backends | sort)"
    if [ "$LXD_BACKEND" != "random" ] && ! storage_backend_available "$LXD_BACKEND"; then
    echo "Storage backend \"$LXD_BACKEND\" is not available"
    exit 1
    fi
    echo "==> Using storage backend ${LXD_BACKEND}"

    import_storage_backends

    if [ "${LXD_TMPFS:-0}" = "1" ]; then
      mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751 -o size=8G
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
    LXD_ADDR="$(< "${LXD_DIR}/lxd.addr")"
    export LXD_ADDR
}

# Spawn an interactive test shell when invoked as `./main.sh test-shell`.
# This is useful for quick interactions with LXD and its test suite.
if [ "${1:-"all"}" = "test-shell" ]; then
  spawn_initial_lxd
  bash --rcfile test-shell.bashrc || true
  TEST_CURRENT="test-shell"
  TEST_RESULT=success
  exit 0
fi

if [ -n "${SHELL_TRACING:-}" ]; then
  set -x
fi

# If no args, default to group:all
if [ "$#" -eq 0 ]; then
  set -- "group:all"
fi

# Run tests against all requested backends
for LXD_BACKEND in ${LXD_BACKENDS}; do
  spawn_initial_lxd

  for arg in "$@"; do
    if [[ "${arg}" == group:* ]]; then
      group_name="${arg#group:}"
      declare -p test_group_"${group_name}" >/dev/null 2>&1 || {
        echo "Unknown test group: ${group_name}" >&2
        exit 1
      }
      run_test_group "${group_name}"
    else
      declare -f "test_${arg}" >/dev/null 2>&1 || {
        echo "Unknown test: test_${arg}" >&2
        exit 1
      }
      run_test_n_times "${arg}"
    fi
  done

  TEST_RESULT=success

  cleanup
done

# Avoid running cleanup again
trap - EXIT HUP INT TERM
