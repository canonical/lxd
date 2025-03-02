#!/bin/bash
set -e

echo "=== RSS Calculation Validation Test ==="

# Run free command to get baseline
FREE_USED=$(free | grep "^Mem:" | awk '{print $3}')
echo "Used memory from free command: $FREE_USED kB"

# Get meminfo values
MEM_TOTAL=$(grep MemTotal /proc/meminfo | awk '{print $2}')
MEM_FREE=$(grep MemFree /proc/meminfo | awk '{print $2}')
BUFFERS=$(grep Buffers /proc/meminfo | awk '{print $2}')
CACHED=$(grep "^Cached:" /proc/meminfo | awk '{print $2}')
SHMEM=$(grep "^Shmem:" /proc/meminfo | awk '{print $2}')

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
DIFF_PCT=$(awk "BEGIN {print ($DIFF_RAW / $FREE_USED) * 100}")
ABS_DIFF_PCT=$(awk "BEGIN {print ($DIFF_PCT < 0) ? -$DIFF_PCT : $DIFF_PCT}")

echo "Difference: $DIFF_RAW kB ($DIFF_PCT%)"
echo "Absolute difference: $(printf "%.2f" $ABS_DIFF_PCT)%"

# Test passes if difference is less than 5%
if (( $(awk "BEGIN {print ($ABS_DIFF_PCT < 5.0) ? 1 : 0}") )); then
    echo "✅ Test PASSED - difference within 5%"
    exit 0
else
    echo "❌ Test FAILED - difference exceeds 5%"
    exit 1
fi