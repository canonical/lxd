#!/bin/bash
set -eu

# Update copilot instructions from two sources:
# - Extract the recommendations section from test/README.md.
# - Extract the commit message table from CONTRIBUTING.md.
# The script replaces the marked sections in .github/copilot-instructions.md.

echo "Updating .github/copilot-instructions.md from test/README.md recommendations"

tmp=""
tmp_out=""

cleanup() {
  rm -f "${tmp-}" "${tmp_out-}"
}
trap cleanup EXIT

check_markers() {
  local label="$1"
  local file="$2"
  local begin="<!-- BEGIN ${label} -->"
  local end="<!-- END ${label} -->"
  if ! grep -qF "${begin}" "${file}" || ! grep -qF "${end}" "${file}"; then
    echo "Missing ${begin}/${end} markers in ${file}"
    exit 1
  fi
}

replace_block() {
  local label="$1"
  local content="$2"
  local file="$3"
  local begin="<!-- BEGIN ${label} -->"
  local end="<!-- END ${label} -->"

  check_markers "${label}" "${file}"

  : "${tmp_out:?tmp_out must be set}"
  awk -v begin="${begin}" -v end="${end}" -v src="${content}" '\
    BEGIN { \
      while ((getline line < src) > 0) { \
        lines[++n] = line; \
      } \
    } \
    { \
      if ($0 == begin) { \
        print $0; \
        for (i = 1; i <= n; i++) print lines[i]; \
        inblock = 1; \
        next; \
      } \
      if ($0 == end) { \
        print $0; \
        inblock = 0; \
        next; \
      } \
      if (!inblock) print $0; \
    } \
  ' "${file}" > "${tmp_out}"
  mv "${tmp_out}" "${file}"
}

tmp="$(mktemp)"
tmp_out="$(mktemp)"
awk 'f{print} /^## Recommendations$/{f=1; next} /^## /{if(f){exit}}' test/README.md > "${tmp}"
if [ ! -s "${tmp}" ]; then
  echo "Failed to extract recommendations from test/README.md"
  exit 1
fi
replace_block "TEST RECOMMENDATIONS" "${tmp}" .github/copilot-instructions.md

echo "Updating commit structure table from CONTRIBUTING.md"
awk '/^\| Type/ {in_table=1} in_table {print} in_table && NF==0 {exit}' CONTRIBUTING.md > "${tmp}"
if [ ! -s "${tmp}" ]; then
  echo "Failed to extract commit table from CONTRIBUTING.md"
  exit 1
fi
replace_block "COMMIT STRUCTURE" "${tmp}" .github/copilot-instructions.md
