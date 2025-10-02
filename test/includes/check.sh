# Miscellaneous test checks.

check_dependencies() {
    local dep missing
    missing=""

    for dep in "$@"; do
        if ! command -v "$dep" >/dev/null; then
            [ -n "$missing" ] && missing="$missing $dep" || missing="$dep"
        fi
    done

    if [ -n "$missing" ]; then
       echo "Missing dependencies: $missing" >&2
       return 1
    fi
}

check_empty() {
    [ -d "${1}" ] || return 0

    if [ "$(find "${1}" 2> /dev/null | wc -l)" -gt "1" ]; then
        echo "${1} is not empty, content:"
        find "${1}"
        false
    fi
}

check_empty_table() {
    [ -f "${1}" ] || return 0

    # The profiles table will never be empty since the `default` profile cannot
    # be deleted.
    if [ "$2" = 'profiles' ]; then
        if [ -n "$(sqlite3 "${1}" "SELECT 1 FROM ${2} WHERE name != 'default' LIMIT 1;")" ]; then
          echo "DB table ${2} is not empty, content:"
          sqlite3 "${1}" "SELECT * FROM ${2} WHERE name != 'default';"
          return 1
        fi
        return 0
    fi

    if [ -n "$(sqlite3 "${1}" "SELECT 1 FROM ${2} LIMIT 1;")" ]; then
        echo "DB table ${2} is not empty, content:"
        sqlite3 "${1}" "SELECT * FROM ${2};"
        return 1
    fi
}

# check_log_presence checks for specific messages in a log file.
# The order in which the reference messages are given does not matter.
check_log_presence() {
  local logfile="$1"
  shift
  local expected_messages=("$@")

  local all_found=true
  for message in "${expected_messages[@]}"; do
    if grep -qF "$message" "$logfile"; then
      echo "Found expected message: '$message'"
    else
      echo "ERROR: Missing expected message: '$message'"
      all_found=false
    fi
  done

  if [ "$all_found" = "true" ]; then
    echo "Log file check completed. All expected messages found."
    return 0
  else
    echo "Log file check completed with errors. Not all expected messages found."
    return 1
  fi
}

# check_log_order checks for specific messages in a log file and validates their order.
# * The matching priorities are in the following order:
#   1. Unix nano timestamps (timestamp=<unix_nano>)
#   2. Standard log timestamps (time="2025-02-27T10:07:50+01:00")
#   3. Line position (fallback)
# * If a message is missing, it is reported as an error.
# * If a message is out of order, it is reported as an error.
check_log_order() {
  local logfile="$1"
  local expected_messages=("${@:2}")
  local prev_line_num=0
  local prev_timestamp=""
  local prev_unix_timestamp=0
  local all_in_order=true

  for message in "${expected_messages[@]}"; do
    local grep_output
    grep_output=$(grep -nF "$message" "$logfile")

    if [[ -z "$grep_output" ]]; then
      echo "ERROR: Missing expected message: '$message'"
      echo "   (Looking for: '$message')"
      all_in_order=false
      continue
    fi

    # If multiple matches, take the first one
    local first_match
    first_match=$(echo "$grep_output" | head -n1)

    local line_num
    line_num=$(echo "$first_match" | cut -d':' -f1)

    local full_line
    full_line=$(echo "$first_match" | cut -d':' -f2-)

    # Extract timestamp=<unix_nano> with proper pattern for long integers (high priority)
    local current_unix_timestamp
    current_unix_timestamp=$(echo "$full_line" | grep -o 'timestamp=[0-9]\+' | cut -d'=' -f2)

    # Extract standard timestamp (format: time="2025-02-27T10:07:50+01:00")
    local current_timestamp
    current_timestamp=$(echo "$full_line" | grep -o 'time="[^"]*"' | cut -d'"' -f2)

    echo "DEBUG: Message: '$message'"
    echo "DEBUG: Line Number: $line_num"
    if [[ -n "$current_unix_timestamp" ]]; then
      echo "DEBUG: Unix Timestamp: $current_unix_timestamp"
    fi
    if [[ -n "$current_timestamp" ]]; then
      echo "DEBUG: Time: $current_timestamp"
    fi

    # First message is always in order
    if [[ -z "$prev_unix_timestamp" && -z "$prev_timestamp" ]]; then
      echo "Found first expected message: '$message' on line $line_num"
      prev_line_num=$line_num
      prev_timestamp=$current_timestamp
      prev_unix_timestamp=$current_unix_timestamp
      continue
    fi

    # Check order based on priority
    local in_order=false
    local order_method=""

    # Priority 1: Check unix nano timestamps if available for both messages
    if [[ -n "$current_unix_timestamp" && -n "$prev_unix_timestamp" ]]; then
      # Compare as numbers, not strings
      if (( 10#$current_unix_timestamp >= 10#$prev_unix_timestamp )); then
        in_order=true
        order_method="Unix timestamp (nanoseconds)"
      fi
    # Priority 2: Use line standard log timestamps
    elif [[ -n "$current_timestamp" && -n "$prev_timestamp" ]]; then
      # Compare timestamps as strings
      if [[ "$current_timestamp" == "$prev_timestamp" || "$current_timestamp" > "$prev_timestamp" ]]; then
        in_order=true
        order_method="Standard timestamp"
      fi
    # Priority 3: Fall back to line position.
    elif (( line_num > prev_line_num )); then
      in_order=true
      order_method="Line position"
    fi

    if $in_order; then
      echo "✓ OK: Message '$message' is in order (by $order_method)"
    else
      echo "ERROR: Expected message '$message' is out of order!"
      echo "   Found on line: $line_num, Previous message on line: $prev_line_num"
      if [[ -n "$current_unix_timestamp" && -n "$prev_unix_timestamp" ]]; then
        echo "   Current unix timestamp: $current_unix_timestamp"
        echo "   Previous unix timestamp: $prev_unix_timestamp"
      fi

      if [[ -n "$current_timestamp" && -n "$prev_timestamp" ]]; then
        echo "   Current standard timestamp: $current_timestamp"
        echo "   Previous standard timestamp: $prev_timestamp"
      fi

      all_in_order=false
    fi

    # Update tracking variables
    prev_line_num=$line_num
    prev_timestamp=$current_timestamp
    prev_unix_timestamp=$current_unix_timestamp
  done

  echo "----------------------------------------"
  if $all_in_order; then
    echo "✓ Log file check PASSED: All messages found in correct order."
    return 0
  else
    echo "✗ Log file check FAILED: Messages are out of order or missing."
    return 1
  fi
}

# runsMinimumKernel: check if the running kernel is at least the minimum version.
runsMinimumKernel() {
    local min_version min_major min_minor
    local running_version running_major running_minor
    min_version="${1}"
    min_major="$(echo "${min_version}" | cut -d. -f1)"
    min_minor="$(echo "${min_version}" | cut -d. -f2)"
    running_version="$(uname -r | cut -d. -f 1,2)"
    running_major="$(echo "${running_version}" | cut -d. -f1)"
    running_minor="$(echo "${running_version}" | cut -d. -f2)"

    if [ "${running_major}" -lt "${min_major}" ]; then
        return 1
    elif [ "${running_major}" -eq "${min_major}" ] && [ "${running_minor}" -lt "${min_minor}" ]; then
        return 1
    fi
    return 0
}
