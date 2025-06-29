#!/bin/bash
set -eu
set -o pipefail

# This is a meta-test as it tests the tests themselves. It makes sure that all
# the exising test functions are being called by the test suite.

# It also check there are no invalid test constructs like: `! cmd_should_fail || true`

# Ensure predictable sorting
export LC_ALL=C.UTF-8

CALLED_TESTS="$(mktemp)"
EXISTING_TESTS="$(mktemp)"
SKIPPED_TESTS="$(mktemp)"
REQUIRED_TESTS="$(mktemp)"

LXD_SKIP_TESTS="${LXD_SKIP_TESTS:-}"
LXD_REQUIRED_TESTS="${LXD_REQUIRED_TESTS:-}"

echo "${LXD_SKIP_TESTS}" | tr ' ' '\n' > "${SKIPPED_TESTS}"
echo "${LXD_REQUIRED_TESTS}" | tr ' ' '\n' > "${REQUIRED_TESTS}"

# Validate the skipped and required tests
if grep '^test_' "${SKIPPED_TESTS}"; then
    echo 'LXD_SKIP_TESTS should not start with "test_"' >&2
    exit 1
fi
if grep '^test_' "${REQUIRED_TESTS}"; then
    echo 'LXD_REQUIRED_TESTS should not start with "test_"' >&2
    exit 1
fi

# Validate that required tests are not skipped
if [ -n "${LXD_SKIP_TESTS}" ] && [ -n "${LXD_REQUIRED_TESTS}" ]; then
  if grep -xf "${SKIPPED_TESTS}" "${REQUIRED_TESTS}"; then
      echo "LXD_REQUIRED_TESTS cannot be skipped" >&2
      exit 1
  fi
fi

# Warn if skipping tests
if [ -n "${LXD_SKIP_TESTS}" ]; then
    echo "::warning::Skipped tests: ${LXD_SKIP_TESTS}"
fi

sed -n 's/^\s*run_test test_\([^ ]\+\).*/\1/p' test/main.sh                 | grep -vxf "${SKIPPED_TESTS}" | sort > "${CALLED_TESTS}"
grep -hxE 'test_[^(]+\(\) ?{' test/suites/* | sed 's/^test_//; s/() \?{$//' | grep -vxf "${SKIPPED_TESTS}" | sort > "${EXISTING_TESTS}"

diff -Nau "${CALLED_TESTS}" "${EXISTING_TESTS}"

# Cleanup
rm -f "${CALLED_TESTS}" "${EXISTING_TESTS}" "${SKIPPED_TESTS}" "${REQUIRED_TESTS}"


# Check for invalid test constructs
if grep -rlE '\!.* \|\| true$' test/; then
    echo "Some tests commands are ignoring expected failures (! cmd_should_fail || true)" >&2
    exit 1
fi
if grep -rlE '^\s*[^\!]+ \|\| false$' test/; then
    echo "Some tests commands use unneeded construct to fail (cmd_should_succeed || false)" >&2
    exit 1
fi

# Check for `! cmd_should_fail` missing the needed `|| false` due to how `bash`
# treats compound commands with `set -e`. Ignore ` || false # comment` with `sed`.
if grep -rE '^\s*!.+$' --include="*.sh" test/ | sed 's/ \+# .*//' | grep -v '|| false$'; then
    echo "Some tests commands expected to fail (!) are missing the '|| false' fallback (! cmd_should_fail || false)" >&2
    exit 1
fi
