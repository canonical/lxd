create_vg() {

    pvfile=$LXD_DIR/lvm-pv.img
    truncate -s 10G $pvfile
    pvloopdev=$(losetup -f)
    losetup $pvloopdev $pvfile

    pvcreate $pvloopdev
    VGDEBUG=""
    if [ -n "$LXD_DEBUG" ]; then
        VGDEBUG="-vv"
    fi
    vgcreate $VGDEBUG lxd_test_vg $pvloopdev

}

cleanup_vg() {

    if [ -n "$LXD_INSPECT_LVM" ]; then
        echo "To poke around, use:\n LXD_DIR=$LXD_DIR sudo -E $GOPATH/bin/lxc COMMAND --config ${LXD_CONF} "
        read -p "Pausing to inspect LVM state. Hit Enter to continue cleanup." x
    fi

    if [ -d "$LXD_DIR"/lxc/testcontainer ]; then
        echo "unmounting testcontainer LV"
        umount "$LXD_DIR"/lxc/testcontainer || echo "Couldn't unmount testcontainer, skipping"
    fi

    # -f removes any LVs in the VG
    vgremove -f lxd_test_vg
    losetup -d $pvloopdev
    rm $pvfile

    cleanup
}

die() {
    set +x
    message=$1
    echo ""
    echo "\033[1;31m###### Test Failed : $message\033[0m"
    exit 1
}

test_lvm() {
    if ! which vgcreate >/dev/null; then
        echo "===> SKIPPING lvm backing: vgcreate not found"
        return
    fi
    create_vg
    trap cleanup_vg EXIT HUP INT TERM

    test_delete_with_appropriate_storage
    lvremove -f lxd_test_vg/LXDPool

    test_lvm_withpool
    lvremove -f lxd_test_vg/LXDPool

    lvcreate -l 100%FREE --poolmetadatasize 500M --thinpool lxd_test_vg/test_user_thinpool
    test_lvm_withpool test_user_thinpool

    lvremove -f lxd_test_vg/test_user_thinpool
    test_remote_launch_imports_lvm
}

test_delete_with_appropriate_storage() {
    PREV_LXD_DIR=$LXD_DIR
    export LXD_DIR=$(mktemp -d -p $(pwd))
    chmod 777 "${LXD_DIR}"
    spawn_lxd 127.0.0.1:18451 "${LXD_DIR}"

    ../scripts/lxd-images import busybox --alias testimage || die "couldn't import image"
    lxc launch testimage reg-container || die "couldn't launch regular container"
    lxc config set core.lvm_vg_name "lxd_test_vg" || die "error setting core.lvm_vg_name config"
    lxc config show | grep "lxd_test_vg" || die "test_vg not in config show output"
    lxc stop reg-container || die "couldn't stop reg-container"
    lxc start reg-container || die "couldn't start reg-container"
    lxc stop reg-container || die "couldn't stop reg-container"
    lxc delete reg-container || die "couldn't delete reg-container"
    lxc image delete testimage || die "couldn't delete regular image"

    ../scripts/lxd-images import busybox --alias testimage || die "couldn't import image"

    check_image_exists_in_pool testimage LXDPool

    lxc launch testimage lvm-container || die "couldn't launch lvm container"
    lxc config unset core.lvm_vg_name || die "couldn't unset config"
    lxc stop lvm-container || die "couldn't stop lvm-container"
    lxc start lvm-container || die "couldn't start lvm-container"
    lxc stop lvm-container || die "couldn't stop lvm-container"
    lxc delete lvm-container || die "couldn't delete container"
    lxc image delete testimage || die "couldn't delete lvm-backed image"

    kill -9 `cat $LXD_DIR/lxd.pid`
    sleep 3
    rm -Rf ${LXD_DIR}
    LXD_DIR=${PREV_LXD_DIR}
}

check_image_exists_in_pool() {
    imagename=$1
    poolname=$2
    # get sha of image
    lxc image info $imagename || die "Couldn't find $imagename in lxc image info"
    testimage_sha=$(lxc image info $imagename | grep Fingerprint | cut -d' ' -f 2)

    imagelvname=$testimage_sha

    lvs --noheadings -o lv_attr lxd_test_vg/$poolname | grep "^  t" || die "$poolname not found or not a thin pool"

    lvs --noheadings -o lv_attr lxd_test_vg/$imagelvname | grep "^  V" || die "no lv named $imagelvname found or not a thin Vol."

    lvs --noheadings -o pool_lv lxd_test_vg/$imagelvname | grep "$poolname" || die "new LV not member of $poolname"

}

test_lvm_withpool() {
    poolname=$1
    PREV_LXD_DIR=$LXD_DIR
    export LXD_DIR=$(mktemp -d -p $(pwd))
    chmod 777 "${LXD_DIR}"
    spawn_lxd 127.0.0.1:18451 "${LXD_DIR}"

    lxc config set core.lvm_vg_name "zambonirodeo" && die "Shouldn't be able to set nonexistent LVM VG"
    lxc config show | grep "core.lvm_vg_name" && die "vg_name should not be set after invalid attempt"

    lxc config set core.lvm_vg_name "lxd_test_vg" || die "error setting core.lvm_vg_name config"
    lxc config show | grep "lxd_test_vg" || die "test_vg not in config show output"

    if [ -n "$poolname" ]; then
        echo " --> Testing with user-supplied thin pool name '$poolname'"
        lxc config set core.lvm_thinpool_name $poolname || die "error setting core.lvm_thinpool_name config"
        lxc config show | grep "$poolname" || die "thin pool name not in config show output."
    else
        echo " --> Testing with default thin pool name 'LXDPool'"
        poolname=LXDPool
    fi

    ../scripts/lxd-images import busybox --alias testimage

    check_image_exists_in_pool testimage $poolname

    # launch a container using that image

    lxc init testimage testcontainer || die "Couldn't init test container"

    # check that we now have a new volume in the pool
    lvs --noheadings -o pool_lv lxd_test_vg/testcontainer | grep "$poolname" || die "LV for new container not found or not in $poolname"

    mount | grep ${LXD_DIR}/lxc/testcontainer && die "LV for new container should not be mounted until container start"

    lxc start testcontainer || die "Couldn't start testcontainer"
    mount | grep ${LXD_DIR}/lxc/testcontainer || die "testcontainer LV is not mounted?"
    lxc list testcontainer | grep RUNNING || die "testcontainer doesn't seem to be running"

    lxc stop testcontainer --force || die "Couldn't stop testcontainer"
    mount | grep ${LXD_DIR}/lxc/testcontainer && die "LV for new container should be umounted after stop"

    # TODO can't do this because busybox ignores SIGPWR, breaking restart:
    # check that 'shutdown' also unmounts:
    # lxc start testcontainer || die "Couldn't re-start testcontainer"
    # lxc stop testcontainer --timeout 1 || die "Couldn't shutdown testcontainer"
    # lxc list testcontainer | grep STOPPED || die "testcontainer is still running"
    # mount | grep ${LXD_DIR}/lxc/testcontainer && die "LV for new container should be umounted after shutdown"

    lxc delete testcontainer || die "Couldn't delete testcontainer"
    lvs lxd_test_vg/testcontainer && die "testcontainer LV is still there, should've been destroyed"
    lxc image delete testimage || die "Couldn't delete testimage"

    lvs lxd_test_vg/$imagelvname && die "lv $imagelvname is still there, should be gone"

    do_kill_lxd `cat $LXD_DIR/lxd.pid`
    sleep 3
    rm -Rf ${LXD_DIR}
    LXD_DIR=${PREV_LXD_DIR}
}

test_remote_launch_imports_lvm() {
    PREV_LXD_DIR=$LXD_DIR
    export LXD_DIR=$(mktemp -d -p $(pwd))
    chmod 777 "${LXD_DIR}"
    spawn_lxd 127.0.0.1:18466 "${LXD_DIR}"

    # import busybox as a regular file-backed image
    ../scripts/lxd-images import busybox --alias testimage

    export LXD_REMOTE_DIR=$(mktemp -d -p $(pwd))
    chmod 777 "${LXD_REMOTE_DIR}"

    spawn_lxd 127.0.0.1:18467 "${LXD_REMOTE_DIR}"

    # swap env so 'lxc' will point at the new LXD
    TEMPLXDDIR=$LXD_DIR
    LXD_DIR=$LXD_REMOTE_DIR
    LXD_REMOTE_DIR=$TEMPLXDDIR

    lxc config set core.lvm_vg_name "lxd_test_vg" || die "couldn't set vg_name"
    (echo y; sleep 3; echo foo) | lxc remote add testremote 127.0.0.1:18466

    testimage_sha=$(lxc image info testremote:testimage | grep Fingerprint | cut -d' ' -f 2)
    lxc launch testremote:testimage remote-test || die "Couldn't launch from remote"

    lxc image show $testimage_sha || die "Didn't import image from remote"
    lvs --noheadings -o lv_attr lxd_test_vg/$testimage_sha | grep "^  V" || die "no lv named $testimage_sha or not a thin Vol."

    lxc list | grep remote-test | grep RUNNING || die "remote-test is not RUNNING"
    lvs --noheadings -o pool_lv lxd_test_vg/remote-test | grep LXDPool || die "LV for remote-test not found or not in LXDPool"
    lxc stop remote-test --force || die "Couldn't stop remote-test"
    lxc delete remote-test

    lvs lxd_test_vg/remote-test && die "remote-test LV is still there, should have been removed."
    lxc image delete $testimage_sha
    lvs lxd_test_vg/$testimage_sha && die "LV $testimage_sha is still there, should have been removed."

    do_kill_lxd `cat $LXD_DIR/lxd.pid`
    do_kill_lxd `cat $LXD_REMOTE_DIR/lxd.pid`
    sleep 3
    rm -Rf ${LXD_DIR}
    rm -Rf ${LXD_REMOTE_DIR}
    LXD_DIR=${PREV_LXD_DIR}
}
