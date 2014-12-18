#!/bin/sh
export PATH=../lxc:../lxd:$PATH

# /tmp isn't moutned exec on most systems, so we can't actually start
# contianers that are created there.
export LXD_DIR=$(mktemp -d -p $(pwd))
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
. ./basic.sh
. ./snapshots.sh

echo "Spawning lxd"
lxd --tcp 127.0.0.1:8443 &
lxd_pid=$!

BASEURL=https://127.0.0.1:8443
my_curl() {
  curl -k -s --cert ~/.config/lxc/client.crt --key ~/.config/lxc/client.key $@
}

wait_for() {
  op=$($@ | jq -r .operation)
  my_curl -X POST $BASEURL$op/wait
}

echo "Confirming lxd is responsive"
alive=0
while [ $alive -eq 0 ]; do
  lxc finger && alive=1 || true
done

echo "Setting trust password"
lxc config set password foo

echo "TEST: lxc remote"
test_remote

echo "TEST: commit sign-off"
test_commits_signed_off

# Only run the tests below if we're not in travis, since travis itself is using
# openvz containers and the full test suite won't work.
if [ -n "$TRAVIS_PULL_REQUEST" ]; then
  RESULT=success
  exit
fi

echo "TEST: basic usage"
test_basic_usage

echo "TEST: snapshots"
test_snapshots

RESULT=success
