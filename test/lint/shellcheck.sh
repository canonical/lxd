#!/bin/bash

# differential-shellcheck runs on PR
if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ]; then
    echo "Skipping shellcheck script during PR tests (already done by differential-shellcheck action)"
    exit 0
fi

exec shellcheck test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh test/extras/*.sh
