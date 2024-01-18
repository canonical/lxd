#!/bin/sh -eu

# Check client and shared packages don't include non-permissive licenses.
# As they are licensed out under Apache-2.0.
cd client
go-licenses check ./...

cd ../shared
go-licenses check ./...
