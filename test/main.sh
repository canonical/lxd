#!/bin/sh
export PATH=../lxc:../lxd:$PATH
export LXD_DIR=$(mktemp -d)
RESULT=failure
lxd_pid=0

echo "Running the LXD testsuite"

cleanup() {
    [ "${lxd_pid}" -gt "0" ] && kill -9 ${lxd_pid}
    rm -Rf ${LXD_DIR}
    echo "Test result: $RESULT"
}

set -e

trap cleanup EXIT HUP INT TERM

. ./remote.sh
. ./signoff.sh

echo "Spawning lxd"
lxd --tcp 127.0.0.1:5555 &
lxd_pid=$!

echo "Confirming lxd is responsive"
alive=0
while [ $alive -eq 0 ]; do
  lxc ping && alive=1 || true
done

echo "Setting trust password"
lxc config set password foo

echo "TEST: lxc remote"
test_remote

echo "TEST: commit sign-off"
test_commits_signed_off

RESULT=success
