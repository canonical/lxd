#!/bin/bash
set -eu

cleanup() {
    # Restore COPYING into place
    # Save the exit code of the command that triggered the trap
    # to return it as verdict instead of the return code of the last
    # command (git restore)
    local exit_code=$?
    git restore -- COPYING
    exit "${exit_code}"
}
trap cleanup EXIT HUP INT TERM

# Check LXD doesn't include non-permissive licenses (except for itself).
cp client/COPYING COPYING

# XXX: `go install ...@latest` is almost a noop if already up to date
go install github.com/google/go-licenses@latest
go-licenses check ./...
