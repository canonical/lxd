#!/bin/bash
set -eu
set -o pipefail

export LC_ALL=C.UTF-8  # Ensure consistency in sorting/grep/etc

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
export LXD_REPEAT_TESTS="${LXD_REPEAT_TESTS:-1}"

# This must be enough to accommodate the busybox testimage
export SMALL_ROOT_DISK="${SMALL_ROOT_DISK:-"root,size=32MiB"}"

# This must be enough to accommodate the ubuntu-minimal-daily:24.04 image
export SMALLEST_VM_ROOT_DISK="3584MiB"
export SMALL_VM_ROOT_DISK="${SMALL_VM_ROOT_DISK:-"root,size=${SMALLEST_VM_ROOT_DISK}"}"

# shellcheck disable=SC2034
LXD_NETNS=""

# LXD_XXX_DIRs for multiple LXD instances
export LXD_ONE_DIR="" LXD_TWO_DIR="" LXD_THREE_DIR="" LXD_FOUR_DIR="" LXD_FIVE_DIR="" LXD_SIX_DIR="" LXD_SEVEN_DIR="" LXD_EIGHT_DIR="" LXD_NINE_DIR=""
# nsX variables for multiple network namespaces
export ns1="" ns2="" ns3="" ns4="" ns5="" ns6="" ns7="" ns8="" ns9=""

export prefix="lxd$$"
export bridge="${prefix}"

import_subdir_files() {
    test "$1"
    local file
    for file in "$1"/*.sh; do
        # shellcheck disable=SC1090
        . "$file"
    done
}

run_dependency_checks() {
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
# Expand LXD_BACKENDS
LXD_BACKENDS="${LXD_BACKENDS:-"${LXD_BACKEND:-}"}"
if [ "${LXD_BACKENDS}" = "all" ]; then
  LXD_BACKENDS="btrfs ceph dir lvm zfs random"
elif [ "${LXD_BACKENDS}" = "fasts" ]; then
  LXD_BACKENDS="btrfs dir"
elif [ "${LXD_BACKENDS}" = "fast" ]; then
  # Pick one of btrfs or dir
  LXD_BACKENDS="btrfs"
  if [ $(( "${GITHUB_RUN_ID:-"${RANDOM}"}" % 2 )) -eq 0 ]; then
    LXD_BACKENDS="dir"
  fi
  echo "::notice::fast backend=${LXD_BACKENDS}"
fi
readonly LXD_BACKENDS

# Determine active tests
# If LXD_BACKEND is set, we only run that one.
# Otherwise, we run all backends in LXD_BACKENDS.
active_backends="${LXD_BACKEND:-${LXD_BACKENDS}}"

import_subdir_files includes

# Import all storage backends once
import_storage_backends

# Install needed instance drivers
install_instance_drivers

run_dependency_checks

# find the path to lxc binary, not the shell wrapper function
_LXC="$(unset -f lxc; command -v lxc)"
readonly _LXC
export _LXC

# Set ulimit to ensure core dump is outputted.
ulimit -c unlimited
echo '|/bin/sh -c $@ -- eval exec gzip --fast > /var/crash/core-%e.%p.gz' > /proc/sys/kernel/core_pattern

# Check for core dumps, ignoring qemu crashes (known issue)
check_coredumps() {
  if ! compgen -G "/var/crash/core-*.gz" > /dev/null; then
    return 0  # No core dumps at all
  fi

  # Ignore qemu core dumps (known crasher, to be fixed later)
  # TODO: look at the core dump along with debug builds of qemu to track the
  #       root cause.
  # Enable extended globbing for the !(pattern) syntax
  shopt -s extglob
  if compgen -G "/var/crash/core-!(qemu-system-*).gz" > /dev/null 2>&1; then
    echo "==> CORE: coredumps found"
    ls -la /var/crash/
    shopt -u extglob
    return 1
  fi
  shopt -u extglob

  # Only QEMU core dumps (known issue)
  echo "::notice::==> CORE: QEMU core dump ignored"

  return 0
}

# Check if the current backend is the last one to be tested in the current context
is_matrix_final_step() {
  local last_expected_backend
  # shellcheck disable=SC2086
  set -- ${LXD_BACKENDS}
  eval "last_expected_backend=\${$#}"

  [ "${LXD_BACKEND}" = "${last_expected_backend}" ]
}

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

  # Run cleanup commands silently
  local ORIG_CLIENT_DEBUG="${CLIENT_DEBUG}" ORIG_SERVER_DEBUG="${SERVER_DEBUG}" ORIG_SHELL_TRACING="${SHELL_TRACING}"
  unset CLIENT_DEBUG SERVER_DEBUG SHELL_TRACING

  # Check if we failed and if so, provide debug info and possibly an inspection shell.
  if [ "${TEST_RESULT}" != "success" ]; then
    # Allow for inspection on failure
    if [ -n "${LXD_INSPECT:-}" ]; then
      # Re-execution prevention
      export LXD_INSPECT_INPROGRESS=true

      echo "==> FAILED TEST: ${TEST_CURRENT} (${TEST_CURRENT_DESCRIPTION})"
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

  if [ -n "${GITHUB_ACTIONS:-}" ] && is_matrix_final_step; then
    echo "==> Skipping cleanup (final step)"
  else
    echo "==> Cleaning up"

    kill_oidc
    clear_ovn_nb_db
    mountpoint -q "${TEST_DIR}/dev" && umount -l "${TEST_DIR}/dev"
    cleanup_lxds "$TEST_DIR"

    mountpoint -q "${TEST_DIR}" && umount -l "${TEST_DIR}"
    rm -rf "${TEST_DIR}"

    # Fail if any loop devices were left behind
    if losetup -l | grep -F "lxdtest-" | grep -wF '(deleted)'; then
      echo "ERROR: loop devices were left behind"
      return 1
    fi
  fi

  echo ""
  echo ""
  if [ "${TEST_RESULT}" != "success" ]; then
    # Generate the duration table on failure as it won't be generated at the end
    # of the script
    generate_duration_table
    echo "==> FAILED TEST: ${TEST_CURRENT}"
  fi
  echo "==> Test result: ${TEST_RESULT}"

  # Restore original debug/tracing settings
  CLIENT_DEBUG="${ORIG_CLIENT_DEBUG}" SERVER_DEBUG="${ORIG_SERVER_DEBUG}" SHELL_TRACING="${ORIG_SHELL_TRACING}"
}

# Must be set before cleanup()
TEST_CURRENT=setup
TEST_CURRENT_DESCRIPTION="setup"
# shellcheck disable=SC2034
TEST_RESULT=failure

# Record tests durations info per backend
# Structure: durations[test_name,backend]=duration_seconds
declare -A durations

# Generate markdown table with test durations across backends
generate_duration_table() {
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
        for f in "${MAIN_DIR}"/.durations.*; do
            [ -e "${f}" ] || continue
            while IFS='=' read -r key value; do
                durations["${key}"]="${value}"
            done < "${f}"
        done
    fi

    # Collect all unique test names
    local -a test_names=()
    for key in "${!durations[@]}"; do
        local test_name="${key%,*}"
        if [[ ! " ${test_names[*]} " =~ (^|[[:space:]])${test_name}([[:space:]]|$) ]]; then
            test_names+=("${test_name}")
        fi
    done

    # Sort test names using version sort (-V) so numbered test runs like "test (1/10)" are ordered naturally.
    mapfile -t test_names < <(printf '%s\n' "${test_names[@]}" | sort -V)

    # Calculate column widths and totals
    local test_col_width=5  # "TOTAL"
    local -a backends
    read -ra backends <<< "${LXD_BACKENDS}"
    local -A backend_col_widths
    local -A backend_totals

    # helper vars for average calculation
    local -A group_sums
    local -A group_counts
    local last_base=""

    for backend in "${backends[@]}"; do
        backend_col_widths[${backend}]=${#backend}
        backend_totals[${backend}]=0
        group_sums[${backend}]=0
        group_counts[${backend}]=0
    done

    # Pre-calculate widths
    for test_name in "${test_names[@]}"; do
        [ ${#test_name} -gt "${test_col_width}" ] && test_col_width=${#test_name}

        # Logic to handle averaging for Total calculation
        local current_base=""
        local current_is_group=0
        if [[ "${test_name}" =~ ^(.*)\ \([0-9]+/[0-9]+\)$ ]]; then
             current_base="${BASH_REMATCH[1]}"
             current_is_group=1
        else
             current_base="${test_name}"
        fi

        # If group changed, add average of previous group to total
        if [ -n "${last_base}" ] && [ "${current_base}" != "${last_base}" ]; then
             for backend in "${backends[@]}"; do
                 if [ "${group_counts[${backend}]}" -gt 0 ]; then
                     local avg
                     avg=$(awk "BEGIN {printf \"%.2f\", ${group_sums[${backend}]} / ${group_counts[${backend}]}}")
                     backend_totals[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${backend_totals[${backend}]} + ${avg}}")
                 fi
                 # Reset group stats
                 group_sums[${backend}]=0
                 group_counts[${backend}]=0
             done
        fi

        last_base="${current_base}"

        for backend in "${backends[@]}"; do
            local duration="${durations[${test_name},${backend}]:-}"
            local cell_text="-"
            if [ -n "${duration}" ]; then
                cell_text="${duration}s"
                if [ "${current_is_group}" -eq 1 ]; then
                    group_sums[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${group_sums[${backend}]} + ${duration}}")
                    group_counts[${backend}]=$((group_counts[${backend}] + 1))
                else
                    # Non-grouped item adds directly to total
                    backend_totals[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${backend_totals[${backend}]} + ${duration}}")
                fi
            fi
            [ ${#cell_text} -gt "${backend_col_widths[${backend}]}" ] && backend_col_widths[${backend}]=${#cell_text}
        done
    done

    # Handle last group
    for backend in "${backends[@]}"; do
         if [ "${group_counts[${backend}]}" -gt 0 ]; then
             local avg
             avg=$(awk "BEGIN {printf \"%.2f\", ${group_sums[${backend}]} / ${group_counts[${backend}]}}")
             backend_totals[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${backend_totals[${backend}]} + ${avg}}")
         fi
    done

    # Check total label width
    local total_label="TOTAL"
    if [ "${LXD_REPEAT_TESTS}" -gt 1 ]; then
        total_label="TOTAL (avg)"
    fi
    [ ${#total_label} -gt "${test_col_width}" ] && test_col_width=${#total_label}

    # Check avg label width for the longest test name
    local avg_suffix_len=6 # " (avg)"
    local max_test_len=0
    for test_name in "${test_names[@]}"; do
        if [[ "${test_name}" =~ ^(.*)\ \([0-9]+/[0-9]+\)$ ]]; then
             local base="${BASH_REMATCH[1]}"
             [ ${#base} -gt "${max_test_len}" ] && max_test_len=${#base}
        elif [ ${#test_name} -gt "${max_test_len}" ]; then
             max_test_len=${#test_name}
        fi
    done

    if [ "${LXD_REPEAT_TESTS}" -gt 1 ]; then
        local potential_width=$((max_test_len + avg_suffix_len))
        [ "${potential_width}" -gt "${test_col_width}" ] && test_col_width=${potential_width}
    fi

    # Check total value widths
    for backend in "${backends[@]}"; do
        local total_text="${backend_totals[${backend}]}s"
        [ ${#total_text} -gt "${backend_col_widths[${backend}]}" ] && backend_col_widths[${backend}]=${#total_text}

        # Reset totals for the actual printing phase.
        # At this point backend_totals has only been used to size the columns above.
        # The reporting/printing loop will recompute these totals from scratch while
        # generating the table, so we intentionally clear them here to avoid mixing
        # the sizing pass with the final results.
        backend_totals[${backend}]=0
    done

    {
        # Header row
        printf "%-${test_col_width}s" "Test"
        for backend in "${backends[@]}"; do
            printf " | %${backend_col_widths[${backend}]}s" "${backend}"
        done
        echo ""

        # Alignment row
        printf ":%-$((test_col_width-1))s" "$(printf '%*s' $((test_col_width-1)) '' | tr ' ' '-')"
        for backend in "${backends[@]}"; do
            printf " | %s:" "$(printf '%*s' $((backend_col_widths[${backend}]-1)) '' | tr ' ' '-')"
        done
        echo ""

        local last_base=""
        local is_group=0
        local -A print_group_sums
        local -A print_group_counts

        # Initialize group stats
        for backend in "${backends[@]}"; do
             print_group_sums[${backend}]=0
             print_group_counts[${backend}]=0
        done

        # Data rows
        for test_name in "${test_names[@]}"; do
            local current_base=""
            local current_is_group=0

            if [[ "${test_name}" =~ ^(.*)\ \([0-9]+/[0-9]+\)$ ]]; then
                current_base="${BASH_REMATCH[1]}"
                current_is_group=1
            else
                current_base="${test_name}"
                current_is_group=0
            fi

            # Check if group changed
            if [ -n "${last_base}" ] && [ "${current_base}" != "${last_base}" ]; then
                if [ "${is_group}" -eq 1 ]; then
                    printf "%-${test_col_width}s" "${last_base} (avg)"
                    for backend in "${backends[@]}"; do
                        local sum="${print_group_sums[${backend}]:-0}"
                        local count="${print_group_counts[${backend}]:-0}"
                        local cell_text="-"
                        if [ "${count}" -gt 0 ]; then
                            local avg
                            avg=$(awk "BEGIN {printf \"%.2f\", ${sum} / ${count}}")
                            cell_text="${avg}s"
                            backend_totals[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${backend_totals[${backend}]} + ${avg}}")
                        fi
                        printf " | %${backend_col_widths[${backend}]}s" "${cell_text}"

                        # Reset for next usage (though we can just ignore, safer to reset)
                        print_group_sums[${backend}]=0
                        print_group_counts[${backend}]=0
                    done
                    echo ""
                fi
            fi

            last_base="${current_base}"
            is_group="${current_is_group}"

            printf "%-${test_col_width}s" "${test_name}"
            for backend in "${backends[@]}"; do
                local duration="${durations[${test_name},${backend}]:-}"
                local cell_text
                if [ -n "${duration}" ]; then
                    cell_text="${duration}s"
                    if [ "${current_is_group}" -eq 1 ]; then
                        print_group_sums[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${print_group_sums[${backend}]} + ${duration}}")
                        print_group_counts[${backend}]=$((print_group_counts[${backend}] + 1))
                    else
                        backend_totals[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${backend_totals[${backend}]} + ${duration}}")
                    fi
                else
                    cell_text="-"
                fi
                printf " | %${backend_col_widths[${backend}]}s" "${cell_text}"
            done
            echo ""
        done

        # Handle last group
        if [ "${is_group}" -eq 1 ]; then
             printf "%-${test_col_width}s" "${last_base} (avg)"
             for backend in "${backends[@]}"; do
                local sum="${print_group_sums[${backend}]:-0}"
                local count="${print_group_counts[${backend}]:-0}"
                local cell_text="-"
                if [ "${count}" -gt 0 ]; then
                    local avg
                    avg=$(awk "BEGIN {printf \"%.2f\", ${sum} / ${count}}")
                    cell_text="${avg}s"
                    backend_totals[${backend}]=$(awk "BEGIN {printf \"%.2f\", ${backend_totals[${backend}]} + ${avg}}")
                fi
                printf " | %${backend_col_widths[${backend}]}s" "${cell_text}"
             done
             echo ""
        fi

        # Total row
        printf "%-${test_col_width}s" "${total_label}"
        for backend in "${backends[@]}"; do
            printf " | %${backend_col_widths[${backend}]}s" "${backend_totals[${backend}]}s"
        done
        echo ""
    } | tee ${GITHUB_STEP_SUMMARY:+"${GITHUB_STEP_SUMMARY}"}  # Output to GitHub summary + stdout if in GitHub Actions, stdout alone otherwise
}

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
  while [ "${iterCount}" -le "${LXD_REPEAT_TESTS}" ]; do
    run_test "${name}" "${iterCount}"
    iterCount=$((iterCount + 1))
  done
}

# Run a single test
run_test() {
  local test_name="${1}"
  local run_count="${2:-1}"
  TEST_CURRENT="${test_name}"

  if [ "${LXD_REPEAT_TESTS}" -gt 1 ]; then
    TEST_CURRENT="${TEST_CURRENT} (${run_count}/${LXD_REPEAT_TESTS})"
  fi

  TEST_CURRENT_DESCRIPTION="${TEST_CURRENT} on ${LXD_BACKEND}"

  # Clear unmet requirement message between tests
  TEST_UNMET_REQUIREMENT=""

  echo "==> TEST BEGIN: ${TEST_CURRENT_DESCRIPTION}"
  local DURATION=""
  local cwd="${PWD}"
  local skip=false

  # Skip test if requested.
  if [ -n "${LXD_SKIP_TESTS:-}" ]; then
    for testName in ${LXD_SKIP_TESTS}; do
      if [ "${testName}" = "${test_name}" ]; then
          echo "==> SKIP: ${TEST_CURRENT} as specified in LXD_SKIP_TESTS"
          skip=true
          break
      fi
    done
  fi

  if [ "${skip}" = false ]; then

    if [[ "${test_name}" =~ ^snap_.*$ ]]; then
      [ -e "/snap/lxd/current" ] || spawn_lxd_snap

      # For snap based tests, the lxc and lxc_remote functions MUST not be used
      unset -f lxc lxc_remote
    elif [ -e "/snap/lxd/current" ]; then
      kill_lxd_snap
    fi

    # If there is '_vm' in the test name, then VM tests are expected to be run.
    # If LXD_VM_TESTS=1, then VM tests can be run.
    if [[ "${test_name}" =~ ^.*_vm.*$ ]] && [ "${LXD_VM_TESTS}" = "0" ]; then
      TEST_UNMET_REQUIREMENT="VM test currently disabled due to LXD_VM_TESTS=0"
    else
      # Check for any core dump before running the test
      if ! check_coredumps; then
        false
      fi

      local START_TIME END_TIME

      START_TIME="$(date +%s.%2N)"
      readonly START_TIME

      # Run test.
      test_"${test_name}"

      END_TIME="$(date +%s.%2N)"
      DURATION=$(awk "BEGIN {printf \"%.2f\", ${END_TIME} - ${START_TIME}}")

      # Check for any core dump after running the test
      if ! check_coredumps; then
        false
      fi
    fi

    # Check whether test was skipped due to unmet requirements, and if so check if the test is required and fail.
    if [ -n "${TEST_UNMET_REQUIREMENT}" ]; then
      DURATION=""
      if [ -n "${LXD_REQUIRED_TESTS:-}" ]; then
        for testName in ${LXD_REQUIRED_TESTS}; do
          if [ "${testName}" = "${test_name}" ]; then
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

  # output duration in blue
  echo -e "==> TEST DONE: ${TEST_CURRENT_DESCRIPTION} (\033[0;34m${DURATION:-"-1"}s\033[0m)"

  durations["${TEST_CURRENT},${LXD_BACKEND}"]="${DURATION}"
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "${TEST_CURRENT},${LXD_BACKEND}=${DURATION}" >> "${MAIN_DIR}/.durations.${LXD_BACKEND}"
  fi
  cd "${cwd}"
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

    # Export initial LXD dir for tests that need to refer to the standalone LXD instance
    export LXD_INITIAL_DIR="${LXD_DIR}"
}

# Spawn an interactive test shell when invoked as `./main.sh test-shell`.
# This is useful for quick interactions with LXD and its test suite.
if [ "${1:-"all"}" = "test-shell" ]; then
  spawn_initial_lxd
  bash --rcfile test-shell.bashrc || true
  TEST_CURRENT="test-shell"
  TEST_CURRENT_DESCRIPTION="test-shell"
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
for LXD_BACKEND in ${active_backends}; do
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

# If in CI, wait for the final step before printing the summary table
# Build a markdown table with the duration of each test
if [ -z "${GITHUB_ACTIONS:-}" ] || is_matrix_final_step; then
    generate_duration_table
fi
