#!/bin/bash
set -eu

# Check LXD doesn't include non-permissive licenses (except for itself).
mv COPYING COPYING.tmp
cp client/COPYING COPYING
go-licenses check ./...
mv COPYING.tmp COPYING


