#!/bin/sh -eu
#
# Performance tests runner
#

[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

PERF_LOG_CSV="perf.csv"

# shellcheck disable=SC2034
LXD_ALT_CERT=""

# shellcheck disable=SC2034
LXD_NETNS=""

import_subdir_files() {
    test  "$1"
    # shellcheck disable=SC2039
    local file
    for file in "$1"/*.sh; do
        # shellcheck disable=SC1090
        . "$file"
    done
}

import_subdir_files includes

log_message() {
    echo "==>" "$@"
}

run_benchmark() {
    # shellcheck disable=SC2039
    local label description
    label="$1"
    description="$2"
    shift 2

    log_message "Benchmark start: $label - $description"
    lxd-benchmark "$@" --report-file "$PERF_LOG_CSV" --report-label "$label"
    log_message "Benchmark completed: $label"
}

cleanup() {
    if [ "$TEST_RESULT" != "success" ]; then
        rm -f "$PERF_LOG_CSV"
    fi
    lxd-benchmark delete  # ensure all test containers have been deleted
    kill_lxd "$LXD_DIR"
    cleanup_lxds "$TEST_DIR"
    log_message "Performance tests result: $TEST_RESULT"
}

trap cleanup EXIT HUP INT TERM

# Setup test directories
TEST_DIR=$(mktemp -d -p "$(pwd)" tmp.XXX)

if [ -n "${LXD_TMPFS:-}" ]; then
  mount -t tmpfs tmpfs "${TEST_DIR}" -o mode=0751
fi

LXD_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_DIR
chmod +x "${TEST_DIR}" "${LXD_DIR}"

if [ -z "${LXD_BACKEND:-}" ]; then
    LXD_BACKEND="dir"
fi

import_storage_backends

spawn_lxd "${LXD_DIR}" true
ensure_import_testimage

# shellcheck disable=SC2034
TEST_RESULT=failure

run_benchmark "create-one" "create 1 container" init --count 1 "${LXD_TEST_IMAGE:-"testimage"}"
run_benchmark "start-one" "start 1 container" start
run_benchmark "stop-one" "stop 1 container" stop
run_benchmark "delete-one" "delete 1 container" delete
run_benchmark "create-128" "create 128 containers" init --count 128 "${LXD_TEST_IMAGE:-"testimage"}"
run_benchmark "start-128" "start 128 containers" start
run_benchmark "delete-128" "delete 128 containers" delete

# shellcheck disable=SC2034
TEST_RESULT=success
