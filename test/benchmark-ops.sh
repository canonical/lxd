#!/bin/bash
# Benchmark common LXD integration test operations across storage backends,
# tmpfs vs disk, standalone vs 3-node cluster, and snap-test workloads.
#
# Usage:
#   # system mode (evaluate `make env` first if using local dqlite/liblxc deps):
#   sudo --preserve-env=PATH,GOPATH,LXD_CEPH_CLUSTER,LXD_TEST_IMAGE,LXD_VM_TESTS,SMALL_ROOT_DISK,TMPDIR \
#     env "LD_LIBRARY_PATH=${LD_LIBRARY_PATH:-}" ./test/benchmark-ops.sh --mode system [options]
#
#   # snap mode (uses the installed LXD snap):
#   sudo ./test/benchmark-ops.sh --mode snap [options]
#
# Options:
#   --mode <system|snap|all>      Benchmark mode (default: system)
#   --backends "<list>"           Backends for system mode
#                                 (default: auto-detect from btrfs ceph dir lvm lvm-nonthin zfs)
#   --snap-backends "<list>"      Backends for snap mode
#                                 (default: btrfs ceph dir lvm lvm-thin zfs)
#   --iterations <N>              Repetitions for fast ops (default: 3)
#   --slow-iterations <N>         Repetitions for slow ops like VMs/cluster (default: 2)
#   --tmpfs                       Use tmpfs for TEST_DIR in system mode (default)
#   --no-tmpfs                    Use disk for TEST_DIR in system mode
#   --compare-tmpfs               Run storage-dependent ops on both tmpfs and disk
#   --cluster                     Run standalone vs 3-node cluster comparison (default)
#   --no-cluster                  Skip standalone vs 3-node cluster comparison
#   --skip-storage                Skip per-backend storage-dependent benchmarks in system mode
#   --snap-ct-image <ref>         Container image for snap mode (default: auto-detect cached or ubuntu-minimal-daily:24.04)
#   --snap-vm-image <ref>         VM image for snap mode (default: auto-detect cached or ubuntu-minimal-daily:24.04)
#   --nested                      Include nested snapd/LXD install benchmark in snap mode
#   --output <file>               Write raw TSV measurements to file
#   -h, --help                    Show this help message

set -eu
set -o pipefail
shopt -s inherit_errexit 2>/dev/null || { echo "Bash 4.4+ is required." >&2; exit 1; }

export LC_ALL="C"
export TZ="UTC"
export GOTOOLCHAIN="local"
export NO_PROXY="${NO_PROXY:-127.0.0.1}"

SCRIPT_DIR="$(dirname "$(realpath "${BASH_SOURCE[0]}")")"

MODE="system"
REQ_BACKENDS=""
SNAP_BACKENDS="btrfs ceph dir lvm lvm-thin zfs"
ITERATIONS=3
SLOW_ITERATIONS=2
USE_TMPFS=1
COMPARE_TMPFS=0
RUN_CLUSTER=1
RUN_STORAGE=1
SNAP_CT_IMAGE=""
SNAP_VM_IMAGE=""
RUN_NESTED=0
OUTPUT_TSV=""

usage() {
    sed -n '2,/^$/{ /^$/d; s/^# \{0,1\}//; p; }' "${BASH_SOURCE[0]}"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --mode)
            MODE="${2}"
            shift 2
            ;;
        --backends)
            REQ_BACKENDS="${2}"
            shift 2
            ;;
        --snap-backends)
            SNAP_BACKENDS="${2}"
            shift 2
            ;;
        --iterations)
            ITERATIONS="${2}"
            shift 2
            ;;
        --slow-iterations)
            SLOW_ITERATIONS="${2}"
            shift 2
            ;;
        --tmpfs)
            USE_TMPFS=1
            shift
            ;;
        --no-tmpfs)
            USE_TMPFS=0
            shift
            ;;
        --compare-tmpfs)
            COMPARE_TMPFS=1
            shift
            ;;
        --cluster)
            RUN_CLUSTER=1
            shift
            ;;
        --no-cluster)
            RUN_CLUSTER=0
            shift
            ;;
        --skip-storage)
            RUN_STORAGE=0
            shift
            ;;
        --snap-ct-image)
            SNAP_CT_IMAGE="${2}"
            shift 2
            ;;
        --snap-vm-image)
            SNAP_VM_IMAGE="${2}"
            shift 2
            ;;
        --nested)
            RUN_NESTED=1
            shift
            ;;
        --output)
            OUTPUT_TSV="${2}"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

case "${MODE}" in
    system|snap|all)
        ;;
    *)
        echo "Unsupported mode: ${MODE} (expected: system, snap, or all)" >&2
        usage >&2
        exit 1
        ;;
esac

if [ "${EUID}" -ne 0 ]; then
    # shellcheck disable=SC2016
    echo 'benchmark-ops.sh must be run as root (e.g. sudo --preserve-env=PATH,GOPATH env "LD_LIBRARY_PATH=${LD_LIBRARY_PATH:-}" ./test/benchmark-ops.sh).' >&2
    exit 1
fi

# Prevent lxc commands from blocking on open stdin pipes.
exec < /dev/null

# Ensure Go bin is on PATH when running system mode.
if [ -z "${GOPATH:-}" ] && command -v go >/dev/null 2>&1; then
    GOPATH="$(go env GOPATH)"
fi
if [ -n "${GOPATH:-}" ] && [[ "${PATH}" != *"${GOPATH}/bin"* ]]; then
    PATH="${GOPATH}/bin:${PATH}"
fi
if [ -d "/snap/microovn/current/commands" ] && [[ "${PATH}" != *"/snap/microovn/current/commands"* ]]; then
    PATH="${PATH}:/snap/microovn/current/commands"
fi
export PATH

# Initialize TSV output file if requested.
if [ -n "${OUTPUT_TSV}" ]; then
    printf 'section\toperation\tcolumn\tmedian_ms\tsamples_ms\n' > "${OUTPUT_TSV}"
fi

# Timing helpers (uses Bash 5+ EPOCHREALTIME to avoid forks and date %s/%N drift).
now_ms() {
    if [ -n "${EPOCHREALTIME:-}" ]; then
        local t="${EPOCHREALTIME}"
        local sec="${t%.*}"
        local us="${t#*.}"
        echo $(( sec * 1000 + 10#${us:0:3} ))
    else
        local up sec cs
        read -r up _ < /proc/uptime
        sec="${up%.*}"
        cs="${up#*.}"
        echo $(( sec * 1000 + 10#${cs} * 10 ))
    fi
}

# Compute median from whitespace-separated integer millisecond samples.
median_ms() {
    local sorted count mid
    mapfile -t sorted < <(printf '%s\n' "$@" | sort -n)
    count="${#sorted[@]}"
    if [ "${count}" -eq 0 ]; then
        echo ""
        return
    fi
    mid=$((count / 2))
    if [ $((count % 2)) -eq 1 ]; then
        echo "${sorted[${mid}]}"
    else
        echo $(( (sorted[mid - 1] + sorted[mid]) / 2 ))
    fi
}

# Store results in associative arrays for table printing.
# Key format: "section|op|col" -> median_ms
declare -A BENCH_RESULTS
declare -A BENCH_SEEN_OPS
declare -A BENCH_SEEN_COLS
declare -a BENCH_SECTIONS=()

record_result() {
    local section="${1}"
    local op="${2}"
    local col="${3}"
    local med="${4}"
    local samples="${5:-${med}}"

    BENCH_RESULTS["${section}|${op}|${col}"]="${med}"

    if [ -z "${BENCH_SEEN_OPS["${section}|${op}"]:-}" ]; then
        BENCH_SEEN_OPS["${section}|${op}"]=1
        local cur="${BENCH_SEEN_OPS["${section}|__list"]:-}"
        if [ -n "${cur}" ]; then
            BENCH_SEEN_OPS["${section}|__list"]="${cur}"$'\n'"${op}"
        else
            BENCH_SEEN_OPS["${section}|__list"]="${op}"
            BENCH_SECTIONS+=("${section}")
        fi
    fi

    if [ -z "${BENCH_SEEN_COLS["${section}|${col}"]:-}" ]; then
        BENCH_SEEN_COLS["${section}|${col}"]=1
        local ccur="${BENCH_SEEN_COLS["${section}|__list"]:-}"
        if [ -n "${ccur}" ]; then
            BENCH_SEEN_COLS["${section}|__list"]="${ccur}"$'\n'"${col}"
        else
            BENCH_SEEN_COLS["${section}|__list"]="${col}"
        fi
    fi

    if [ -n "${OUTPUT_TSV}" ]; then
        printf '%s\t%s\t%s\t%s\t%s\n' "${section}" "${op}" "${col}" "${med}" "${samples}" >> "${OUTPUT_TSV}"
    fi

    echo "  [bench] ${section} | ${col} | ${op}: ${med} ms (samples: ${samples})" >&2
}

# Format milliseconds cleanly for markdown tables.
fmt_ms() {
    local ms="${1:-}"
    if [ -z "${ms}" ] || [ "${ms}" = "-" ]; then
        echo "-"
    elif [ "${ms}" -ge 10000 ]; then
        awk -v m="${ms}" 'BEGIN { printf "%.1fs", m / 1000.0 }'
    elif [ "${ms}" -ge 1000 ]; then
        awk -v m="${ms}" 'BEGIN { printf "%.2fs", m / 1000.0 }'
    else
        echo "${ms}ms"
    fi
}

print_section_table() {
    local section="${1}"
    local -a ops=()
    local -a cols=()
    local op col val

    mapfile -t ops <<< "${BENCH_SEEN_OPS["${section}|__list"]}"
    mapfile -t cols <<< "${BENCH_SEEN_COLS["${section}|__list"]}"

    echo ""
    echo "### ${section}"
    echo ""

    printf '| Operation'
    for col in "${cols[@]}"; do
        printf ' | %s' "${col}"
    done
    printf ' |\n'

    printf '| :---'
    for col in "${cols[@]}"; do
        printf ' | ---:'
    done
    printf ' |\n'

    for op in "${ops[@]}"; do
        # shellcheck disable=SC2016
        printf '| `%s`' "${op}"
        for col in "${cols[@]}"; do
            val="${BENCH_RESULTS["${section}|${op}|${col}"]:--}"
            printf ' | %s' "$(fmt_ms "${val}")"
        done
        printf ' |\n'
    done
}

# Time a shell command (passed as args) N times, recording median ms.
# Usage: bench_cmd <section> <op_label> <col> <iters> -- <cmd...>
bench_cmd() {
    local section="${1}"
    local op="${2}"
    local col="${3}"
    local iters="${4}"
    shift 4
    if [ "${1:-}" = "--" ]; then
        shift
    fi

    local -a samples=()
    local i t0 t1 dt
    for i in $(seq 1 "${iters}"); do
        t0="$(now_ms)"
        "$@" >/dev/null 2>&1
        t1="$(now_ms)"
        dt=$((t1 - t0))
        samples+=("${dt}")
    done

    local med
    med="$(median_ms "${samples[@]}")"
    record_result "${section}" "${op}" "${col}" "${med}" "${samples[*]}"
}

# ==============================================================================
# SYSTEM-TESTS MODE (reuses test/main.sh harness helpers & ephemeral daemons)
# ==============================================================================

TEST_DIR=""
LXD_DIR=""
LXD_CONF=""
# shellcheck disable=SC2034
LXD_NETNS=""
export CLIENT_DEBUG="" SERVER_DEBUG="" SHELL_TRACING=""
export DEFAULT_VOLUME_SIZE="24MiB"
export DEFAULT_POOL_SIZE="7GiB"
export SMALL_ROOT_DISK="${SMALL_ROOT_DISK:-root,size=${DEFAULT_VOLUME_SIZE}}"
export SMALLEST_VM_ROOT_DISK="3584MiB"
export SMALL_VM_ROOT_DISK="${SMALL_VM_ROOT_DISK:-root,size=${SMALLEST_VM_ROOT_DISK}}"
export LXD_CEPH_CLUSTER="${LXD_CEPH_CLUSTER:-ceph}"
export LXD_CEPH_CEPHFS="${LXD_CEPH_CEPHFS:-cephfs}"
export LXD_VM_TESTS="${LXD_VM_TESTS:-1}"
export LXD_ONE_DIR="" LXD_TWO_DIR="" LXD_THREE_DIR=""
export LXD_FOUR_DIR="" LXD_FIVE_DIR="" LXD_SIX_DIR=""
export LXD_SEVEN_DIR="" LXD_EIGHT_DIR="" LXD_NINE_DIR=""
export ns1="" ns2="" ns3="" ns4="" ns5="" ns6="" ns7="" ns8="" ns9=""
export prefix="lxd$$"
export bridge="${prefix}"
export MAIN_DIR="${SCRIPT_DIR}"

cleanup_system_test_dir() {
    set +e
    if [ -n "${TEST_DIR:-}" ] && [ -d "${TEST_DIR}" ]; then
        mountpoint -q "${TEST_DIR}/dev" && umount -l "${TEST_DIR}/dev" >/dev/null 2>&1
        cleanup_lxds "${TEST_DIR}" >/dev/null 2>&1 || true
        teardown_clustering_netns >/dev/null 2>&1 || true
        teardown_clustering_bridge >/dev/null 2>&1 || true
        mountpoint -q "${TEST_DIR}" && umount -l "${TEST_DIR}" >/dev/null 2>&1
        rm -rf "${TEST_DIR}"
    fi
    TEST_DIR=""
    LXD_DIR=""
    LXD_CONF=""
    set -e
}

# Track snap benchmark cleanup resources.
declare -a SNAP_CLEANUP_INSTANCES=()
declare -a SNAP_CLEANUP_POOLS=()
declare -a SNAP_CLEANUP_IMAGES=()

cleanup_all() {
    local rc=$?
    trap - EXIT HUP INT TERM
    set +e

    if [ "${MODE}" = "system" ] || [ "${MODE}" = "all" ]; then
        cleanup_system_test_dir
    fi

    local item
    for item in "${SNAP_CLEANUP_INSTANCES[@]}"; do
        lxc delete -f "${item}" >/dev/null 2>&1 || true
    done
    for item in "${SNAP_CLEANUP_IMAGES[@]}"; do
        lxc image delete "${item}" >/dev/null 2>&1 || true
    done
    for item in "${SNAP_CLEANUP_POOLS[@]}"; do
        lxc storage delete "${item}" >/dev/null 2>&1 || true
    done

    exit "${rc}"
}
trap cleanup_all EXIT HUP INT TERM

load_system_harness() {
    cd "${SCRIPT_DIR}"

    local file
    for file in "${SCRIPT_DIR}"/includes/*.sh; do
        # Skip snap-helpers.sh in system mode to avoid shadowing functions.
        if [ "$(basename "${file}")" = "snap-helpers.sh" ]; then
            continue
        fi
        # shellcheck disable=SC1090
        . "${file}"
    done

    import_storage_backends

    _LXC="$(unset -f lxc; command -v lxc)"
    export _LXC

    # Ensure busybox testimage cache is available.
    if [ -z "${LXD_TEST_IMAGE:-}" ]; then
        if [ -f "${SCRIPT_DIR}/busybox.tar.cache" ]; then
            export LXD_TEST_IMAGE="${SCRIPT_DIR}/busybox.tar.cache"
        else
            deps/import-busybox --save-image
            mv busybox.tar busybox.tar.cache
            export LXD_TEST_IMAGE="${SCRIPT_DIR}/busybox.tar.cache"
        fi
    fi
}

setup_system_test_dir() {
    local use_tmpfs="${1}"

    TEST_DIR="$(mktemp -d -p "${TMPDIR:-/tmp}" lxd-bench-XXXXXXXXX)"
    export TEST_DIR
    chmod +x "${TEST_DIR}"

    if [ "${use_tmpfs}" -eq 1 ]; then
        mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751 -o size=100%
    fi

    LXD_CONF="$(mktemp -d -p "${TEST_DIR}" conf-XXX)"
    export LXD_CONF
    export LXD_FUIDMAP_DIR="${TEST_DIR}/fuidmap"
    mkdir -p "${LXD_FUIDMAP_DIR}"
    mkdir -p "${TEST_DIR}/dev"
    mount -t tmpfs tmpfs "${TEST_DIR}/dev" -o mode=0755 -o size=16M
    export LXD_DEVMONITOR_DIR="${TEST_DIR}/dev"
}

# Create a storage pool matching the requested backend (including lvm-nonthin).
configure_bench_pool() {
    local backend="${1}"
    local pool="${2}"

    case "${backend}" in
        dir)
            lxc storage create "${pool}" dir
            ;;
        btrfs)
            lxc storage create "${pool}" btrfs size="${DEFAULT_POOL_SIZE}"
            ;;
        lvm)
            lxc storage create "${pool}" lvm volume.size="${DEFAULT_VOLUME_SIZE}" size="${DEFAULT_POOL_SIZE}"
            ;;
        lvm-nonthin)
            lxc storage create "${pool}" lvm lvm.use_thinpool=false volume.size="${DEFAULT_VOLUME_SIZE}" size="${DEFAULT_POOL_SIZE}"
            ;;
        zfs)
            lxc storage create "${pool}" zfs size="${DEFAULT_POOL_SIZE}"
            ;;
        ceph)
            lxc storage create "${pool}" ceph volume.size="${DEFAULT_VOLUME_SIZE}" ceph.osd.pg_num=8
            ;;
        *)
            echo "Unsupported backend: ${backend}" >&2
            return 1
            ;;
    esac
    lxc profile device add default root disk path="/" pool="${pool}"
}

bench_storage_backend() {
    local backend="${1}"
    local use_tmpfs="${2}"
    local section="${3}"
    local col="${4}"

    local real_backend="${backend}"
    if [ "${backend}" = "lvm-nonthin" ]; then
        real_backend="lvm"
    fi

    echo "==> Benchmarking storage-dependent ops: backend=${backend} tmpfs=${use_tmpfs}" >&2
    setup_system_test_dir "${use_tmpfs}"

    export LXD_BACKEND="${real_backend}"
    LXD_DIR="$(mktemp -d -p "${TEST_DIR}" lxd-XXX)"
    export LXD_DIR
    local pool
    pool="lxdtest-$(basename "${LXD_DIR}")"

    # 1. Measure spawn_lxd (without pool) + pool creation separately and combined.
    local t0 t1 dt_spawn dt_pool
    t0="$(now_ms)"
    spawn_lxd "${LXD_DIR}" false >/dev/null 2>&1
    t1="$(now_ms)"
    dt_spawn=$((t1 - t0))

    t0="$(now_ms)"
    configure_bench_pool "${backend}" "${pool}" >/dev/null 2>&1
    t1="$(now_ms)"
    dt_pool=$((t1 - t0))

    record_result "${section}" "spawn_lxd (daemon only)" "${col}" "${dt_spawn}"
    record_result "${section}" "lxc storage create (default pool)" "${col}" "${dt_pool}"
    record_result "${section}" "spawn_lxd (daemon + pool)" "${col}" "$((dt_spawn + dt_pool))"

    # 2. Secondary pool create + delete.
    local -a s_pool_create=() s_pool_delete=()
    local i sec_pool
    for i in $(seq 1 "${SLOW_ITERATIONS}"); do
        sec_pool="bench-p2-${i}-$$"
        t0="$(now_ms)"
        case "${backend}" in
            dir) lxc storage create "${sec_pool}" dir >/dev/null 2>&1 ;;
            btrfs) lxc storage create "${sec_pool}" btrfs size=512MiB >/dev/null 2>&1 ;;
            lvm) lxc storage create "${sec_pool}" lvm volume.size="${DEFAULT_VOLUME_SIZE}" size=512MiB >/dev/null 2>&1 ;;
            lvm-nonthin) lxc storage create "${sec_pool}" lvm lvm.use_thinpool=false volume.size="${DEFAULT_VOLUME_SIZE}" size=512MiB >/dev/null 2>&1 ;;
            zfs) lxc storage create "${sec_pool}" zfs size=512MiB >/dev/null 2>&1 ;;
            ceph) lxc storage create "${sec_pool}" ceph volume.size="${DEFAULT_VOLUME_SIZE}" ceph.osd.pg_num=8 >/dev/null 2>&1 ;;
            *) ;;
        esac
        t1="$(now_ms)"
        s_pool_create+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc storage delete "${sec_pool}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_pool_delete+=($((t1 - t0)))
    done
    record_result "${section}" "lxc storage create (secondary)" "${col}" "$(median_ms "${s_pool_create[@]}")" "${s_pool_create[*]}"
    record_result "${section}" "lxc storage delete (secondary)" "${col}" "$(median_ms "${s_pool_delete[@]}")" "${s_pool_delete[*]}"

    # 3. ensure_import_testimage (from cached busybox tarball).
    local -a s_img=()
    for i in $(seq 1 "${ITERATIONS}"); do
        t0="$(now_ms)"
        ensure_import_testimage >/dev/null 2>&1
        t1="$(now_ms)"
        s_img+=($((t1 - t0)))
        if [ "${i}" -lt "${ITERATIONS}" ]; then
            lxc image delete testimage >/dev/null 2>&1
        fi
    done
    record_result "${section}" "ensure_import_testimage (cached)" "${col}" "$(median_ms "${s_img[@]}")" "${s_img[*]}"

    # 4. Container init variants (--empty vs testimage, default vs SMALL_ROOT_DISK).
    local -a s_init_empty=() s_init_empty_small=() s_init_img=() s_init_img_small=()
    local -a s_del_stopped=()
    for i in $(seq 1 "${ITERATIONS}"); do
        t0="$(now_ms)"
        lxc init --empty "be-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_init_empty+=($((t1 - t0)))
        t0="$(now_ms)"
        lxc delete "be-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_del_stopped+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc init --empty "bes-${i}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_init_empty_small+=($((t1 - t0)))
        lxc delete "bes-${i}" >/dev/null 2>&1

        t0="$(now_ms)"
        lxc init testimage "bi-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_init_img+=($((t1 - t0)))
        lxc delete "bi-${i}" >/dev/null 2>&1

        t0="$(now_ms)"
        lxc init testimage "bis-${i}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_init_img_small+=($((t1 - t0)))
        lxc delete "bis-${i}" >/dev/null 2>&1
    done
    record_result "${section}" "lxc init --empty" "${col}" "$(median_ms "${s_init_empty[@]}")" "${s_init_empty[*]}"
    record_result "${section}" "lxc init --empty -d SMALL_ROOT_DISK" "${col}" "$(median_ms "${s_init_empty_small[@]}")" "${s_init_empty_small[*]}"
    record_result "${section}" "lxc init testimage" "${col}" "$(median_ms "${s_init_img[@]}")" "${s_init_img[*]}"
    record_result "${section}" "lxc init testimage -d SMALL_ROOT_DISK" "${col}" "$(median_ms "${s_init_img_small[@]}")" "${s_init_img_small[*]}"
    record_result "${section}" "lxc delete (stopped)" "${col}" "$(median_ms "${s_del_stopped[@]}")" "${s_del_stopped[*]}"

    # 5. Container launch, start, exec, stop -f, stop (graceful), restart -f, delete -f.
    local -a s_launch=() s_launch_small=() s_start=() s_exec=() s_stop_f=() s_stop_g=() s_restart_f=() s_del_f=()
    for i in $(seq 1 "${ITERATIONS}"); do
        t0="$(now_ms)"
        lxc launch testimage "bl-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_launch+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc exec "bl-${i}" -- true >/dev/null 2>&1
        t1="$(now_ms)"
        s_exec+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc restart -f "bl-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_restart_f+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc stop -f "bl-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_stop_f+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc start "bl-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_start+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc stop "bl-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_stop_g+=($((t1 - t0)))

        lxc start "bl-${i}" >/dev/null 2>&1
        t0="$(now_ms)"
        lxc delete -f "bl-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_del_f+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc launch testimage "bls-${i}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_launch_small+=($((t1 - t0)))
        lxc delete -f "bls-${i}" >/dev/null 2>&1
    done
    record_result "${section}" "lxc launch testimage" "${col}" "$(median_ms "${s_launch[@]}")" "${s_launch[*]}"
    record_result "${section}" "lxc launch testimage -d SMALL_ROOT_DISK" "${col}" "$(median_ms "${s_launch_small[@]}")" "${s_launch_small[*]}"
    record_result "${section}" "lxc start (testimage)" "${col}" "$(median_ms "${s_start[@]}")" "${s_start[*]}"
    record_result "${section}" "lxc exec -- true" "${col}" "$(median_ms "${s_exec[@]}")" "${s_exec[*]}"
    record_result "${section}" "lxc restart -f" "${col}" "$(median_ms "${s_restart_f[@]}")" "${s_restart_f[*]}"
    record_result "${section}" "lxc stop -f" "${col}" "$(median_ms "${s_stop_f[@]}")" "${s_stop_f[*]}"
    record_result "${section}" "lxc stop (graceful)" "${col}" "$(median_ms "${s_stop_g[@]}")" "${s_stop_g[*]}"
    record_result "${section}" "lxc delete -f (running)" "${col}" "$(median_ms "${s_del_f[@]}")" "${s_del_f[*]}"

    # 6. VM empty init & launch (requires qemu-img; default VM disk is 10GiB, so skip default size on non-thin LVM with 7GiB pool).
    if [ "${LXD_VM_TESTS}" = "1" ] && command -v qemu-img >/dev/null 2>&1; then
        local -a s_vm_init_small=() s_vm_init_def=() s_vm_launch_small=()
        for i in $(seq 1 "${ITERATIONS}"); do
            t0="$(now_ms)"
            lxc init --empty --vm "bvs-${i}" -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_vm_init_small+=($((t1 - t0)))
            lxc delete "bvs-${i}" >/dev/null 2>&1
        done
        record_result "${section}" "lxc init --empty --vm -d SMALL_ROOT_DISK" "${col}" "$(median_ms "${s_vm_init_small[@]}")" "${s_vm_init_small[*]}"

        if [ "${backend}" != "lvm-nonthin" ]; then
            for i in $(seq 1 "${SLOW_ITERATIONS}"); do
                t0="$(now_ms)"
                if lxc init --empty --vm "bvd-${i}" -c limits.memory=128MiB >/dev/null 2>&1; then
                    t1="$(now_ms)"
                    s_vm_init_def+=($((t1 - t0)))
                    lxc delete "bvd-${i}" >/dev/null 2>&1
                fi
            done
            if [ "${#s_vm_init_def[@]}" -gt 0 ]; then
                record_result "${section}" "lxc init --empty --vm (default size)" "${col}" "$(median_ms "${s_vm_init_def[@]}")" "${s_vm_init_def[*]}"
            fi
        fi

        if command -v "qemu-system-$(uname -m)" >/dev/null 2>&1; then
            for i in $(seq 1 "${SLOW_ITERATIONS}"); do
                t0="$(now_ms)"
                lxc launch --empty --vm "bvl-${i}" -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_launch_small+=($((t1 - t0)))
                lxc delete -f "bvl-${i}" >/dev/null 2>&1
            done
            record_result "${section}" "lxc launch --empty --vm -d SMALL_ROOT_DISK" "${col}" "$(median_ms "${s_vm_launch_small[@]}")" "${s_vm_launch_small[*]}"
        fi
    fi

    # 7. Snapshots, copy, refresh, export, import, publish.
    lxc init testimage bsrc -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1

    local -a s_snap=() s_snap_restore=() s_copy=() s_copy_ref=() s_export=() s_export_opt=() s_import=() s_publish=()
    for i in $(seq 1 "${ITERATIONS}"); do
        t0="$(now_ms)"
        lxc snapshot bsrc "s${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_snap+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc restore bsrc "s${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_snap_restore+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc copy bsrc "bdst-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_copy+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc copy bsrc "bdst-${i}" --refresh >/dev/null 2>&1
        t1="$(now_ms)"
        s_copy_ref+=($((t1 - t0)))

        lxc delete "bdst-${i}" >/dev/null 2>&1
        lxc delete "bsrc/s${i}" >/dev/null 2>&1
    done
    record_result "${section}" "lxc snapshot" "${col}" "$(median_ms "${s_snap[@]}")" "${s_snap[*]}"
    record_result "${section}" "lxc restore" "${col}" "$(median_ms "${s_snap_restore[@]}")" "${s_snap_restore[*]}"
    record_result "${section}" "lxc copy (same pool)" "${col}" "$(median_ms "${s_copy[@]}")" "${s_copy[*]}"
    record_result "${section}" "lxc copy --refresh" "${col}" "$(median_ms "${s_copy_ref[@]}")" "${s_copy_ref[*]}"

    for i in $(seq 1 "${SLOW_ITERATIONS}"); do
        local exp_file="${TEST_DIR}/bsrc-${i}.tar.gz"
        t0="$(now_ms)"
        lxc export bsrc "${exp_file}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_export+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc import "${exp_file}" "bimp-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_import+=($((t1 - t0)))
        lxc delete "bimp-${i}" >/dev/null 2>&1
        rm -f "${exp_file}"

        if [ "${backend}" = "btrfs" ] || [ "${backend}" = "zfs" ]; then
            local opt_file="${TEST_DIR}/bsrc-opt-${i}.tar.gz"
            t0="$(now_ms)"
            lxc export bsrc "${opt_file}" --optimized-storage >/dev/null 2>&1
            t1="$(now_ms)"
            s_export_opt+=($((t1 - t0)))
            rm -f "${opt_file}"
        fi

        t0="$(now_ms)"
        lxc publish bsrc --alias "bpub-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_publish+=($((t1 - t0)))
        lxc image delete "bpub-${i}" >/dev/null 2>&1
    done
    record_result "${section}" "lxc export" "${col}" "$(median_ms "${s_export[@]}")" "${s_export[*]}"
    if [ "${#s_export_opt[@]}" -gt 0 ]; then
        record_result "${section}" "lxc export --optimized-storage" "${col}" "$(median_ms "${s_export_opt[@]}")" "${s_export_opt[*]}"
    fi
    record_result "${section}" "lxc import" "${col}" "$(median_ms "${s_import[@]}")" "${s_import[*]}"
    record_result "${section}" "lxc publish" "${col}" "$(median_ms "${s_publish[@]}")" "${s_publish[*]}"

    lxc delete bsrc >/dev/null 2>&1

    # 8. Custom storage volumes (filesystem and block).
    local -a s_vol_fs=() s_vol_blk=()
    for i in $(seq 1 "${ITERATIONS}"); do
        t0="$(now_ms)"
        lxc storage volume create "${pool}" "v-fs-${i}" size=32MiB >/dev/null 2>&1
        t1="$(now_ms)"
        s_vol_fs+=($((t1 - t0)))
        lxc storage volume delete "${pool}" "v-fs-${i}" >/dev/null 2>&1

        t0="$(now_ms)"
        lxc storage volume create "${pool}" "v-blk-${i}" --type=block size=32MiB >/dev/null 2>&1
        t1="$(now_ms)"
        s_vol_blk+=($((t1 - t0)))
        lxc storage volume delete "${pool}" "v-blk-${i}" >/dev/null 2>&1
    done
    record_result "${section}" "lxc storage volume create (fs, 32MiB)" "${col}" "$(median_ms "${s_vol_fs[@]}")" "${s_vol_fs[*]}"
    record_result "${section}" "lxc storage volume create (block, 32MiB)" "${col}" "$(median_ms "${s_vol_blk[@]}")" "${s_vol_blk[*]}"

    # 9. Clean up testimage and default pool before shutdown so loop/VG/zpool resources detach cleanly.
    lxc image delete testimage >/dev/null 2>&1 || true
    lxc profile device remove default root >/dev/null 2>&1 || true
    lxc storage delete "${pool}" >/dev/null 2>&1 || true

    # Measure shutdown_lxd.
    t0="$(now_ms)"
    shutdown_lxd "${LXD_DIR}" >/dev/null 2>&1
    t1="$(now_ms)"
    record_result "${section}" "shutdown_lxd" "${col}" "$((t1 - t0))"

    cleanup_system_test_dir
}

# Benchmark storage-agnostic operations on standalone vs 3-node cluster.
bench_agnostic_ops_suite() {
    local section="${1}"
    local col="${2}"
    local target_dir="${3}"
    local is_cluster="${4}"

    local orig_dir="${LXD_DIR}"
    export LXD_DIR="${target_dir}"

    # 0 instances baseline
    bench_cmd "${section}" "lxc query /1.0" "${col}" "${ITERATIONS}" -- lxc query /1.0
    bench_cmd "${section}" "lxc list (0 instances)" "${col}" "${ITERATIONS}" -- lxc list
    bench_cmd "${section}" "lxc list -f csv -c n (0 instances)" "${col}" "${ITERATIONS}" -- lxc list -f csv -c n
    bench_cmd "${section}" "lxc query /1.0/instances (0 instances)" "${col}" "${ITERATIONS}" -- lxc query /1.0/instances

    # 1 running instance
    ensure_import_testimage >/dev/null 2>&1
    lxc launch testimage ag1 -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1

    bench_cmd "${section}" "lxc list (1 running)" "${col}" "${ITERATIONS}" -- lxc list
    bench_cmd "${section}" "lxc list -f csv -c n (1 running)" "${col}" "${ITERATIONS}" -- lxc list -f csv -c n
    bench_cmd "${section}" "lxc list -f csv -c ns (1 running)" "${col}" "${ITERATIONS}" -- lxc list -f csv -c ns
    bench_cmd "${section}" "lxc list -f json (1 running)" "${col}" "${ITERATIONS}" -- lxc list -f json
    bench_cmd "${section}" "lxc info <inst> (running)" "${col}" "${ITERATIONS}" -- lxc info ag1
    bench_cmd "${section}" "lxc config show <inst>" "${col}" "${ITERATIONS}" -- lxc config show ag1
    bench_cmd "${section}" "lxc query /1.0/instances (1 running)" "${col}" "${ITERATIONS}" -- lxc query /1.0/instances
    bench_cmd "${section}" "lxc query /1.0/instances?recursion=1 (1 running)" "${col}" "${ITERATIONS}" -- lxc query '/1.0/instances?recursion=1'
    bench_cmd "${section}" "lxc query /1.0/instances?recursion=2 (1 running)" "${col}" "${ITERATIONS}" -- lxc query '/1.0/instances?recursion=2'
    bench_cmd "${section}" "lxc query /1.0/instances/<inst>" "${col}" "${ITERATIONS}" -- lxc query /1.0/instances/ag1
    bench_cmd "${section}" "lxc query /1.0/instances/<inst>/state" "${col}" "${ITERATIONS}" -- lxc query /1.0/instances/ag1/state
    bench_cmd "${section}" "lxc exec -- true" "${col}" "${ITERATIONS}" -- lxc exec ag1 -- true
    bench_cmd "${section}" "lxc config set user.bench=1" "${col}" "${ITERATIONS}" -- lxc config set ag1 user.bench=1

    # Add 9 more running instances (10 total) to expose list/recursion scaling.
    local i
    for i in $(seq 2 10); do
        if [ "${is_cluster}" -eq 1 ]; then
            local node_num=$(( ((i - 1) % 3) + 1 ))
            lxc launch testimage "ag${i}" --target "node${node_num}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
        else
            lxc launch testimage "ag${i}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
        fi
    done

    bench_cmd "${section}" "lxc list (10 running)" "${col}" "${ITERATIONS}" -- lxc list
    bench_cmd "${section}" "lxc list -f csv -c n (10 running)" "${col}" "${ITERATIONS}" -- lxc list -f csv -c n
    bench_cmd "${section}" "lxc list -f csv -c ns (10 running)" "${col}" "${ITERATIONS}" -- lxc list -f csv -c ns
    bench_cmd "${section}" "lxc list -f json (10 running)" "${col}" "${ITERATIONS}" -- lxc list -f json
    bench_cmd "${section}" "lxc query /1.0/instances (10 running)" "${col}" "${ITERATIONS}" -- lxc query /1.0/instances
    bench_cmd "${section}" "lxc query /1.0/instances?recursion=1 (10 running)" "${col}" "${ITERATIONS}" -- lxc query '/1.0/instances?recursion=1'
    bench_cmd "${section}" "lxc query /1.0/instances?recursion=2 (10 running)" "${col}" "${ITERATIONS}" -- lxc query '/1.0/instances?recursion=2'

    # Clean up 10 instances.
    lxc delete -f ag1 ag2 ag3 ag4 ag5 ag6 ag7 ag8 ag9 ag10 >/dev/null 2>&1

    # Metadata CRUD writes (profile, project, managed bridge network).
    local -a s_prof=() s_proj=() s_net=()
    local t0 t1
    for i in $(seq 1 "${ITERATIONS}"); do
        t0="$(now_ms)"
        lxc profile create "bprof-${i}" >/dev/null 2>&1
        lxc profile delete "bprof-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_prof+=($((t1 - t0)))

        t0="$(now_ms)"
        lxc project create "bproj-${i}" >/dev/null 2>&1
        lxc project delete "bproj-${i}" >/dev/null 2>&1
        t1="$(now_ms)"
        s_proj+=($((t1 - t0)))

        if [ "${is_cluster}" -eq 0 ]; then
            t0="$(now_ms)"
            lxc network create "bnet${i}" ipv4.address=none ipv6.address=none >/dev/null 2>&1
            lxc network delete "bnet${i}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_net+=($((t1 - t0)))
        fi
    done
    record_result "${section}" "lxc profile create + delete" "${col}" "$(median_ms "${s_prof[@]}")" "${s_prof[*]}"
    record_result "${section}" "lxc project create + delete" "${col}" "$(median_ms "${s_proj[@]}")" "${s_proj[*]}"
    if [ "${#s_net[@]}" -gt 0 ]; then
        record_result "${section}" "lxc network create + delete (bridge)" "${col}" "$(median_ms "${s_net[@]}")" "${s_net[*]}"
    fi

    # Instance init/launch comparison (including cluster local vs forwarded target).
    local -a s_init_local=() s_launch_local=() s_init_remote=() s_launch_remote=()
    for i in $(seq 1 "${ITERATIONS}"); do
        if [ "${is_cluster}" -eq 1 ]; then
            t0="$(now_ms)"
            lxc init --empty "cl-l-${i}" --target node1 -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_init_local+=($((t1 - t0)))
            lxc delete "cl-l-${i}" >/dev/null 2>&1

            t0="$(now_ms)"
            lxc init --empty "cl-r-${i}" --target node2 -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_init_remote+=($((t1 - t0)))
            lxc delete "cl-r-${i}" >/dev/null 2>&1

            t0="$(now_ms)"
            lxc launch testimage "cl-ll-${i}" --target node1 -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_launch_local+=($((t1 - t0)))
            lxc delete -f "cl-ll-${i}" >/dev/null 2>&1

            t0="$(now_ms)"
            lxc launch testimage "cl-rl-${i}" --target node2 -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_launch_remote+=($((t1 - t0)))
            lxc delete -f "cl-rl-${i}" >/dev/null 2>&1
        else
            t0="$(now_ms)"
            lxc init --empty "st-l-${i}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_init_local+=($((t1 - t0)))
            lxc delete "st-l-${i}" >/dev/null 2>&1

            t0="$(now_ms)"
            lxc launch testimage "st-ll-${i}" -d "${SMALL_ROOT_DISK}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_launch_local+=($((t1 - t0)))
            lxc delete -f "st-ll-${i}" >/dev/null 2>&1
        fi
    done

    record_result "${section}" "lxc init --empty -d SMALL_ROOT_DISK (local)" "${col}" "$(median_ms "${s_init_local[@]}")" "${s_init_local[*]}"
    record_result "${section}" "lxc launch testimage -d SMALL_ROOT_DISK (local)" "${col}" "$(median_ms "${s_launch_local[@]}")" "${s_launch_local[*]}"
    if [ "${is_cluster}" -eq 1 ]; then
        record_result "${section}" "lxc init --empty -d SMALL_ROOT_DISK (forwarded node2)" "${col}" "$(median_ms "${s_init_remote[@]}")" "${s_init_remote[*]}"
        record_result "${section}" "lxc launch testimage -d SMALL_ROOT_DISK (forwarded node2)" "${col}" "$(median_ms "${s_launch_remote[@]}")" "${s_launch_remote[*]}"
    fi

    export LXD_DIR="${orig_dir}"
}

run_cluster_comparison() {
    local section="Storage-agnostic & cluster overhead (dir, tmpfs)"
    echo "==> Benchmarking standalone vs 3-node cluster (dir, tmpfs)" >&2

    # --- Standalone ---
    setup_system_test_dir 1
    export LXD_BACKEND="dir"
    LXD_DIR="$(mktemp -d -p "${TEST_DIR}" standalone-XXX)"
    export LXD_DIR
    spawn_lxd "${LXD_DIR}" true >/dev/null 2>&1
    bench_agnostic_ops_suite "${section}" "standalone" "${LXD_DIR}" 0
    local t0 t1
    t0="$(now_ms)"
    kill_lxd "${LXD_DIR}" >/dev/null 2>&1
    t1="$(now_ms)"
    record_result "${section}" "kill_lxd" "standalone" "$((t1 - t0))"
    cleanup_system_test_dir

    # --- 3-node cluster ---
    setup_system_test_dir 1
    export LXD_BACKEND="dir"
    local cert
    t0="$(now_ms)"
    spawn_lxd_and_bootstrap_cluster "dir" >/dev/null 2>&1
    cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"
    spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" >/dev/null 2>&1
    spawn_lxd_and_join_cluster "${cert}" 3 1 "${LXD_ONE_DIR}" >/dev/null 2>&1
    t1="$(now_ms)"
    record_result "${section}" "3-node cluster bootstrap + 2 joins" "3-node cluster" "$((t1 - t0))"

    bench_agnostic_ops_suite "${section}" "3-node cluster" "${LXD_ONE_DIR}" 1

    # Also measure cluster-specific read commands.
    LXD_DIR="${LXD_ONE_DIR}" bench_cmd "${section}" "lxc cluster list" "3-node cluster" "${ITERATIONS}" -- lxc cluster list
    LXD_DIR="${LXD_ONE_DIR}" bench_cmd "${section}" "lxc cluster list -f csv -c n" "3-node cluster" "${ITERATIONS}" -- lxc cluster list -f csv -c n

    cleanup_system_test_dir
}

run_system_mode() {
    load_system_harness

    local backends_to_run="${REQ_BACKENDS}"
    if [ -z "${backends_to_run}" ]; then
        backends_to_run=""
        if is_backend_available btrfs; then backends_to_run="${backends_to_run} btrfs"; fi
        if is_backend_available ceph && ceph status --connect-timeout 3 >/dev/null 2>&1; then
            backends_to_run="${backends_to_run} ceph"
        fi
        backends_to_run="${backends_to_run} dir"
        if is_backend_available lvm; then backends_to_run="${backends_to_run} lvm lvm-nonthin"; fi
        if is_backend_available zfs; then backends_to_run="${backends_to_run} zfs"; fi
        backends_to_run="${backends_to_run# }"
    fi

    if [ "${RUN_STORAGE}" -eq 1 ]; then
        local b
        if [ "${COMPARE_TMPFS}" -eq 1 ]; then
            for b in ${backends_to_run}; do
                bench_storage_backend "${b}" 1 "Storage-dependent ops (tmpfs vs disk)" "${b} (tmpfs)"
                bench_storage_backend "${b}" 0 "Storage-dependent ops (tmpfs vs disk)" "${b} (disk)"
            done
        else
            local mode_lbl="disk"
            if [ "${USE_TMPFS}" -eq 1 ]; then
                mode_lbl="tmpfs"
            fi
            for b in ${backends_to_run}; do
                bench_storage_backend "${b}" "${USE_TMPFS}" "Storage-dependent ops (${mode_lbl})" "${b}"
            done
        fi
    fi

    if [ "${RUN_CLUSTER}" -eq 1 ]; then
        run_cluster_comparison
    fi
}

# ==============================================================================
# SNAP-TESTS MODE (runs against the live LXD snap daemon)
# ==============================================================================

wait_instance_ready_local() {
    local inst="${1}"
    local max_wait="${2:-120}"
    local procs
    for _ in $(seq 1 "${max_wait}"); do
        procs="$(lxc info "${inst}" 2>/dev/null | awk '{if ($1 == "Processes:") print $2}')"
        if [ "${procs:-0}" -ge 2 ]; then
            return 0
        fi
        sleep 1
    done
    return 1
}

wait_instance_booted_local() {
    local inst="${1}"
    local max_wait="${2:-120}"
    wait_instance_ready_local "${inst}" "${max_wait}"
    sleep 1
    local rc=0
    lxc exec "${inst}" -- timeout "${max_wait}" systemctl is-system-running --wait >/dev/null 2>&1 || rc=$?
    [ "${rc}" -ne 124 ]
}

detect_snap_image() {
    local img_type="${1}"
    local fallback="${2}"
    local fp
    fp="$(lxc image list -f csv -c f,t 2>/dev/null | awk -F, -v t="${img_type}" '$2 == t {print $1; exit}')"
    if [ -n "${fp}" ]; then
        echo "${fp}"
    else
        echo "${fallback}"
    fi
}

create_snap_bench_pool() {
    local driver="${1}"
    local pool="${2}"
    case "${driver}" in
        dir)
            lxc storage create "${pool}" dir >/dev/null 2>&1
            ;;
        btrfs)
            lxc storage create "${pool}" btrfs size=12GiB >/dev/null 2>&1
            ;;
        lvm)
            lxc storage create "${pool}" lvm lvm.use_thinpool=false size=30GiB volume.size=4GiB >/dev/null 2>&1
            ;;
        lvm-thin)
            lxc storage create "${pool}" lvm size=16GiB >/dev/null 2>&1
            ;;
        zfs)
            lxc storage create "${pool}" zfs size=16GiB >/dev/null 2>&1
            ;;
        ceph)
            lxc storage create "${pool}" ceph >/dev/null 2>&1
            ;;
        *)
            return 1
            ;;
    esac
}

run_snap_mode() {
    # Ensure we use the system/snap lxc binary, not any wrapper function.
    unset -f lxc lxc_remote 2>/dev/null || true

    if ! lxd waitready --timeout=30 >/dev/null 2>&1; then
        echo "LXD snap daemon is not ready; run on a snap-test VM with LXD snap active." >&2
        exit 1
    fi

    local ct_img="${SNAP_CT_IMAGE}"
    if [ -z "${ct_img}" ]; then
        ct_img="$(detect_snap_image "CONTAINER" "ubuntu-minimal-daily:24.04")"
    fi
    local vm_img="${SNAP_VM_IMAGE}"
    if [ -z "${vm_img}" ]; then
        vm_img="$(detect_snap_image "VIRTUAL-MACHINE" "ubuntu-minimal-daily:24.04")"
    fi
    echo "==> Snap mode images: container=${ct_img} vm=${vm_img}" >&2

    local ct_section="Snap-tests: Ubuntu minimal container ops"
    local vm_section="Snap-tests: Ubuntu minimal VM ops (3584MiB root)"

    local driver pool t0 t1 i
    for driver in ${SNAP_BACKENDS}; do
        pool="bench-snap-${driver}-$$"
        echo "==> Snap benchmark pool: ${driver} (${pool})" >&2
        t0="$(now_ms)"
        # shellcheck disable=SC2310
        if ! create_snap_bench_pool "${driver}" "${pool}"; then
            echo "  [skip] Cannot create ${driver} pool on this host" >&2
            continue
        fi
        t1="$(now_ms)"
        SNAP_CLEANUP_POOLS+=("${pool}")
        record_result "${ct_section}" "lxc storage create (snap pool)" "${driver}" "$((t1 - t0))"

        # --- Container benchmarks ---
        local -a s_ct_init=() s_ct_launch=() s_ct_ready=() s_ct_boot=() s_ct_exec=() s_ct_snap=() s_ct_copy=() s_ct_stop=() s_ct_del=()
        for i in $(seq 1 "${SLOW_ITERATIONS}"); do
            local cname="bct-${driver}-${i}-$$"
            t0="$(now_ms)"
            lxc init "${ct_img}" "${cname}" -s "${pool}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_init+=($((t1 - t0)))
            SNAP_CLEANUP_INSTANCES+=("${cname}")
            lxc delete "${cname}" >/dev/null 2>&1

            t0="$(now_ms)"
            lxc launch "${ct_img}" "${cname}" -s "${pool}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_launch+=($((t1 - t0)))

            t0="$(now_ms)"
            wait_instance_ready_local "${cname}" 60
            t1="$(now_ms)"
            s_ct_ready+=($((t1 - t0)))

            t0="$(now_ms)"
            wait_instance_booted_local "${cname}" 90
            t1="$(now_ms)"
            s_ct_boot+=($((t1 - t0)))

            t0="$(now_ms)"
            lxc exec "${cname}" -- true >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_exec+=($((t1 - t0)))

            t0="$(now_ms)"
            lxc snapshot "${cname}" s0 >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_snap+=($((t1 - t0)))

            t0="$(now_ms)"
            lxc stop -f "${cname}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_stop+=($((t1 - t0)))

            t0="$(now_ms)"
            lxc copy "${cname}" "${cname}-cp" >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_copy+=($((t1 - t0)))
            lxc delete -f "${cname}-cp" >/dev/null 2>&1

            t0="$(now_ms)"
            lxc delete -f "${cname}" >/dev/null 2>&1
            t1="$(now_ms)"
            s_ct_del+=($((t1 - t0)))
        done
        record_result "${ct_section}" "lxc init <ubuntu-ct>" "${driver}" "$(median_ms "${s_ct_init[@]}")" "${s_ct_init[*]}"
        record_result "${ct_section}" "lxc launch <ubuntu-ct>" "${driver}" "$(median_ms "${s_ct_launch[@]}")" "${s_ct_launch[*]}"
        record_result "${ct_section}" "waitInstanceReady (after launch)" "${driver}" "$(median_ms "${s_ct_ready[@]}")" "${s_ct_ready[*]}"
        record_result "${ct_section}" "waitInstanceBooted (after ready)" "${driver}" "$(median_ms "${s_ct_boot[@]}")" "${s_ct_boot[*]}"
        record_result "${ct_section}" "lxc exec -- true" "${driver}" "$(median_ms "${s_ct_exec[@]}")" "${s_ct_exec[*]}"
        record_result "${ct_section}" "lxc snapshot" "${driver}" "$(median_ms "${s_ct_snap[@]}")" "${s_ct_snap[*]}"
        record_result "${ct_section}" "lxc copy (stopped + 1 snap)" "${driver}" "$(median_ms "${s_ct_copy[@]}")" "${s_ct_copy[*]}"
        record_result "${ct_section}" "lxc stop -f" "${driver}" "$(median_ms "${s_ct_stop[@]}")" "${s_ct_stop[*]}"
        record_result "${ct_section}" "lxc delete -f" "${driver}" "$(median_ms "${s_ct_del[@]}")" "${s_ct_del[*]}"

        # --- VM benchmarks (real Ubuntu minimal VM) ---
        local -a s_vm_init=() s_vm_start=() s_vm_ready=() s_vm_boot=() s_vm_snap=() s_vm_restore=() s_vm_copy=() s_vm_stop=() s_vm_del=()
        for i in $(seq 1 "${SLOW_ITERATIONS}"); do
            local vname="bvm-${driver}-${i}-$$"
            t0="$(now_ms)"
            if lxc init "${vm_img}" "${vname}" --vm -s "${pool}" -c limits.memory=1GiB -d root,size=3584MiB >/dev/null 2>&1; then
                t1="$(now_ms)"
                s_vm_init+=($((t1 - t0)))
                SNAP_CLEANUP_INSTANCES+=("${vname}")

                t0="$(now_ms)"
                lxc start "${vname}" >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_start+=($((t1 - t0)))

                t0="$(now_ms)"
                wait_instance_ready_local "${vname}" 120
                t1="$(now_ms)"
                s_vm_ready+=($((t1 - t0)))

                t0="$(now_ms)"
                wait_instance_booted_local "${vname}" 120
                t1="$(now_ms)"
                s_vm_boot+=($((t1 - t0)))

                t0="$(now_ms)"
                lxc stop -f "${vname}" >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_stop+=($((t1 - t0)))

                t0="$(now_ms)"
                lxc snapshot "${vname}" s0 >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_snap+=($((t1 - t0)))

                t0="$(now_ms)"
                lxc restore "${vname}" s0 >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_restore+=($((t1 - t0)))

                SNAP_CLEANUP_INSTANCES+=("${vname}-cp")
                t0="$(now_ms)"
                lxc copy "${vname}" "${vname}-cp" >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_copy+=($((t1 - t0)))
                lxc delete -f "${vname}-cp" >/dev/null 2>&1

                t0="$(now_ms)"
                lxc delete -f "${vname}" >/dev/null 2>&1
                t1="$(now_ms)"
                s_vm_del+=($((t1 - t0)))
            else
                echo "  [skip] VM benchmark failed on ${driver}" >&2
                break
            fi
        done

        if [ "${#s_vm_init[@]}" -gt 0 ]; then
            record_result "${vm_section}" "lxc init <ubuntu-vm> --vm (3584MiB)" "${driver}" "$(median_ms "${s_vm_init[@]}")" "${s_vm_init[*]}"
            record_result "${vm_section}" "lxc start <ubuntu-vm>" "${driver}" "$(median_ms "${s_vm_start[@]}")" "${s_vm_start[*]}"
            record_result "${vm_section}" "waitInstanceReady (VM agent up)" "${driver}" "$(median_ms "${s_vm_ready[@]}")" "${s_vm_ready[*]}"
            record_result "${vm_section}" "waitInstanceBooted (VM systemd)" "${driver}" "$(median_ms "${s_vm_boot[@]}")" "${s_vm_boot[*]}"
            record_result "${vm_section}" "lxc stop -f <ubuntu-vm>" "${driver}" "$(median_ms "${s_vm_stop[@]}")" "${s_vm_stop[*]}"
            record_result "${vm_section}" "lxc snapshot (VM)" "${driver}" "$(median_ms "${s_vm_snap[@]}")" "${s_vm_snap[*]}"
            record_result "${vm_section}" "lxc restore (VM)" "${driver}" "$(median_ms "${s_vm_restore[@]}")" "${s_vm_restore[*]}"
            record_result "${vm_section}" "lxc copy (VM + 1 snap)" "${driver}" "$(median_ms "${s_vm_copy[@]}")" "${s_vm_copy[*]}"
            record_result "${vm_section}" "lxc delete -f <ubuntu-vm>" "${driver}" "$(median_ms "${s_vm_del[@]}")" "${s_vm_del[*]}"
        fi

        lxc storage delete "${pool}" >/dev/null 2>&1 || true
    done

    # Optional nested snapd/LXD install benchmark.
    if [ "${RUN_NESTED}" -eq 1 ]; then
        local nsec="Snap-tests: Nested snapd/LXD setup"
        local nname="bnest-$$"
        lxc launch "${ct_img}" "${nname}" -c security.nesting=true >/dev/null 2>&1
        SNAP_CLEANUP_INSTANCES+=("${nname}")
        wait_instance_booted_local "${nname}" 90

        t0="$(now_ms)"
        if lxc exec "${nname}" -- timeout 90 snap wait system seed.loaded >/dev/null 2>&1; then
            t1="$(now_ms)"
            record_result "${nsec}" "snap wait system seed.loaded (in container)" "default" "$((t1 - t0))"

            t0="$(now_ms)"
            if lxc exec "${nname}" -- snap install lxd --channel=latest/edge --cohort=+ >/dev/null 2>&1; then
                t1="$(now_ms)"
                record_result "${nsec}" "snap install lxd (in container)" "default" "$((t1 - t0))"
            else
                echo "  [skip] Nested LXD snap install failed in ${nname}" >&2
            fi
        else
            echo "  [skip] Nested snapd seeding failed in ${nname}" >&2
        fi

        lxc delete -f "${nname}" >/dev/null 2>&1
    fi
}

# ==============================================================================
# MAIN
# ==============================================================================

echo "# LXD Test Operation Benchmark Results"
echo ""
echo "- Host: $(hostname) ($(uname -r), $(nproc) vCPU, $(awk '/MemTotal/ {printf "%.1fGiB", $2/1048576}' /proc/meminfo) RAM)"
echo "- Date (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "- Mode: ${MODE} | Iterations: ${ITERATIONS} (slow: ${SLOW_ITERATIONS})"

if [ "${MODE}" = "system" ] || [ "${MODE}" = "all" ]; then
    run_system_mode
fi

if [ "${MODE}" = "snap" ] || [ "${MODE}" = "all" ]; then
    run_snap_mode
fi

for sec in "${BENCH_SECTIONS[@]}"; do
    print_section_table "${sec}"
done
