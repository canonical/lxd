#!/bin/bash

# Skip if already ran via differential-shellcheck GH action
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "GitHub Action runner detected, skipping shellcheck script (already done)"
    exit 0
fi

exec shellcheck test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh test/extras/*.sh
