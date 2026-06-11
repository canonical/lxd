#!/bin/bash
set -eu
set -o pipefail

echo "Checking that API extensions have a doc anchor in doc/api-extensions.md..."

API_FILE="shared/version/api.go"
DOC_FILE="doc/api-extensions.md"

missing=()
while IFS= read -r ext; do
  anchor="extension-${ext//_/-}"
  if ! grep -qF "(${anchor})=" "${DOC_FILE}"; then
    missing+=("${ext} (expected anchor: (${anchor})=)")
  fi
done < <(sed -n '/^var APIExtensions = \[\]string{/,/^}/p' "${API_FILE}" | grep -oE '"[a-zA-Z0-9_-]+"' | tr -d '"')

if [ "${#missing[@]}" -gt 0 ]; then
  echo "ERROR: the following API extensions are missing a doc anchor in ${DOC_FILE}:"
  printf '  - %s\n' "${missing[@]}"
  exit 1
fi
