#!/bin/bash

MYDIR=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )
CIMAGE="testimage"
CNAME="speedtest"

count=${1}
if [ "x${count}" == "x" ]; then
  echo "USAGE: ${0} 10"
  echo "This deletes 10 busybox containers"
  exit 1
fi

if [ "x${2}" != "xnotime" ]; then
  time ${0} ${count} notime
  exit 0
fi

PIDS=""
for c in $(seq 1 $count); do
  lxc delete "${CNAME}${c}" 2>&1 &
  PIDS="$PIDS $!"
done

for pid in $PIDS; do
  wait $pid
done

echo -e "\nRun completed"
