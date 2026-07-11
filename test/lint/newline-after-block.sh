#!/bin/bash
set -euo pipefail
shopt -s inherit_errexit

echo "Checking that functional blocks are followed by newlines..."

# Gather all tracked and untracked files, respecting .gitignore
mapfile -d '' raw_files < <(git ls-files -z --cached --others --exclude-standard '*.go' ':!:*.pb.go' ':!:test/mini-oidc/storage/*.go')

# Filter out any tracked files that have been deleted locally so sed doesn't crash
files=()
for f in "${raw_files[@]}"; do
  if [ -f "$f" ]; then
    files+=("$f")
  fi
done

# Exit early if there are absolutely no files to check
if [ ${#files[@]} -eq 0 ]; then
  exit 0
fi

# Process ALL existing files in one single batch
# This oneliner has a few steps:
# 1. sed:
#     a. Check for lines that contain a single closing brace (plus whitespace).
#     b. Move the pattern space window forward to the next line.
#     c. Match lines that start with a word character. This allows for a closing brace on subsequent lines.
#     d. Print the line number (=) and the filename (F) on separate lines
# 2. Stitch the line numbers and the filenames together to make a clickable link.
RESULT=$(sed -n -s -e '/^\s*}\s*$/{n;/^\s*\w/{;=;F}}' "${files[@]}" | awk 'NR%2{ln=$0;next} {print $0 ":" ln}')

if [ -n "${RESULT}" ]; then
  echo "${RESULT}"
  exit 1
fi
