#!/bin/bash
set -eu

echo "=== RSS Calculation Validation Test ==="

# Run free command to get baseline
FREE_USED=$(free | awk '/^Mem:/ {print $3}')
echo "Used memory from free command: $FREE_USED kB"

# Get meminfo values
MEM_TOTAL=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
MEM_FREE=$(awk '/^MemFree:/ {print $2}' /proc/meminfo)
BUFFERS=$(awk '/^Buffers:/ {print $2}' /proc/meminfo)
CACHED=$(awk '/^Cached:/ {print $2}' /proc/meminfo)
SHMEM=$(awk '/^Shmem:/ {print $2}' /proc/meminfo)

# Calculate RSS using our formula
RSS=$((MEM_TOTAL - (MEM_FREE + BUFFERS + CACHED - SHMEM)))
echo "RSS from our calculation: $RSS kB"

# Display all values for debugging
echo
echo "Values from /proc/meminfo:"
echo "  MemTotal:  $MEM_TOTAL kB"
echo "  MemFree:   $MEM_FREE kB"
echo "  Buffers:   $BUFFERS kB"
echo "  Cached:    $CACHED kB"
echo "  Shmem:     $SHMEM kB"
echo "Formula: MemTotal - (MemFree + Buffers + Cached - Shmem)"
echo "       = $MEM_TOTAL - ($MEM_FREE + $BUFFERS + $CACHED - $SHMEM)"
echo "       = $MEM_TOTAL - $((MEM_FREE + BUFFERS + CACHED - SHMEM))"
echo "       = $RSS kB"
echo

# Calculate difference percentage using shell math
DIFF_RAW=$((RSS - FREE_USED))
# Calculate percentage difference: (RSS - FREE_USED) / FREE_USED * 100
DIFF_PCT=$(awk "BEGIN {print ($DIFF_RAW / $FREE_USED) * 100}")
# Get absolute value of percentage difference for comparison
ABS_DIFF_PCT=$(awk "BEGIN {print ($DIFF_PCT < 0) ? -$DIFF_PCT : $DIFF_PCT}")

echo "Difference: $DIFF_RAW kB ($DIFF_PCT%)"
echo "Absolute difference: $(printf "%.2f" "$ABS_DIFF_PCT")%"

# Test passes if difference is less than 5%
# Use awk to compare float values since bash can only handle integers
if (( $(awk "BEGIN {print ($ABS_DIFF_PCT < 5.0) ? 1 : 0}") )); then
    echo "✅ Test PASSED - difference within 5%"
    exit 0
else
    echo "❌ Test FAILED - difference exceeds 5%"
    exit 1
fi
