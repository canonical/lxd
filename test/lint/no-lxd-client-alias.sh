#!/bin/bash
set -eu
set -o pipefail

if grep --include=\*.go -r -F 'lxd "github.com/canonical/lxd/client"'; then
  exit 1
fi

exit 0