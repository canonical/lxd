#!/bin/bash
set -eu
set -o pipefail

# Ensure that all test scripts use `jq` or `yq` with `--exit-status` when
# checking for JSON/YAML.

RC=0

# Find all jq/yq invocations without --exit-status.
# Notes:
#   - ':[0-9]+:\s*#' drops comment-only lines (grep -n output format: file:line:content).
#   - 'check_dependencies' drops the dependency-list call in main.sh where jq/yq are
#     tool names passed as arguments, not command invocations.
#   - '\b(jq|yq)\b[^|]*[[:space:]]--exit-status\b' drops lines where the jq/yq invocation already
#     carries --exit-status.
OUTPUT="$(grep --include='*.sh' --exclude-dir=lint -rnE '\b(jq|yq)\b' test/ | \
    grep -vE ':[0-9]+:\s*#' | \
    grep -vwF 'check_dependencies' | \
    grep -vE '\b(jq|yq)\b[^|]*[[:space:]]--exit-status\b' \
    || true)"

if [ -n "${OUTPUT}" ]; then
    echo "FAIL: use '--exit-status' with jq and yq when checking for JSON/YAML"
    echo "${OUTPUT}"
    echo
    RC=1
fi

exit "${RC}"
