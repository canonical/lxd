#!/bin/sh -eu
#
# Performance tests runner
#

[ -n "${GOPATH:-}" ] && export "PATH=${GOPATH}/bin:${PATH}"

PERF_LOG_CSV="perf.csv"

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
    local label description opts
    label="$1"
    description="$2"
    shift 2

    log_message "Benchmark start: $label - $description"
    lxd_benchmark "$@" --report-file "$PERF_LOG_CSV" --report-label "$label"
    log_message "Benchmark completed: $label"
}

lxd_benchmark() {
    # shellcheck disable=SC2039
    local opts
    [ "${LXD_TEST_IMAGE:-}" ] && opts="--image $LXD_TEST_IMAGE" || opts=""
    lxd-benchmark "$@" $opts
}

cleanup() {
    if [ "$TEST_RESULT" != "success" ]; then
        rm -f "$PERF_LOG_CSV"
    fi
    lxd_benchmark delete  # ensure all test containers have been deleted
    kill_lxd "$LXD_DIR"
    cleanup_lxds "$TEST_DIR"
    log_message "Performance tests result: $TEST_RESULT"
}

trap cleanup EXIT HUP INT TERM

# Setup test directories
TEST_DIR=$(mktemp -d -p "$(pwd)" tmp.XXX)
LXD_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
export LXD_DIR
chmod +x "${TEST_DIR}" "${LXD_DIR}"

if [ -z "${LXD_BACKEND:-}" ]; then
    LXD_BACKEND="dir"
fi

import_storage_backends

spawn_lxd "${LXD_DIR}" true

# shellcheck disable=SC2034
TEST_RESULT=failure

run_benchmark "create-one" "create 1 container" spawn --count 1 --start=false
run_benchmark "start-one" "start 1 container" start
run_benchmark "stop-one" "stop 1 container" stop
run_benchmark "delete-one" "delete 1 container" delete
run_benchmark "create-128" "create 128 containers" spawn --count 128 --start=false
run_benchmark "delete-128" "delete 128 containers" delete

# shellcheck disable=SC2034
TEST_RESULT=success
