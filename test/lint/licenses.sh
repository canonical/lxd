#!/bin/bash
set -eu

cleanup() {
    # Restore COPYING into place
    git restore -- COPYING
}
trap cleanup EXIT HUP INT TERM

# Check LXD doesn't include non-permissive licenses (except for itself).
cp client/COPYING COPYING
go-licenses check ./...
