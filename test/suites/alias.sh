test_alias() {
  # Get list of original aliases before test
  local original_alias_list
  original_alias_list=$(lxc alias list --format json | jq --raw-ouput 'keys[]' || echo "")

  echo "Test 1: Add a new alias"
  lxc alias add my-list "list -c ns46S"
  lxc alias list --format csv | grep -xF "my-list,list -c ns46S"

  echo "Test 2: Prevent adding duplicate alias (should fail)"
  ALIAS_ERR="$(! lxc alias add my-list "list -c -s" 2>&1 || echo fail)"
  [ "$(echo "${ALIAS_ERR}" | tail -1)" = "Error: Alias my-list already exists" ]

  echo "Test 3: Show alias in YAML format"
  lxc alias show | grep -xF "my-list: list -c ns46S"

  echo "Test 4: Rename an alias"
  lxc alias rename my-list my-list2
  ! lxc alias list --format csv | grep -F "my-list," || false
  lxc alias list --format csv | grep -xF "my-list2,list -c ns46S"

  echo "Test 5: Prevent rename to existing alias (should fail)"
  lxc alias add another-alias "list -c s"
  ALIAS_ERR="$(! lxc alias rename another-alias my-list2 2>&1 || echo fail)"
  [ "$(echo "${ALIAS_ERR}" | tail -1)" = "Error: Alias my-list2 already exists" ]

  echo "Test 6: Remove an alias"
  lxc alias remove my-list2
  ! lxc alias list --format csv | grep -F "my-list2," || false

  echo "Test 7: Prevent removing non-existent alias (should fail)"
  ALIAS_ERR="$(! lxc alias remove non-existent 2>&1 || echo fail)"
  [ "$(echo "${ALIAS_ERR}" | tail -1)" = "Error: Alias non-existent doesn't exist" ]

  echo "Test 8: alias command with spaces in target"
  lxc alias add complex "list --format csv --all-projects"
  lxc alias list --format csv | grep -xF "complex,list --format csv --all-projects"

  echo "Test 9: Alias list formats"
  formats=("json" "yaml" "compact" "table")
  for format in "${formats[@]}"; do
    lxc alias list --format "${format}" | grep -F "another-alias"
  done

  echo "Test 10: Edit aliases via stdin (non-interactive)"
  lxc alias edit <<EOF
new-alias1: "list --format csv"
new-alias2: "list -c ns"
EOF
  lxc alias list --format csv | grep -xF "new-alias1,list --format csv"
  lxc alias list --format csv | grep -xF "new-alias2,list -c ns"

  echo "Test 11: Prevent clearing all aliases with empty input"
  ALIAS_ERR="$(! echo "" | lxc alias edit 2>&1 || echo fail)"
  [ "$(echo "${ALIAS_ERR}" | tail -1)" = "Error: No aliases found in input." ]

  echo "Test 12: Handle invalid YAML in edit (non-interactive)"
  # Store current aliases count
  alias_count_before=$(lxc alias list --format json | jq length)
  ALIAS_ERR="$(! echo "invalid: yaml: [}" | lxc alias edit 2>&1 || echo fail)"
  echo "${ALIAS_ERR}" | grep -F "yaml:"

  # Verify aliases were not changed
  alias_count_after="$(lxc alias list --format json | jq --exit-status length)"
  [ "${alias_count_before}" = "${alias_count_after}" ]

  echo "Test 13: Verify alias functionality"
  lxc alias add running 'list -f csv STATUS=RUNNING'
  [ "$(lxc running || echo fail)" = "" ]

  echo "Test 14: ls alias for list command"
  lxc alias ls --format csv | grep -F "new-alias1"

  echo "Test 15: rm alias for remove command"
  lxc alias rm new-alias1
  ! lxc alias list --format csv | grep -F "new-alias1," || false

  echo "Test 16: mv alias for rename command"
  lxc alias mv new-alias2 renamed-alias
  ! lxc alias list --format csv | grep -F "new-alias2," || false
  lxc alias list --format csv | grep -xF "renamed-alias,list -c ns"

  echo "Test 17: Alias names with special characters"
  declare -A special_aliases=(
    ["alias-with-dash"]="list"
    ["alias_with_underscore"]="list --format json"
    ["alias.with.dots"]="list --format yaml"
  )

  # Add aliases
  for alias_name in "${!special_aliases[@]}"; do
    lxc alias add "${alias_name}" "${special_aliases[$alias_name]}"
  done

  # Check aliases
  for alias_name in "${!special_aliases[@]}"; do
    lxc alias list --format csv | grep -xF "${alias_name},${special_aliases[$alias_name]}"
  done

  echo "Test 18: Bulk update aliases via edit"
  lxc alias edit <<EOF
bulk-alias1: "list --format csv"
bulk-alias2: "list --format json"
bulk-alias3: "list --format yaml"
bulk-alias4: "list --format table"
EOF

  # Check bulk aliases
  bulk_formats=("csv" "json" "yaml" "table")
  for format_index in "${!bulk_formats[@]}"; do
    alias_number=$((format_index + 1))
    format_string=${bulk_formats[format_index]}
    lxc alias list --format csv | grep -xF "bulk-alias${alias_number},list --format ${format_string}"
  done

  echo "Test 19: Round-trip show to edit pipeline"
  # Declare test aliases for round-trip
  declare -A roundtrip_aliases=(
    ["roundtrip1"]="list --format csv"
    ["roundtrip2"]="list --format json"
    ["roundtrip3"]="list --format yaml"
  )

  # Add the aliases
  for alias_name in "${!roundtrip_aliases[@]}"; do
    lxc alias add "${alias_name}" "${roundtrip_aliases[$alias_name]}"
  done

  # show alias and pipe to edit
  lxc alias show | lxc alias edit

  # Get alias lists once outside the loop
  ALIAS_LIST_JSON="$(lxc alias list --format json)"
  ALIAS_LIST_CSV="$(lxc alias list --format csv)"

  # Verify that Round-trip aliases still exist and being correct
  for alias_name in "${!roundtrip_aliases[@]}"; do
    alias_value="${roundtrip_aliases[$alias_name]}"
    # check with JSON format
    jq --exit-status '.["'"${alias_name}"'"] == "'"${alias_value}"'"' <<< "${ALIAS_LIST_JSON}"
    # Check with CSV format
    grep -xF "${alias_name},${roundtrip_aliases[$alias_name]}" <<< "${ALIAS_LIST_CSV}"
  done

  # Clean up
  alias_test_cleanup "${original_alias_list}"

}

alias_test_cleanup() {
  local original_alias_list="$1"

  # Get current aliases
  local current_alias_list
  current_alias_list=$(lxc alias list --format json | jq --raw-output 'keys[]' || echo "")

  # Remove all test aliases
  local removed_count=0
  local error_count=0

  for alias in ${current_alias_list}; do
    # Skip if this alias existed before tests
    if echo "${original_alias_list}" | grep "^${alias}$"; then
      continue
    fi

    # Remove test alias
    echo "Removing test alias: ${alias}"
    if lxc alias remove "${alias}"; then
      removed_count=$((removed_count + 1))
    else
      echo "Warning: Failed to remove alias: ${alias}" >&2
      error_count=$((error_count + 1))
    fi
  done

  echo "Cleanup complete: removed ${removed_count} aliases"
  if [ "${error_count}" -gt 0 ]; then
    echo "Warning: ${error_count} aliases could not be removed" >&2
    return 1
  fi

  return 0

}
