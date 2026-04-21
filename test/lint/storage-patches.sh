#!/bin/bash
set -eu
set -o pipefail

# Verify that every patch registered via patchGenericStorage in lxd/patches.go
# is also present in every storage driver's patches map. Missing entries cause
# a fatal error ("Patch %q is not implemented on pool %q") at daemon startup.

echo "Checking storage driver patch coverage..."

GIT_ROOT="$(git rev-parse --show-toplevel)"

RC=0

# Collect all patch names that use patchGenericStorage.
mapfile -t GENERIC_PATCHES < <(
    grep -oP '(?<=\{name: ")[^"]+(?=".*patchGenericStorage)' "${GIT_ROOT}/lxd/patches.go" | sort
)

# Collect all storage driver files that register a patches map.
mapfile -t DRIVER_FILES < <(
    grep -rlF 'd.patches = map[string]func() error{' "${GIT_ROOT}/lxd/storage/drivers/"
)

for driver_file in "${DRIVER_FILES[@]}"; do
    driver_name="$(basename "${driver_file}")"
    for patch_name in "${GENERIC_PATCHES[@]}"; do
        if ! grep -qF "\"${patch_name}\"" "${driver_file}"; then
            echo "ERROR: ${driver_name}: missing patch registration for \"${patch_name}\""
            RC=1
        fi
    done
done

exit "${RC}"
