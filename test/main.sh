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
    set +x
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
    if [ -n "$LXD_DEBUG" ]; then
        set -x
    fi
    eval "$CMD"
}


wipe() {
    if type btrfs >/dev/null 2>&1; then
        rm -Rf "$1" 2>/dev/null || true
        if [ -d "$1" ]; then
            find "$1" | tac | xargs btrfs subvolume delete >/dev/null 2>&1 || true
        fi
    fi

    rm -Rf "$1"
}

curtest=setup

cleanup() {
    set +x

    if [ -n "$LXD_INSPECT" ]; then
        echo "==> Test result: $RESULT"
        if [ $RESULT != "success" ]; then
          echo "failed test: $curtest"
        fi

        echo "To poke around, use:\n LXD_DIR=$LXD_DIR sudo -E $GOPATH/bin/lxc COMMAND --config ${LXD_CONF}"
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
          do_kill_lxd $p
        fi
    done

    # Apparently we need to wait a while for everything to die
    sleep 3
    for dir in ${LXD_CONF} ${LXD_DIR} ${LXD2_DIR} ${LXD3_DIR} ${LXD4_DIR} ${LXD5_DIR} ${LXD6_DIR} ${LXD_MIGRATE_DIR} ${LXD_SERVERCONFIG_DIR}; do
        [ -n "${dir}" ] && wipe "${dir}"
    done

    rm -f devlxd-client || true
    find . -name shmounts -exec "umount" "-l" "{}" \; || true

    echo ""
    echo ""
    echo "==> Test result: $RESULT"
    if [ $RESULT != "success" ]; then
      echo "failed test: $curtest"
    fi
}

do_kill_lxd() {
  pid=$1
  kill -15 $pid
  sleep 2
  kill -9 $pid 2>/dev/null || true
}

trap cleanup EXIT HUP INT TERM

if [ -z "`which lxc`" ]; then
    echo "==> Couldn't find lxc" && false
fi

. ./basic.sh
. ./concurrent.sh
. ./exec.sh
. ./database.sh
. ./deps.sh
. ./filemanip.sh
. ./fuidshift.sh
. ./migration.sh
. ./remote.sh
. ./signoff.sh
. ./snapshots.sh
. ./static_analysis.sh
. ./config.sh
. ./serverconfig.sh
. ./profiling.sh
. ./fdleak.sh
. ./database_update.sh
. ./devlxd.sh
. ./lvm.sh
. ./image.sh

if [ -n "$LXD_DEBUG" ]; then
    debug=--debug
fi

spawn_lxd() {
  set +x
  # LXD_DIR is local here because since `lxc` is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.
  local LXD_DIR

  addr=$1
  lxddir=$2
  shift
  shift

  # Copy pre generated Certs
  cp server.crt $lxddir
  cp server.key $lxddir

  echo "==> Spawning lxd on $addr in $lxddir"
  LXD_DIR=$lxddir lxd --logfile $lxddir/lxd.log $debug $extraargs $* 2>&1 & echo $! > $lxddir/lxd.pid

  echo "==> Confirming lxd on $addr is responsive"
  alive=0
  while [ $alive -eq 0 ]; do
    [ -e "${lxddir}/unix.socket" ] && LXD_DIR=$lxddir lxc finger && alive=1
    sleep 1s
  done

  echo "==> Binding to network"
  LXD_DIR=$lxddir lxc config set core.https_address $addr

  echo "==> Setting trust password"
  LXD_DIR=$lxddir lxc config set core.trust_password foo
  if [ -n "$LXD_DEBUG" ]; then
      set -x
  fi
}

ensure_has_localhost_remote() {
    if ! lxc remote list | grep -q "localhost"; then
        (echo y; sleep 3) | lxc remote add localhost $BASEURL $debug --password foo
    fi
}

ensure_import_testimage() {
  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
    if [ -e "$LXD_TEST_IMAGE" ]; then
        lxc image import $LXD_TEST_IMAGE --alias testimage
    else
        ../scripts/lxd-images import busybox --alias testimage
    fi
  fi
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
curtest=test_commits_signed_off
test_commits_signed_off

echo "==> TEST: doing static analysis of commits"
curtest=static_analysis
static_analysis

echo "==> TEST: checking dependencies"
curtest=test_check_deps
test_check_deps

echo "==> TEST: Database schema update"
curtest=test_database_update
test_database_update

echo "==> TEST: lxc remote url"
curtest=test_remote_url
test_remote_url

echo "==> TEST: lxc remote administration"
curtest=test_remote_admin
test_remote_admin

echo "==> TEST: basic usage"
curtest=test_basic_usage
test_basic_usage

echo "==> TEST: images (and cached image expiry)"
curtest=test_image_expiry
test_image_expiry

if [ -n "$LXD_CONCURRENT" ]; then
    echo "==> TEST: concurrent exec"
    curtest=test_concurrent_exec
    test_concurrent_exec

    echo "==> TEST: concurrent startup"
    curtest=test_concurrent
    test_concurrent
fi

echo "==> TEST: lxc remote usage"
curtest=test_remote_usage
test_remote_usage

echo "==> TEST: snapshots"
curtest=test_snapshots
test_snapshots

echo "==> TEST: snapshot restore"
curtest=test_snap_restore
test_snap_restore

echo "==> TEST: profiles, devices and configuration"
curtest=test_config_profiles
test_config_profiles

echo "==> TEST: server config"
curtest=test_server_config
test_server_config

echo "==> TEST: filemanip"
curtest=test_filemanip
test_filemanip

echo "==> TEST: devlxd"
curtest=test_devlxd
test_devlxd

if type fuidshift >/dev/null 2>&1; then
    echo "==> TEST: uidshift"
    curtest=test_fuidshift
    test_fuidshift
else
    echo "==> SKIP: fuidshift (binary missing)"
fi

echo "==> TEST: migration"
curtest=test_migration
test_migration

if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    echo "===> SKIP: lvm backing (no loop device on Travis)"
else
    echo "==> TEST: lvm backing"
    curtest=test_lvm
    test_lvm
fi

curversion=`dpkg -s lxc | awk '/^Version/ { print $2 }'`
if dpkg --compare-versions "$curversion" gt 1.1.2-0ubuntu3; then
    echo "==> TEST: fdleak"
    curtest=test_fdleak
    test_fdleak
else
    # We temporarily skip the fdleak test because a bug in lxc is
    # known to make it # fail without lxc commit
    # 858377e: # logs: introduce a thread-local 'current' lxc_config (v2)
    echo "==> SKIPPING TEST: fdleak"
fi

echo "==> TEST: cpu profiling"
curtest=test_cpu_profiling
test_cpu_profiling
echo "==> TEST: memory profiling"
curtest=test_mem_profiling
test_mem_profiling

# This should always be run last
echo "==> TEST: database lock"
curtest=test_database_lock
test_database_lock

RESULT=success
