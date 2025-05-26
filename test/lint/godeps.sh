#!/bin/bash
set -eu
set -o pipefail

UPDATE_LISTS="${UPDATE_LISTS:-"false"}"

# Ensure predictable sorting
export LC_ALL=C.UTF-8

rc=0
for pkg in client lxc/config lxd-agent shared/api; do
  DEP_FILE="test/godeps/$(echo "${pkg}" | sed 's/\//-/g').list"

  TAGS=""
  [ "${pkg}" = "lxd-agent" ] && TAGS="-tags agent,netgo"

  CURRENT_DEPS="$(go list ${TAGS:+${TAGS}} -f '{{ join .Deps "\n" }}' "./${pkg}" | grep -F . | sort -u)"
  OUT="$(diff --new-file -u "${DEP_FILE}" - <<< "${CURRENT_DEPS}" || true)"
  if [ -n "${OUT}" ]; then
    if [ "${UPDATE_LISTS:-"false"}" = "true" ]; then
      echo
      echo "Changed dependencies for ${pkg}:"
      echo "${OUT}"
      echo
      read -rp "Would you like to update ${DEP_FILE} with the new dependency list (y/N)? " answer
      if [ "${answer:-n}" = "y" ]; then
        echo "${CURRENT_DEPS}" > "${DEP_FILE}"
        continue
      fi
    fi

    echo "ERROR: changed dependencies for ${pkg}; please make sure this is what you want:"
    echo "${OUT}"
    echo
    rc=1
  fi
done

exit "${rc}"
