test_fdleak() {
    if ! lxc image alias list | grep -q ^testimage$; then
        if [ -e "$LXD_TEST_IMAGE" ]; then
            lxc image import $LXD_TEST_IMAGE --alias testimage
        else
            ../scripts/lxd-images import busybox --alias testimage
        fi
    fi
    lxd1_pid=`ps -ef | grep lxd | grep -v grep | awk '/127.0.0.1:18443/ { print $2 }'`
    echo "lxd1_pid is $lxd1_pid"
    beforefds=`/bin/ls /proc/$lxd1_pid/fd | wc -l`
    lxc init testimage leaktest1
    lxc info leaktest1
    [ -n "$TRAVIS_PULL_REQUEST" ] || lxc start leaktest1
    [ -n "$TRAVIS_PULL_REQUEST" ] || lxc exec leaktest1 -- ps -ef
    [ -n "$TRAVIS_PULL_REQUEST" ] || lxc stop leaktest1
    lxc delete leaktest1
    afterfds=`/bin/ls /proc/$lxd1_pid/fd | wc -l`
    leakedfds=$((afterfds - beforefds))
    [ $leakedfds -eq 0 ] || { echo "$leakedfds FDS leaked"; ls /proc/$lxd1_pid/fd -al; false; }
}
