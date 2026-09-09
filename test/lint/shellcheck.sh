#!/bin/bash
set -eu
set -o pipefail
shopt -s inherit_errexit

# differential-shellcheck is run via GitHub actions so avoid checking twice
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "Skipping shellcheck script (already done by differential-shellcheck action)"
    exit 0
fi

# snap scripts must use POSIX compliant shell (no bashisms)
find snap/local snap/hooks -type f -exec grep -lZ '^#!/bin/sh' {} + | xargs -0 shellcheck --shell=sh

# snap scripts must use POSIX compliant shell (no bashisms)
find snap/local snap/hooks -type f -exec grep -lZ '^#!/bin/sh' {} + | xargs -0 shellcheck --shell=sh

# Avoid scooping in files that are not scripts (like test/snap/COPYING)
mapfile -t snap_scripts < <(grep -l '^#!/bin/bash' test/snap/*)

exec shellcheck test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh "${snap_scripts[@]}"
