#!/bin/bash

MYDIR=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )
CIMAGE="testimage"
CNAME="speedtest"

count=${1}
if [ "x${count}" == "x" ]; then
  $COUNT = 2
fi

if [ "x${2}" != "xnotime" ]; then
  time ${0} ${count} notime
  exit 0
fi

${MYDIR}/../scripts/lxd-images import busybox --alias busybox
lxc init busybox "${CNAME}"
for c in $(seq 1 $count); do
  lxc copy "${CNAME}" "${CNAME}${c}"
done

echo -e "\nlxc list: All shutdown"
time lxc list 1>/dev/null

lxc start "${CNAME}"
for c in $(seq 1 $count); do
  lxc start "${CNAME}${c}"
done

echo -e "\nlxc list: All started"
time lxc list 1>/dev/null

echo -e "\nRun completed"
