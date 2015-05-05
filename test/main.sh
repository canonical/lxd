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
BASEURL=https://127.0.0.1:18443
RESULT=failure

set -e
if [ -n "$LXD_DEBUG" ]; then
    set -x
fi

if [ "$USER" != "root" ]; then
    echo "The testsuite must be run as root."
    exit 1
fi

for dep in lxd lxc curl jq git xgettext sqlite3 msgmerge msgfmt; do
    type $dep >/dev/null 2>&1 || (echo "Missing dependency: $dep" && exit 1)
done

echo "==> Running the LXD testsuite"

BASEURL=https://127.0.0.1:18443
my_curl() {
  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" $@
}

wait_for() {
  op=$($@ | jq -r .operation)
  my_curl $BASEURL$op/wait
}

lxc() {
    INJECTED=0
    CMD="$(which lxc)"
    for arg in $@; do
        if [ "$arg" = "--" ]; then
            INJECTED=1
            CMD="$CMD \"--config\" \"${LXD_CONF}\" $debug"
            CMD="$CMD \"--debug\""
            CMD="$CMD --"
        else
            CMD="$CMD \"$arg\""
        fi
    done

    if [ "$INJECTED" = "0" ]; then
        CMD="$CMD \"--config\" \"${LXD_CONF}\" $debug"
    fi

    eval "$CMD"
}


wipe() {
    if type btrfs >/dev/null 2>&1; then
        btrfs subvolume list -o "$1" | awk '{print $NF}' | while read line; do
            subvol=$(echo $line | awk '{print $NF}')
            btrfs subvolume delete "/$subvol"
        done
    fi

    rm -Rf "$1"
}

cleanup() {
    if [ -n "$LXD_INSPECT" ]; then
        read -p "Tests Completed ($RESULT): hit enter to continue" x
    fi
    echo "==> Cleaning up"

    # Try to stop all the containers
    my_curl "$BASEURL/1.0/containers" | jq -r .metadata[] 2>/dev/null | while read -r line; do
        wait_for my_curl -X PUT "$BASEURL$line/state" -d "{\"action\":\"stop\",\"force\":true}"
    done

    # kill the lxds which share our pgrp as parent
    mygrp=`awk '{ print $5 }' /proc/self/stat`
    for p in `pidof lxd`; do
        pgrp=`awk '{ print $5 }' /proc/$p/stat`
        if [ "$pgrp" = "$mygrp" ]; then
            kill -9 $p
        fi
    done

    # Apparently we need to wait a while for everything to die
    sleep 3
    rm -Rf ${LXD_DIR}
    rm -Rf ${LXD_CONF}
    [ -n "${LXD2_DIR}" ] && wipe "${LXD2_DIR}"
    [ -n "${LXD3_DIR}" ] && wipe "${LXD3_DIR}"
    [ -n "${LXD4_DIR}" ] && wipe "${LXD4_DIR}"

    echo ""
    echo ""
    echo "==> Test result: $RESULT"
}

trap cleanup EXIT HUP INT TERM

if [ -z "`which lxc`" ]; then
    echo "==> Couldn't find lxc" && false
fi

. ./basic.sh
. ./concurrent.sh
. ./database.sh
. ./fuidshift.sh
. ./migration.sh
. ./remote.sh
. ./signoff.sh
. ./snapshots.sh
. ./static_analysis.sh
. ./config.sh
. ./profiling.sh
. ./fdleak.sh

if [ -n "$LXD_DEBUG" ]; then
    debug=--debug
fi

spawn_lxd() {
  # LXD_DIR is local here because since `lxc` is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.
  local LXD_DIR

  addr=$1
  lxddir=$2
  shift
  shift
  echo "==> Spawning lxd on $addr in $lxddir"
  LXD_DIR=$lxddir lxd $debug --tcp $addr $extraargs $* 2>&1 | tee $lxddir/lxd.log &

  echo "==> Confirming lxd on $addr is responsive"
  alive=0
  while [ $alive -eq 0 ]; do
    [ -e "${lxddir}/unix.socket" ] && LXD_DIR=$lxddir lxc finger && alive=1
    sleep 1s
  done

  echo "==> Setting trust password"
  LXD_DIR=$lxddir lxc config set password foo
}

spawn_lxd 127.0.0.1:18443 $LXD_DIR

export LXD2_DIR=$(mktemp -d -p $(pwd))
chmod 777 "${LXD2_DIR}"
spawn_lxd 127.0.0.1:18444 "${LXD2_DIR}"

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

echo "==> TEST: lxc remote url"
test_remote_url

echo "==> TEST: lxc remote administration"
test_remote_admin

echo "==> TEST: basic usage"
test_basic_usage

echo "==> TEST: concurrent startup"
test_concurrent

echo "==> TEST: lxc remote usage"
test_remote_usage

echo "==> TEST: snapshots"
test_snapshots

echo "==> TEST: profiles, devices and configuration"
test_config_profiles

if type fuidshift >/dev/null 2>&1; then
    echo "==> TEST: uidshift"
    test_fuidshift
else
    echo "==> SKIP: fuidshift (binary missing)"
fi

echo "==> TEST: migration"
test_migration

curversion=`dpkg -s lxc | awk '/^Version/ { print $2 }'`
if dpkg --compare-versions "$curversion" gt 1.1.2-0ubuntu3; then
    echo "==> TEST: fdleak"
    test_fdleak
else
    # We temporarily skip the fdleak test because a bug in lxc is
    # known to make it # fail without lxc commit
    # 858377e: # logs: introduce a thread-local 'current' lxc_config (v2)
    echo "==> SKIPPING TEST: fdleak"
fi

echo "==> TEST: cpu profiling"
test_cpu_profiling
echo "==> TEST: memory profiling"
test_mem_profiling

# This should always be run last
echo "==> TEST: database lock"
test_database_lock

RESULT=success
