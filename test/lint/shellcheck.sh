#!/bin/bash
set -eu

# differential-shellcheck is run via GitHub actions so avoid checking twice
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "Skipping shellcheck script (already done by differential-shellcheck action)"
    exit 0
fi

exec shellcheck test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh test/extras/*.sh
