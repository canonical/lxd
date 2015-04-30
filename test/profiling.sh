our_lxd_pid() {
    mygrp=`awk '{ print $5 }' /proc/self/stat`
    for p in `pidof lxd`; do
        pgrp=`awk '{ print $5 }' /proc/$p/stat`
        if ! grep -q "$1" /proc/$p/cmdline; then
            continue
        fi
        if [ "$pgrp" = "$mygrp" ]; then
            echo $p
            return
        fi
    done
    echo -1
}

test_cpu_profiling() {
    export LXD3_DIR=$(mktemp -d -p $(pwd))
    spawn_lxd 127.0.0.1:18445 $LXD3_DIR --cpuprofile ${LXD3_DIR}/cpu.out
    lxdpid=`our_lxd_pid cpuprofile`
    [ $lxdpid -ne -1 ]
    kill -TERM $lxdpid
    wait $lxdpid || true
    echo top5 | go tool pprof $(which lxd) ${LXD3_DIR}/cpu.out
    echo ""
}

test_mem_profiling() {
    export LXD4_DIR=$(mktemp -d -p $(pwd))
    spawn_lxd 127.0.0.1:18446 $LXD4_DIR --memprofile ${LXD4_DIR}/mem
    if [ -e ${LXD4_DIR}/mem ] ; then
        false
    fi
    lxdpid=`our_lxd_pid memprofile`
    if [ $lxdpid -eq -1 ]; then
        false
    fi
    kill -USR1 $lxdpid
    sleep 1s
    echo top5 | go tool pprof $(which lxd) ${LXD4_DIR}/mem
    echo ""
    kill -9 $lxdpid
}
