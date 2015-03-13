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

BASEURL=https://127.0.0.1:8443
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
    my_curl "$BASEURL/1.0/containers" | jq -r .metadata[] 2>/dev/null | while read -r line; do
        wait_for my_curl -X PUT "$BASEURL$line/state" -d "{\"action\":\"stop\",\"force\":true}"
    done

    [ "${lxd_pid}" -gt "0" ] && kill -9 ${lxd_pid}
    [ -n "${lxd2_pid}" ] && [ "${lxd2_pid}" -gt "0" ] && kill -9 ${lxd2_pid}

    # Apparently we need to wait a while for everything to die
    sleep 3
    rm -Rf ${LXD_DIR}
    rm -Rf ${LXD_CONF}
    [ -n "${LXD2_DIR}" ] && rm -Rf "${LXD2_DIR}"

    echo ""
    echo ""
    echo "==> Test result: $RESULT"
}

trap cleanup EXIT HUP INT TERM

if [ -z "`which lxc`" ]; then
    echo "==> Couldn't find lxc" && false
fi

. ./basic.sh
. ./database.sh
. ./fuidshift.sh
. ./migration.sh
. ./remote.sh
. ./signoff.sh
. ./snapshots.sh
. ./static_analysis.sh
. ./config.sh

if [ -n "$LXD_DEBUG" ]; then
    debug=--debug
fi

spawn_lxd() {
  # LXD_DIR is local here because since `lxc` is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.
  local LXD_DIR
  echo "==> Spawning lxd on $1 in $2"
  LXD_DIR=$2 lxd $debug --tcp $1 &

  echo "==> Confirming lxd on $1 is responsive"
  alive=0
  while [ $alive -eq 0 ]; do
    [ -e "${2}/unix.socket" ] && LXD_DIR=$2 lxc finger && alive=1
    sleep 1s
  done

  echo "==> Setting trust password"
  LXD_DIR=$2 lxc config set password foo
}

spawn_lxd 127.0.0.1:8443 $LXD_DIR
lxd_pid=$!

# allow for running a specific set of tests
if [ "$#" -gt 0 ]; then
  test_$1
  RESULT=success
  exit
fi

echo "==> TEST: commit sign-off"
test_commits_signed_off

echo "==> TEST: doing static analysis of commits"
static_analysis

echo "==> TEST: lxc remote"
test_remote

echo "==> TEST: basic usage"
test_basic_usage

echo "==> TEST: snapshots"
test_snapshots

echo "==> TEST: profiles, devices and configuration"
test_config_profiles

echo "==> TEST: uidshift"
test_fuidshift

echo "==> TEST: migration"
test_migration

# This should always be run last
echo "==> TEST: database lock"
test_database_lock

RESULT=success
