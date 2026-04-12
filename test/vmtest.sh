#!/bin/bash
# Keep error handling explicit in this wrapper so it can preserve the guest
# command's exit status and still run host-side cleanup paths predictably.
set -u
set -o pipefail

# === static configuration === #
SCRIPT_DIR="$(dirname "${BASH_SOURCE[0]}")"

readonly PROFILE_NAME="lxd-test"
readonly IMAGE_NAME="ubuntu-minimal-daily:24.04"
readonly REPO_PATH="/root/lxd"
readonly TEST_PATH="${REPO_PATH}/test"
readonly DEFAULT_ARTIFACTS_ROOT="/tmp"

# === wrapper state === #
keep_artifacts=0
artifacts_dir=""
timeout_duration=""
caffeinate=0
caffeinate_duration=""

main_args=()
instance_name=""
instance_created=0
cleanup_done=0
run_exit=0
run_exit_set=0
timed_out=0
cleanup_state="pending"
artifacts_state="pending"
summary_enabled=0

HOST_LXC=""
guest_output_log=""
cloud_init_log=""
guest_env=()

# === wrapper helpers === #
usage() {
    cat <<EOF
Usage: $0 [OPTION]... <main.sh arg> [<main.sh arg> ...]

Launch an ephemeral VM using the lxd-test profile, run the supplied
'test/main.sh' arguments inside the VM, and stream output back to the host.

By default, host-side artifacts are written to a temporary directory under
'/tmp' and removed after a successful non-caffeinated run. Use
'--keep-artifacts' to retain them.

If the guest does not already have LXD test binaries in GOPATH/bin, the wrapper
builds them inside the VM before running tests.

Options:
  -h, --help
    Show this help text.
  --keep-artifacts
    Preserve host-side artifacts even after a successful non-caffeinated run.
  --timeout=DURATION
  --timeout DURATION
    Stop waiting after DURATION. Uses 'timeout' syntax such as 600, 10m, or 2h.
  --caffeinate
    Keep the VM running after the command exits.
  --caffeinate=DURATION
  --caffeinate DURATION
    Keep the VM running after the command exits, then stop it after DURATION.
  --
    Stop parsing wrapper options and pass the remaining args to 'main.sh'.

Environment:
  LXC_BIN
    Override the host-side 'lxc' binary path used by the wrapper.
    This is mainly for agents running in environments where 'lxc' resolves to
    a snap wrapper that cannot access the local LXD daemon correctly.
  MAX_WAIT_SECONDS
    Override the instance boot wait timeout (default: 120 seconds).
  LXD_BACKEND
  LXD_BACKENDS
  LXD_SKIP_TESTS
  LXD_REQUIRED_TESTS
  LXD_REPEAT_TESTS
  LXD_VERBOSE
  LXD_DEBUG
  LXD_VM_TESTS
    Forwarded into the guest if set; other LXD_* variables are ignored.

Examples:
  $0 exec
  $0 group:all
  $0 --timeout=30m basic_usage
  $0 --keep-artifacts basic_usage
  $0 --caffeinate=10m basic_usage
EOF
}

validate_duration() {
    [[ "${1}" =~ ^[0-9]+([.][0-9]+)?[smhd]?$ ]]
}

log_info() {
    printf '==> %s\n' "$*"
}

log_error() {
    printf 'ERROR: %s\n' "$*" >&2
}

prepare_artifacts() {
    artifacts_dir="$(mktemp -d "${DEFAULT_ARTIFACTS_ROOT}/vmtest.${instance_name}.XXXXXX")" || return 1

    # Default to a temp directory so normal runs do not leave repo-local noise,
    # but still keep enough context to diagnose a failing VM-backed run.
    guest_output_log="${artifacts_dir}/guest-output.log"
    cloud_init_log="${artifacts_dir}/cloud-init.log"

    : > "${guest_output_log}" || return 1
    : > "${cloud_init_log}" || return 1

    printf 'INSTANCE=%s\n' "${instance_name}" || return 1
    printf 'ARTIFACTS_DIR=%s\n' "${artifacts_dir}" || return 1
}

capture_failure_artifacts() {
    [ "${instance_created}" = "1" ] || return
    [ -n "${artifacts_dir}" ] || return

    "${HOST_LXC}" info "${instance_name}" --show-log > "${artifacts_dir}/lxc-info.txt" 2>&1 || true
    "${HOST_LXC}" list --format json "${instance_name}" > "${artifacts_dir}/lxc-list.json" 2>&1 || true
    "${HOST_LXC}" exec "${instance_name}" -- journalctl --no-pager -b > "${artifacts_dir}/journalctl.txt" 2>&1 || true
}

wait_for_exec_ready() {
    local i=0
    local max_wait=$(( ${MAX_WAIT_SECONDS:-120} / 2 ))

    # VMs can report as booted before the guest agent is ready for lxc exec.
    # Print progress every 30s so agent shell tools that time out on silent
    # output streams do not kill the wrapper prematurely.
    for ((i = 0; i < max_wait; i++)); do
        if "${HOST_LXC}" exec "${instance_name}" -- true >/dev/null 2>&1; then
            return 0
        fi

        if [ $(( (i + 1) % 15 )) -eq 0 ]; then
            log_info "Still waiting for lxc exec readiness ($(( (i + 1) * 2 ))s elapsed)"
        fi

        sleep 2
    done

    return 1
}

wait_for_instance_boot() {
    local i

    # Mirrors waitInstanceReady() in test/includes/lxd.sh but uses ${HOST_LXC}
    # so the LXC_BIN override is respected here as well.
    # Print progress every 30s so agent shell tools that time out on silent
    # output streams do not kill the wrapper prematurely.
    for i in $(seq "${MAX_WAIT_SECONDS:-120}"); do
        if "${HOST_LXC}" query "/1.0/instances/${instance_name}/state" | jq --exit-status '.processes | select(. > 0)' >/dev/null 2>&1; then
            return 0
        fi

        if [ $(( i % 30 )) -eq 0 ] && [ "${i}" -gt 0 ]; then
            log_info "Still waiting for instance boot (${i}s elapsed)"
        fi

        sleep 1
    done

    return 1
}

prepare_guest_env() {
    local var=""
    local -a forwarded_env=()

    guest_env=()

    # Keep vmtest.sh intentionally narrow: it is a fresh-VM wrapper, not a
    # second fully-configurable frontend for every main.sh environment knob.
    for var in LXD_BACKEND LXD_BACKENDS LXD_SKIP_TESTS LXD_REQUIRED_TESTS LXD_REPEAT_TESTS LXD_VERBOSE LXD_DEBUG LXD_VM_TESTS; do
        if [ "${!var+x}" = "x" ]; then
            guest_env+=("${var}=${!var}")
            forwarded_env+=("${var}")
        fi
    done

    if [ "${#forwarded_env[@]}" -gt 0 ]; then
        log_info "Forwarding environment: ${forwarded_env[*]}"
    fi
}

guest_binaries_ready() {
    # The lxd-test profile disables the system LXD binaries, so the guest needs
    # the repo-built tools in GOPATH/bin before test/main.sh can run.
    # shellcheck disable=SC2016
    "${HOST_LXC}" exec "${instance_name}" -- bash -lc '
set -eu
GOPATH="${GOPATH:-$(go env GOPATH)}"

for bin in lxc lxd lxd-agent lxd-user; do
    [ -x "${GOPATH}/bin/${bin}" ] || exit 1
done
'
}

prepare_guest_environment() {
    if guest_binaries_ready >/dev/null 2>&1; then
        return 0
    fi

    log_info "Preparing guest LXD binaries"

    # Build inside the VM so the lxd-test profile can use the repo-mounted tree
    # directly and so failures reflect the guest environment rather than a
    # potentially stale host build.
    # Use a login shell (-l) to source ~/.profile, which adds Go's GOPATH/bin to PATH.
    # shellcheck disable=SC2016
    run_with_logging "${HOST_LXC}" exec "${instance_name}" -- bash -lc '
set -eu
cd "$1"
make deps
eval "$(make -s env)" >/dev/null
make
' bash "${REPO_PATH}"
}

run_with_logging() {
    local -a cmd=("$@")
    local -a pipe_status=()
    local cmd_status=""
    local tee_status=""

    "${cmd[@]}" |& tee -a "${guest_output_log}"
    pipe_status=("${PIPESTATUS[@]}")
    cmd_status="${pipe_status[0]}"
    tee_status="${pipe_status[1]}"

    if [ "${tee_status}" -ne 0 ]; then
        log_error "Failed writing guest output log: ${guest_output_log}"
    fi

    if [ "${cmd_status}" -ne 0 ]; then
        return "${cmd_status}"
    fi

    return "${tee_status}"
}

launch_vm() {
    log_info "Launching ephemeral VM ${instance_name}"

    if ! "${HOST_LXC}" profile list --format json | jq -e --arg p "${PROFILE_NAME}" 'map(.name) | index($p) != null' >/dev/null; then
        log_error "Required LXD profile not found: ${PROFILE_NAME}"
        log_error "Load it from doc/lxd-test.yaml first."
        return 1
    fi

    if ! "${HOST_LXC}" launch "${IMAGE_NAME}" "${instance_name}" --vm -p "${PROFILE_NAME}" --ephemeral; then
        log_error "Failed launching ${instance_name}"
        return 1
    fi

    instance_created=1

    log_info "Waiting for instance boot"
    if ! wait_for_instance_boot; then
        log_error "Instance did not boot cleanly: ${instance_name}"
        return 1
    fi

    log_info "Waiting for lxc exec readiness"
    if ! wait_for_exec_ready; then
        log_error "VM did not become ready for lxc exec: ${instance_name}"
        return 1
    fi

    log_info "Waiting for cloud-init"
    # Print a heartbeat every 30s so agent shell tools that time out on silent
    # output streams do not kill the wrapper during cloud-init.
    ( while sleep 30; do log_info "Still waiting for cloud-init..."; done ) &
    local heartbeat_pid=$!
    "${HOST_LXC}" exec "${instance_name}" -- cloud-init status --wait --long > "${cloud_init_log}" 2>&1
    local ci_rc=$?
    kill "${heartbeat_pid}" 2>/dev/null || true
    wait "${heartbeat_pid}" 2>/dev/null || true
    if [ "${ci_rc}" -ne 0 ]; then
        log_error "cloud-init did not finish successfully in ${instance_name}"
        log_error "See ${cloud_init_log} for details"
        return 1
    fi

    if ! "${HOST_LXC}" exec "${instance_name}" -- test -x "${TEST_PATH}/main.sh"; then
        log_error "Test runner not found in VM at ${TEST_PATH}/main.sh"
        log_error "The ${PROFILE_NAME} profile should mount this repository at ${REPO_PATH}."
        return 1
    fi

    if ! prepare_guest_environment; then
        log_error "Failed preparing guest LXD binaries"
        return 1
    fi

    return 0
}

run_main() {
    local -a guest_prefix=("${HOST_LXC}" exec "${instance_name}" -- env "${guest_env[@]}")

    # Pass the repo path separately so the guest command can re-enter the repo
    # root before forwarding the original main.sh argv unchanged.
    # shellcheck disable=SC2016
    local -a guest_cmd=("${guest_prefix[@]}" bash -lc '
set -eu
cd "$1"
eval "$(make -s env)" >/dev/null
cd test
shift
exec ./main.sh "$@"
' bash "${REPO_PATH}" "${main_args[@]}")

    local -a final_cmd=()
    if [ -n "${timeout_duration}" ]; then
        final_cmd=(timeout --foreground "${timeout_duration}" "${guest_cmd[@]}")
    else
        final_cmd=("${guest_cmd[@]}")
    fi

    log_info "Running ./main.sh ${main_args[*]}"

    run_with_logging "${final_cmd[@]}"
    local rc=$?

    if [ "${rc}" -eq 0 ]; then
        return 0
    fi

    if [ -n "${timeout_duration}" ] && [ "${rc}" -eq 124 ]; then
        timed_out=1
    fi

    return "${rc}"
}

# ShellCheck can't see trap-driven or detached invocations for these helpers.
# shellcheck disable=SC2317,SC2329
schedule_stop() {
    [ -n "${caffeinate_duration}" ] || return 0

    # Detach the stop so caffeinate can return control immediately while still
    # enforcing eventual cleanup of the ephemeral VM.
    # shellcheck disable=SC2016
    nohup bash -c 'sleep "$1" && "$2" stop --force "$3" >/dev/null 2>&1' _ "${caffeinate_duration}" "${HOST_LXC}" "${instance_name}" >/dev/null 2>&1 &
}

# ShellCheck can't see trap-driven invocations for these cleanup helpers.
# shellcheck disable=SC2317,SC2329
should_keep_artifacts() {
    [ -n "${artifacts_dir}" ] || return 1
    [ -d "${artifacts_dir}" ] || return 1

    [ "${keep_artifacts}" = "1" ] && return 0
    [ "${caffeinate}" = "1" ] && return 0
    [ "${run_exit}" -ne 0 ] && return 0
    [ "${timed_out}" = "1" ] && return 0
    [ "${cleanup_state}" = "stop-failed" ] && return 0

    return 1
}

# Trap-driven cleanup is the only reliable way to preserve the guest exit code
# and still stop the VM or retain artifacts on failures and interrupts.
# ShellCheck can't see trap-driven invocations for these cleanup helpers.
# shellcheck disable=SC2317,SC2329
cleanup() {
    [ "${cleanup_done}" = "0" ] || return
    cleanup_done=1

    if [ "${instance_created}" != "1" ]; then
        cleanup_state="not-created"
        return
    fi

    if [ "${caffeinate}" = "1" ]; then
        if [ -n "${caffeinate_duration}" ]; then
            log_info "Leaving ${instance_name} running for ${caffeinate_duration}"
            schedule_stop || true
            cleanup_state="scheduled-stop"
        else
            log_info "Leaving ${instance_name} running"
            cleanup_state="left-running"
        fi

        log_info "Reconnect with: lxc exec ${instance_name} -- bash"
        return
    fi

    log_info "Stopping ${instance_name}"
    if "${HOST_LXC}" stop --force "${instance_name}" >/dev/null 2>&1; then
        cleanup_state="stopped"
    else
        cleanup_state="stop-failed"
    fi
}

# ShellCheck can't see trap-driven invocations for these cleanup helpers.
# shellcheck disable=SC2317,SC2329
prune_artifacts() {
    [ -n "${artifacts_dir}" ] || return
    [ -d "${artifacts_dir}" ] || return

    if should_keep_artifacts; then
        artifacts_state="retained"
        return
    fi

    rm -rf -- "${artifacts_dir}"
    artifacts_state="deleted"
}

# ShellCheck can't see trap-driven invocations for these cleanup helpers.
# shellcheck disable=SC2317,SC2329
write_summary() {
    [ "${summary_enabled}" = "1" ] || return

    printf 'EXIT_CODE=%s\n' "${run_exit}"
    printf 'SUMMARY instance=%s exit_code=%s cleanup=%s artifacts=%s artifacts_dir=%s\n' \
        "${instance_name}" "${run_exit}" "${cleanup_state}" "${artifacts_state}" "${artifacts_dir}"
}

# ShellCheck can't see trap-driven invocations for these cleanup helpers.
# shellcheck disable=SC2317,SC2329
on_exit() {
    local rc=$?
    trap - EXIT INT TERM HUP

    # Prefer the captured guest exit status so wrapper cleanup cannot overwrite
    # the test result with a trap-side success or failure code.
    if [ "${run_exit_set}" = "0" ]; then
        run_exit="${rc}"
        run_exit_set=1
    fi

    cleanup
    prune_artifacts
    write_summary
    exit "${run_exit}"
}

# ShellCheck can't see trap-driven invocations for these cleanup helpers.
# shellcheck disable=SC2317,SC2329
on_signal() {
    case "${1}" in
        INT)
            run_exit=130
            ;;
        TERM)
            run_exit=143
            ;;
        HUP)
            run_exit=129
            ;;
        *)
            run_exit=1
            ;;
    esac

    run_exit_set=1
    exit "${run_exit}"
}

parse_args() {
    # Keep wrapper parsing narrow so new main.sh arguments do not require
    # wrapper changes unless they affect VM lifecycle behavior.
    while [ "$#" -gt 0 ]; do
        case "${1}" in
            -h|--help)
                usage
                exit 0
                ;;
            --keep-artifacts)
                keep_artifacts=1
                ;;
            --timeout)
                [ "$#" -ge 2 ] || {
                    log_error "Missing value for --timeout"
                    exit 1
                }

                timeout_duration="${2}"
                validate_duration "${timeout_duration}" || {
                    log_error "Invalid --timeout duration: ${timeout_duration}"
                    exit 1
                }

                shift
                ;;
            --timeout=*)
                timeout_duration="${1#--timeout=}"
                validate_duration "${timeout_duration}" || {
                    log_error "Invalid --timeout duration: ${timeout_duration}"
                    exit 1
                }
                ;;
            --caffeinate)
                caffeinate=1
                if [ "$#" -ge 2 ] && validate_duration "${2}"; then
                    caffeinate_duration="${2}"
                    shift
                elif [ "$#" -ge 2 ] && [[ "${2}" == -* || "${2}" == "--" ]]; then
                    : # next arg is another option, not a duration
                elif [ "$#" -ge 2 ] && [ -n "${2}" ]; then
                    log_error "Invalid --caffeinate duration: ${2} (use e.g. 10m, 2h, 600)"
                    exit 1
                fi
                ;;
            --caffeinate=*)
                caffeinate=1
                caffeinate_duration="${1#--caffeinate=}"
                validate_duration "${caffeinate_duration}" || {
                    log_error "Invalid --caffeinate duration: ${caffeinate_duration}"
                    exit 1
                }
                ;;
            --)
                shift
                break
                ;;
            -*)
                log_error "Unknown option: ${1}"
                usage >&2
                exit 1
                ;;
            *)
                break
                ;;
        esac

        shift
    done

    if [ "$#" -eq 0 ]; then
        usage >&2
        exit 1
    fi

    main_args=("$@")
}

main() {
    parse_args "$@"

    # Allow an explicit host client path because agent environments often see a
    # snap-wrapped lxc that cannot talk to the local daemon correctly.
    HOST_LXC="${LXC_BIN:-$(command -v lxc 2>/dev/null || true)}"
    if [ -z "${HOST_LXC}" ]; then
        log_error "Unable to find host lxc client"
        return 1
    fi

    if ! command -v jq >/dev/null 2>&1; then
        log_error "Required tool not found: jq"
        return 1
    fi

    # shellcheck disable=SC1091
    . "${SCRIPT_DIR}/includes/helpers.sh"
    # Keep the familiar lxdtest prefix so retained VMs are obvious in `lxc ls`.
    instance_name="lxdtest-$(uuidgen)"
    prepare_guest_env || return 1

    prepare_artifacts || {
        log_error "Failed creating artifacts directory"
        return 1
    }

    # Print summary output only after the wrapper has enough state to describe
    # the run meaningfully. Before this point, there may be no instance name or
    # artifact path to report.
    summary_enabled=1

    if ! launch_vm; then
        # Capture host-side failure context before EXIT cleanup stops the VM.
        # Note: signal interrupts (INT/TERM/HUP) bypass this path and skip
        # artifact capture intentionally — the user interrupted the run.
        capture_failure_artifacts
        return 1
    fi

    run_main
    local rc=$?

    if [ "${rc}" -eq 0 ]; then
        return 0
    fi

    # Preserve additional guest context for failing tests before the VM is
    # stopped by the EXIT trap.
    capture_failure_artifacts

    return "${rc}"
}

# Route all exits through the traps so the wrapper can preserve the guest exit
# status, stop or retain the VM consistently, and prune artifacts in one place.
trap on_exit EXIT
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap 'on_signal HUP' HUP

if main "$@"; then
    run_exit=0
else
    run_exit=$?
fi

# Mark the guest exit status as authoritative before the script reaches the
# EXIT trap so trap-side commands cannot replace it with their own status.
run_exit_set=1
exit "${run_exit}"
