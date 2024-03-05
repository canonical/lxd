#!/bin/sh -eu

# FIXME: Re-enable when https://github.com/canonical/lxd/issues/13048 is resolved.
# Note: Skipping here because `make static-analysis` calls `run-parts` on all files in this directory.
return 0

# Check LXD doesn't include non-permissive licenses (except for itself).
mv COPYING COPYING.tmp
cp client/COPYING COPYING
go-licenses check ./...
mv COPYING.tmp COPYING


