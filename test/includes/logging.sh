# Logging helper for sub-test output.
sub_test() {
    { set +x; } 2>/dev/null

    echo "==> SUB_TEST: ${1} (${TEST_CURRENT_DESCRIPTION})"

    if [ -n "${SHELL_TRACING:-}" ]; then
        set -x
    fi
}
