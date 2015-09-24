#!/bin/bash

MYDIR=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )
CIMAGE="testimage"
CNAME="speedtest"

count=${1}
if [ "x${count}" == "x" ]; then
  echo "USAGE: ${0} 10"
  echo "This creates 10 busybox containers"
  exit 1
fi

if [ "x${2}" != "xnotime" ]; then
  time ${0} ${count} notime
  exit 0
fi

${MYDIR}/../scripts/lxd-images import busybox --alias busybox

PIDS=""
for c in $(seq 1 $count); do
  lxc init busybox "${CNAME}${c}" 2>&1 &
  PIDS="$PIDS $!"
done

for pid in $PIDS; do
  wait $pid
done

echo -e "\nlxc list: All shutdown"
time lxc list 1>/dev/null

PIDS=""
for c in $(seq 1 $count); do
  lxc start "${CNAME}${c}" 2>&1 &
  PIDS="$PIDS $!"
done

for pid in $PIDS; do
  wait $pid
done

echo -e "\nlxc list: All started"
time lxc list 1>/dev/null

echo -e "\nRun completed"
