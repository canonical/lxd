#!/bin/bash
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
    debug=--debug
fi

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
            CMD="$CMD $debug"
            CMD="$CMD --"
        else
            CMD="$CMD \"$arg\""
        fi
    done

    if [ "$INJECTED" = "0" ]; then
        CMD="$CMD $debug"
    fi

    eval "$CMD"
}

cleanup() {
    read -p "Tests Completed ($RESULT): hit enter to continue" x
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

spawn_lxd() {
  # LXD_DIR is local here because since `lxc` is actually a function, it
  # overwrites the environment and we would lose LXD_DIR's value otherwise.
  local LXD_DIR

  addr=$1
  lxddir=$2
  shift
  shift
  echo "==> Spawning lxd on $addr in $lxddir"
  LXD_DIR=$lxddir lxd ${DEBUG} $extraargs $* 2>&1 > $lxddir/lxd.log &

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
}

spawn_lxd 127.0.0.1:18443 $LXD_DIR

## tests go here
if [ ! -e "$LXD_TEST_IMAGE" ]; then
    echo "Please define LXD_TEST_IMAGE"
    false
fi
lxc image import $LXD_TEST_IMAGE --alias busybox

lxc image list
lxc list

NUMCREATES=5
createthread() {
    echo "createthread: I am $$"
    for i in `seq 1 $NUMCREATES`; do
        echo "createthread: starting loop $i out of $NUMCREATES"
        declare -a pids
        for j in `seq 1 20`; do
            lxc launch busybox b.$i.$j &
            pids[$j]=$!
        done
        for j in `seq 1 20`; do
            # ignore errors if the task has already exited
            wait ${pids[$j]} 2>/dev/null || true
        done
        echo "createthread: deleting..."
        for j in `seq 1 20`; do
            lxc delete b.$i.$j &
            pids[$j]=$!
        done
        for j in `seq 1 20`; do
            # ignore errors if the task has already exited
            wait ${pids[$j]} 2>/dev/null || true
        done
    done
    exit 0
}

listthread() {
    echo "listthread: I am $$"
    while [ 1 ]; do
        lxc list
        sleep 2s
    done
    exit 0
}

configthread() {
    echo "configthread: I am $$"
    for i in `seq 1 20`; do
        lxc profile create p$i
        lxc profile set p$i limits.memory 100M
        lxc profile delete p$i
    done
    exit 0
}

disturbthread() {
    echo "disturbthread: I am $$"
    while [ 1 ]; do
        lxc profile create empty
        lxc init busybox disturb1
        lxc profile apply disturb1 empty
        lxc start disturb1
        lxc exec disturb1 -- ps -ef
        lxc stop disturb1 --force
        lxc delete disturb1
        lxc profile delete empty
    done
    exit 0
}

echo "Starting create thread"
createthread 2>&1 | tee $LXD_DIR/createthread.out &
p1=$!

echo "starting the disturb thread"
disturbthread 2>&1 | tee $LXD_DIR/disturbthread.out &
pdisturb=$!

echo "Starting list thread"
listthread 2>&1 | tee $LXD_DIR/listthread.out &
p2=$!
echo "Starting config thread"
configthread 2>&1 | tee $LXD_DIR/configthread.out &
p3=$!

# wait for listthread to finish
wait $p1
# and configthread, it should be quick
wait $p3

echo "The creation loop is done, killing the list and disturb threads"

kill $p2
wait $p2 || true

kill $pdisturb
wait $pdisturb || true

RESULT=success
