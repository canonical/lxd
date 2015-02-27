#!/bin/sh
export PATH=$GOPATH/bin:$PATH

# /tmp isn't moutned exec on most systems, so we can't actually start
# containers that are created there.
export SRC_DIR=$(pwd)
export LXD_DIR=$(mktemp -d -p $(pwd))
chmod 777 "${LXD_DIR}"
export LXD_CONF=$(mktemp -d)
export LXD_FUIDMAP_DIR=${LXD_DIR}/fuidmap
mkdir -p ${LXD_FUIDMAP_DIR}
BASEURL=https://127.0.0.1:8443
RESULT=failure
lxd_pid=0

set -e
if [ -n "$LXD_DEBUG" ]; then
    set -x
fi

echo "==> Running the LXD testsuite"

my_curl() {
  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" $@
}

wait_for() {
  op=$($@ | jq -r .operation)
  my_curl $BASEURL$op/wait
}

lxc() {
  `which lxc` $@ --config "${LXD_CONF}" $debug
}

cleanup() {
    echo "==> Cleaning up"

    # Try to stop all the containers
    my_curl "$BASEURL/1.0/containers" | jq -r .metadata[] | while read -r line; do
        wait_for my_curl -X PUT "$BASEURL$line/state" -d "{\"action\":\"stop\",\"force\":true}"
    done

    [ "${lxd_pid}" -gt "0" ] && kill -9 ${lxd_pid}

    # Apparently we need to wait a while for everything to die
    sleep 3
    rm -Rf ${LXD_DIR}
    rm -Rf ${LXD_CONF}
    echo ""
    echo ""
    echo "==> Test result: $RESULT"
}

trap cleanup EXIT HUP INT TERM

if [ -z "`which lxc`" ]; then
    echo "==> Couldn't find lxc" && false
fi

. ./static_analysis.sh
. ./remote.sh
. ./signoff.sh
. ./basic.sh
. ./snapshots.sh
. ./fuidshift.sh

if [ -n "$LXD_DEBUG" ]; then
    debug=--debug
fi

echo "==> TEST: commit sign-off"
test_commits_signed_off

echo "==> TEST: doing static analysis of commits"
static_analysis

echo "==> Spawning lxd"
lxd $debug --tcp 127.0.0.1:8443 &
lxd_pid=$!

echo "==> Confirming lxd is responsive"
alive=0
while [ $alive -eq 0 ]; do
  [ -e "${LXD_DIR}/unix.socket" ] || continue
  lxc finger && alive=1 || true
  sleep 1s
done

echo "==> Setting trust password"
lxc config set password foo

echo "==> TEST: lxc remote"
test_remote

# Only run the tests below if we're not in travis, since travis itself is using
# openvz containers and the full test suite won't work.
if [ -n "$TRAVIS_PULL_REQUEST" ]; then
  RESULT=success
  exit
fi

echo "==> TEST: basic usage"
test_basic_usage

echo "==> TEST: snapshots"
test_snapshots

echo "==> TEST: uidshift"
test_fuidshift

RESULT=success
