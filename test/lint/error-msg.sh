#!/bin/bash
set -eu
set -o pipefail

# This linter enforces error message style conventions tree-wide on all Go
# files (excluding protobuf bindings).
#
# Checks enforced:
#   1. No "Failed to <verb>" or "unable to" -- use gerund or "cannot" instead.
#   2. No contractions -- "cannot" not "can't", "do not" not "don't".

echo "Checking error message style in Go files..."

RC=0

# Search Go string literals for a PCRE pattern, skipping comment lines.
# Usage: check_strings <pattern> <error_message> [<extra_filter>]
# shellcheck disable=SC2016 # Backticks in patterns are literal regex chars, not command substitutions.
check_strings() {
  local pattern="${1}"
  local message="${2}"
  local extra_filter="${3:-}"

  OUT=$(git grep -n --untracked -P "${pattern}" '*.go' ':!:*.pb.go' | grep -v -P '^\S+:\d+:\s*//' || true)
  if [ -n "${extra_filter}" ]; then
    OUT=$(echo "${OUT}" | grep -v "${extra_filter}" || true)
  fi

  if [ -n "${OUT}" ]; then
    echo "ERROR: ${message}"
    echo "${OUT}"
    echo
    RC=1
  fi
}

# Check 1: "Failed to <verb>" or "unable to" -> use gerund or "cannot" instead.
#   "Failed to read"    -> "Failed reading"
#   "Unable to connect" -> "Cannot connect"
# The [Ff]ailed pattern requires the phrase right after the opening quote
# to avoid matching mid-string occurrences in log/die messages.
# Exclude "failed to verify certificate" -- this is an error string from Go's
# crypto/tls stdlib package, asserted in client/util_test.go.
# shellcheck disable=SC2016 # Backticks are literal regex chars, not command substitutions.
check_strings '["`]([Ff]ailed to [a-z]|[^"`]*[Uu]nable to [a-z])' \
  "error messages must use gerund style or 'cannot': 'Failed reading' not 'Failed to read', 'Cannot connect' not 'Unable to connect'" \
  'failed to verify certificate'

# Check 2: Contractions (n't) -> expand to full form.
#   "Can't connect" -> "Cannot connect"
#   "Don't do that" -> "Do not do that"
# The pattern looks for n't between matching quote characters (" or `),
# catching can't, don't, won't, isn't, couldn't, wouldn't, etc.
# shellcheck disable=SC2016 # Backticks are literal regex chars, not command substitutions.
check_strings '["`][^"`]*n'"'"'t[^"`]*["`]' \
  "error messages must not use contractions: 'cannot' not 'can'\\''t', 'do not' not 'don'\\''t'"

exit "${RC}"
