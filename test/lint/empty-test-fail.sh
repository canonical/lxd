#!/bin/bash
set -eu
set -o pipefail

# We set -e in the test suite to ensure an exit on error. Unfortunately,
# `-e` doesn't apply inside a test.
#
# This means that any test that runs `[ "$(cmd)" = "" ]` is not erroring
# out if cmd fails, and the test is asserting that the stdout from cmd
# is empty. This is usually true if the command fails, as it will send
# output to stderr instead.
#
# This linter enforces that any tests of this kind are of the form `[ "$(cmd || echo fail)" = "" ]`.
# This means that if the command fails, bash will execute the OR statement
# and echo "fail", causing the test to fail.
#
# Tests for emptiness can take multiple forms but here are the predominant ones
# (not an exhaustive list, omits those with `!` and `-n`):
#
# POSIX:  [ "$(cmd) = "" ]    or  [ -z "$(cmd)" ]
# bash:  [[ "$(cmd)" == "" ]] or [[ -z "$(cmd)" ]]
#
out="$(grep -rnE '\[\s+(-z\s+"\$\(.*\)"|"\$\(.*\)"\s+=+\s+["'\'']{2})\s+\]' --exclude="$(basename "${0}")" test/ | grep -Fv ' || echo fail)' || true)"
if [ "${out}" != '' ] ; then
  # shellcheck disable=SC2016
  echo 'One or more assertions of type [ "$(cmd)" = '' ] require `|| echo fail`'
  echo '=========='
  echo "${out}"
  exit 1
fi

exit 0
