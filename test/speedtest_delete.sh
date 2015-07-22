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

lxc delete "${CNAME}"
for c in $(seq 1 $count); do
  lxc delete "${CNAME}${c}"
done
echo -e "\nRun completed"
